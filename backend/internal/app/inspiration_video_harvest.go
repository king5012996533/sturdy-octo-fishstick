package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	// inspirationVideoDetailTimeout 抓一个作品页的预算。作品页只有几百 KB，
	// 但上游偶发慢响应，一次抓取应该坏掉一条而不是卡住整批。
	inspirationVideoDetailTimeout = 30 * time.Second
	// inspirationVideoDetailMaxBytes 是作品页的读取上限：我们要的只是其中一行
	// 成片地址，页面再长也不该把整段 HTML 读进内存。
	inspirationVideoDetailMaxBytes = 4 << 20
)

var (
	// 作品页地址形如 https://www.liblib.tv/detail/<32 位十六进制>。
	liblibDetailPathPattern = regexp.MustCompile(`^/(?:[a-z-]+/)*detail/([0-9a-fA-F]{32})/?$`)
	// 成片地址在 Next.js 的流式载荷里，可能被转义成 \"finalOutput\"。
	// 成片后缀不固定：同一个作品池里 mp4 与 m4v 都有，只认 .mp4 会让一条作品
	// 被误判成"没有成片"。
	liblibFinalOutputPattern = regexp.MustCompile(`finalOutput\\?"?\s*:\s*\\?"(https?://[^"\\]+\.(?:mp4|m4v|mov|webm))`)
)

// InspirationVideoHarvestOptions 控制一次成片地址抓取。
type InspirationVideoHarvestOptions struct {
	// Overwrite 为真时重新取一遍已有地址，用于上游换了成片但作品页没变的情况。
	Overwrite bool
	// Limit 限制本次处理的条目数（0 表示不限制）。
	Limit int
}

// InspirationVideoHarvestItem 是一条条目的处理结果。
type InspirationVideoHarvestItem struct {
	InspirationID string `json:"inspirationId"`
	Title         string `json:"title"`
	DetailURL     string `json:"detailUrl"`
	VideoURL      string `json:"videoUrl"`
	// State 取 harvested / kept / skipped / failed。
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

// InspirationVideoHarvestResult 汇总一次抓取。
type InspirationVideoHarvestResult struct {
	Scanned   int                           `json:"scanned"`
	Harvested int                           `json:"harvested"`
	Kept      int                           `json:"kept"`
	Skipped   int                           `json:"skipped"`
	Failed    int                           `json:"failed"`
	Items     []InspirationVideoHarvestItem `json:"items"`
}

// HarvestInspirationVideos 把广场视频条目的成片地址记进 VideoURL，供前台按需播放。
//
// 为什么只记地址、不把成片抓回来：成片中位 292MB、最大 1.5GB，整池 80 条合计 32GB，
// 平台存不下也不该存。封面那种几十 KB 的静态图抓回本地是对的，成片只能留在上游，
// 由浏览器直连、按需拉分片——本站既不代理也不占带宽，这也是这条路径唯一的承载约束。
//
// 优先取 HLS 播放列表而不是原片：同一个作品有 1080p(12.6Mbps) / 720p(2.8Mbps) /
// 480p(1.2Mbps) 三档，原片是那个 1080p 原件（几百 MB），直接播等于让每个看灵感的人
// 都下一部三五百兆的片子。播放列表取不到时才回落原片。
func (s *Service) HarvestInspirationVideos(ctx context.Context, options InspirationVideoHarvestOptions) (*InspirationVideoHarvestResult, error) {
	if s.repo == nil {
		return nil, errors.New("仓库未初始化")
	}
	records, err := s.repo.AdminCreationInspirations()
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: inspirationVideoDetailTimeout}
	result := &InspirationVideoHarvestResult{Items: make([]InspirationVideoHarvestItem, 0, len(records))}
	for index := range records {
		record := &records[index]
		if options.Limit > 0 && result.Scanned >= options.Limit {
			break
		}
		// 只认视频条目 + LibTV 作品页来源：这两条都不满足时没有可推导的成片地址，
		// 计入 skipped 而不是 failed——运营手录的图文条目天然走不到这一步。
		detail := strings.TrimSpace(record.SourceURL)
		uuid := liblibDetailUUID(detail)
		if record.Mode != "video" || uuid == "" {
			result.Skipped++
			continue
		}
		result.Scanned++
		item := InspirationVideoHarvestItem{InspirationID: record.ID, Title: record.Title, DetailURL: detail}
		if record.VideoURL != "" && !options.Overwrite {
			item.State = "kept"
			item.VideoURL = record.VideoURL
			result.Kept++
			result.Items = append(result.Items, item)
			continue
		}
		videoURL, err := fetchInspirationVideoURL(ctx, client, uuid)
		if err != nil {
			item.State = "failed"
			item.Error = err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}
		record.VideoURL = videoURL
		record.UpdatedAt = time.Now()
		if err := s.repo.SaveCreationInspiration(record); err != nil {
			item.State = "failed"
			item.Error = err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}
		item.State = "harvested"
		item.VideoURL = videoURL
		result.Harvested++
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// liblibDetailBaseURL 是作品页基地址；测试里替换成本地假页面，
// 只有这一处拼接，不需要为可测性往 Service 上挂一个永远只有生产值的字段。
var liblibDetailBaseURL = "https://www.liblib.tv"

// fetchInspirationVideoURL 打开作品页，取出成片地址并挑一个能播的形态。
func fetchInspirationVideoURL(ctx context.Context, client *http.Client, uuid string) (string, error) {
	detail := strings.TrimRight(liblibDetailBaseURL, "/") + "/detail/" + uuid
	page, err := getInspirationVideoPage(ctx, client, detail)
	if err != nil {
		return "", err
	}
	output := extractLiblibFinalOutput(page)
	if output == "" {
		return "", errors.New("作品页里没有成片地址（可能已下架，或这条只有图）")
	}
	if playlist := hlsPlaylistFromOutput(output); playlist != "" {
		if err := probeInspirationVideoPlaylist(ctx, client, playlist); err == nil {
			return playlist, nil
		}
	}
	// 播放列表探测失败不算错误：上游不是每个作品都转过码，原片至少能播。
	return output, nil
}

// extractLiblibFinalOutput 从作品页里取出本条作品的成片地址。
//
// 先锚到 initialDetail：那是"这一条作品"的载荷，页面别处（相关推荐之类）也可能出现
// finalOutput。当前上游页面里它确实只有一份，但"取第一个 finalOutput"是一种巧合式的
// 正确——上游哪天把相关推荐也塞进同一段载荷，取到的就是别人的成片，而卡片上不会有
// 任何迹象。锚不到时才退回整页搜，保住对页面结构调整的容忍度。
func extractLiblibFinalOutput(page string) string {
	scope := page
	if index := strings.Index(page, "initialDetail"); index >= 0 {
		scope = page[index:]
	}
	match := liblibFinalOutputPattern.FindStringSubmatch(scope)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

// hlsPlaylistFromOutput 由成片地址推导 HLS 播放列表地址。
//
// 推导而不是去页面里另找一个地址：作品页同时挂着十几个作品的成片（相关推荐），
// 按"页面里第一个 m3u8"取会张冠李戴；而上游把播放列表固定放在成片的同名目录下。
func hlsPlaylistFromOutput(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Path == "" {
		return ""
	}
	extension := path.Ext(parsed.Path)
	if extension == "" {
		return ""
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, extension) + "/master.m3u8"
	return parsed.String()
}

func getInspirationVideoPage(ctx context.Context, client *http.Client, rawURL string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "text/html")
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("作品页返回 %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, inspirationVideoDetailMaxBytes))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// probeInspirationVideoPlaylist 确认播放列表真的可用。
//
// 只看状态码不够：上游对不存在的转码目录也可能回 200 的 XML 错误体，
// 存进库里就是一个永远转圈、又不会报错的播放器。这里必须认出 #EXTM3U。
func probeInspirationVideoPlaylist(ctx context.Context, client *http.Client, rawURL string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Range", "bytes=0-1023")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("播放列表返回 %d", response.StatusCode)
	}
	head, err := io.ReadAll(io.LimitReader(response.Body, 1024))
	if err != nil {
		return err
	}
	if !strings.HasPrefix(strings.TrimSpace(string(head)), "#EXTM3U") {
		return errors.New("返回内容不是 HLS 播放列表")
	}
	return nil
}

// liblibDetailUUID 从作品页地址里取出作品标识；取不到就说明这条不是 LibTV 来源。
func liblibDetailUUID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	// 必须限定在上游域名上：这个值接下来会被拼成实际请求的地址，
	// 只按路径形状匹配等于允许库里任何一行把我们指去抓任意站点。
	switch strings.ToLower(parsed.Host) {
	case "www.liblib.tv", "liblib.tv":
	default:
		return ""
	}
	match := liblibDetailPathPattern.FindStringSubmatch(parsed.Path)
	if len(match) != 2 {
		return ""
	}
	return strings.ToLower(match[1])
}
