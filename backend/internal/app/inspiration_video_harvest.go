package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"infinite-canvas/backend/internal/asset"
)

const (
	// inspirationVideoDetailTimeout 拉一个模板详情的预算。详情接口只有几百 KB，
	// 但上游偶发慢响应，一次抓取应该坏掉一条而不是卡住整批。
	inspirationVideoDetailTimeout = 30 * time.Second
	// inspirationVideoDetailMaxBytes 是详情响应的读取上限：我们要的只是成片地址与快照，
	// 上游再返回什么也不该把整段响应读进内存。
	inspirationVideoDetailMaxBytes = 8 << 20
)

var (
	// 作品页地址形如 https://www.liblib.tv/detail/<32 位十六进制>。
	liblibDetailPathPattern = regexp.MustCompile(`^/(?:[a-z-]+/)*detail/([0-9a-fA-F]{32})/?$`)
)

// InspirationVideoHarvestOptions 控制一次成片地址与配方抓取。
type InspirationVideoHarvestOptions struct {
	// Overwrite 为真时重新取一遍已有数据，用于上游换了成片或改了配方的情况。
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
	// RecipeVideoModel 与 RecipeImages 是这次同时抓到的复刻配方摘要：只有提示词的
	// 条目复刻不出来，命令的输出必须能看出配方到底有没有落到库里。
	RecipeVideoModel string `json:"recipeVideoModel,omitempty"`
	RecipeImages     int    `json:"recipeImages"`
	// State 取 harvested / kept / skipped / failed。
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

// InspirationVideoHarvestResult 汇总一次抓取。
type InspirationVideoHarvestResult struct {
	Scanned   int `json:"scanned"`
	Harvested int `json:"harvested"`
	Kept      int `json:"kept"`
	Skipped   int `json:"skipped"`
	Failed    int `json:"failed"`
	// Images / ImageFailures 是参考图的落库与失败张数：参考图缺一张不影响成片能否播放，
	// 但它正是"复刻不出来"的主因，所以必须单独报出来，不能被条目级的成功掩盖。
	Images        int                           `json:"images"`
	ImageFailures int                           `json:"imageFailures"`
	Items         []InspirationVideoHarvestItem `json:"items"`
}

// HarvestInspirationVideos 把广场视频条目的成片地址与复刻配方写进条目。
//
// 为什么只记地址、不把成片抓回来：成片中位 292MB、最大 1.5GB，整池 80 条合计 32GB，
// 平台存不下也不该存。封面那种几十 KB 的静态图抓回本地是对的，成片只能留在上游，
// 由浏览器直连、按需拉分片——本站既不代理也不占带宽，这也是这条路径唯一的承载约束。
//
// 同时摘出复刻配方：上游的"模板"是整张画布，提示词只是其中一个文本节点，真正决定
// 画面的是参考图、视频模型与时长比例。只搬提示词的话，用户拿到的输入框看起来一样，
// 生成的却是另一个东西（库里那条《时尚服饰TVC》的提示词引用着 {{Portrait 1..4}}，
// 没有图就只能生成四张随机脸）。参考图落本地资源库，见 harvestInspirationReferenceImages。
func (s *Service) HarvestInspirationVideos(ctx context.Context, options InspirationVideoHarvestOptions) (*InspirationVideoHarvestResult, error) {
	if s.repo == nil {
		return nil, errors.New("仓库未初始化")
	}
	records, err := s.repo.AdminCreationInspirations()
	if err != nil {
		return nil, err
	}
	store := asset.NewFileStore(s.dataDir)
	client := &http.Client{Timeout: inspirationVideoDetailTimeout}
	// 参考图单独一个客户端，理由见 inspirationReferenceDownloadLimit。
	imageClient := &http.Client{Timeout: inspirationReferenceDownloadLimit}
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
		// 成片地址与配方各自判断是否缺项：库里现存的 55 条只有成片地址、没有配方，
		// 整条按"已抓过"跳过的话，它们永远补不上配方。
		needVideo := strings.TrimSpace(record.VideoURL) == "" || options.Overwrite
		needRecipe := strings.TrimSpace(record.RecipeVideoModel) == "" || options.Overwrite
		if !needVideo && !needRecipe {
			item.State = "kept"
			item.VideoURL = record.VideoURL
			item.RecipeVideoModel = record.RecipeVideoModel
			item.RecipeImages = len(splitInspirationRecipeImageIDs(record.RecipeImageIDs))
			result.Kept++
			result.Items = append(result.Items, item)
			continue
		}
		template, err := fetchInspirationTemplateDetail(ctx, client, uuid)
		if err != nil {
			item.State = "failed"
			item.Error = err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}
		if needVideo {
			videoURL, err := playableInspirationVideoURL(ctx, client, template.FinalOutput)
			if err != nil {
				item.State = "failed"
				item.Error = err.Error()
				result.Failed++
				result.Items = append(result.Items, item)
				continue
			}
			record.VideoURL = videoURL
			item.VideoURL = videoURL
		}
		if needRecipe {
			recipe := inspirationRecipeFromSnapshot(template.FinalOutput, template.SnapshotData)
			record.RecipeVideoModel = recipe.VideoModel
			record.RecipeVideoMode = recipe.VideoMode
			record.RecipeRatio = recipe.Ratio
			record.RecipeResolution = recipe.Resolution
			record.RecipeDurationSeconds = recipe.DurationSeconds
			imageIDs, failed, _ := s.harvestInspirationReferenceImages(ctx, imageClient, store, recipe.ImageURLs, options.Overwrite)
			record.RecipeImageIDs = joinInspirationRecipeImageIDs(imageIDs)
			item.RecipeVideoModel = recipe.VideoModel
			item.RecipeImages = len(imageIDs)
			result.Images += len(imageIDs)
			result.ImageFailures += failed
		}
		record.UpdatedAt = time.Now()
		if err := s.repo.SaveCreationInspiration(record); err != nil {
			item.State = "failed"
			item.Error = err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			continue
		}
		item.State = "harvested"
		result.Harvested++
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// liblibTemplateDetailBaseURL 是模板详情接口的基地址；测试里替换成本地假服务，
// 只有这一处拼接，不需要为可测性往 Service 上挂一个永远只有生产值的字段。
var liblibTemplateDetailBaseURL = "https://api.liblib.tv"

// liblibTemplateDetail 是一次详情抓取里我们真正要用的两样东西。
type liblibTemplateDetail struct {
	FinalOutput  string
	SnapshotData string
}

// fetchInspirationTemplateDetail 拉一次模板详情。
//
// 走详情接口而不是抓作品页 HTML：一次请求同时拿到成片地址与整张画布快照，体积减半，
// 也不必再对转义过的页面载荷做正则——配方是结构化的，用正则去抠只会写出一个随上游
// 转义方式变化而碎的解析器。
func fetchInspirationTemplateDetail(ctx context.Context, client *http.Client, uuid string) (*liblibTemplateDetail, error) {
	endpoint := strings.TrimRight(liblibTemplateDetailBaseURL, "/") +
		"/api/community/project/template/detail?projectTemplateUuid=" + url.QueryEscape(uuid)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("模板详情返回 %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, inspirationVideoDetailMaxBytes))
	if err != nil {
		return nil, err
	}
	var payload liblibTemplateDetailResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("模板详情不是合法 JSON：%w", err)
	}
	// 上游用 code 而不是 HTTP 状态表达业务失败，0 之外都当没有这条作品处理。
	if payload.Code != 0 {
		return nil, fmt.Errorf("模板详情返回业务码 %d", payload.Code)
	}
	return &liblibTemplateDetail{
		FinalOutput:  strings.TrimSpace(payload.Data.Detail.FinalOutput),
		SnapshotData: payload.Data.Detail.SnapshotData,
	}, nil
}

// playableInspirationVideoURL 从成片地址挑一个能播的形态。
//
// 优先 HLS 播放列表：同一个作品有 1080p(12.6Mbps) / 720p(2.8Mbps) / 480p(1.2Mbps)
// 三档，原片就是那个 1080p 原件（几百 MB），直接播等于让每个看灵感的人都下一部
// 三五百兆的片子。播放列表探测失败不算错误——上游不是每个作品都转过码，原片至少能播。
func playableInspirationVideoURL(ctx context.Context, client *http.Client, finalOutput string) (string, error) {
	output := strings.TrimSpace(finalOutput)
	if output == "" {
		return "", errors.New("这条作品没有成片地址（可能已下架，或这条只有图）")
	}
	if playlist := hlsPlaylistFromOutput(output); playlist != "" {
		if err := probeInspirationVideoPlaylist(ctx, client, playlist); err == nil {
			return playlist, nil
		}
	}
	return output, nil
}

// hlsPlaylistFromOutput 由成片地址推导 HLS 播放列表地址。
//
// 推导而不是去详情里另找一个地址：快照里存着一条作品流程里每个视频节点的产物，
// 按"第一个 m3u8"取会张冠李戴；而上游把播放列表固定放在成片的同名目录下。
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
