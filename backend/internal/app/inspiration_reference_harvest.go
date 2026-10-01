package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

// InspirationReferenceOwnerID 是抓取回来的广场参考图在资源表里的归属者。
//
// 和封面分成两个虚拟用户而不是共用：封面回答"这张卡片长什么样"，参考图回答"这条作品
// 是怎么生成的"，一个失效时不该牵连另一个，清理时也能分开。
const InspirationReferenceOwnerID = "platform-inspiration-references"

// 参考图的下载预算与体积上限。
//
// 字节上限是兜底而不是常态：我们把请求交给上游图床的图片处理（见
// inspirationReferenceFetchURL），正常回来的是几百 KB 的 JPEG。但上游处理失败时
// `ignore-error,1` 会把原图原样返回，实测最大 20MB，所以留到 24MB 而不是贴着几百 KB 卡。
const (
	inspirationReferenceMaxBytes = 24 << 20
	// 参考图自己的下载预算。比拉详情的 30 秒宽：正常只有几百 KB，但上游缩图失败时
	// 会把二十兆的原图原样返回，用拉 JSON 的预算去搬它，卡住的正好是我们要救的那批图。
	inspirationReferenceDownloadLimit = 60 * time.Second
)

// 上游图床的图片处理指令。上游把产出图原样放在阿里云 OSS 上：一张角色参考图是
// 5504×3072 的 19MB PNG。我们最终只会保留 1280px 的 JPEG，所以让上游先缩再传，
// 一次全量抓取的图片流量从几百 MB 掉到一个数量级以下——这条链是运维命令，但流量
// 也要走这台机器的出口，能不搬的字节就不搬。
//
// m_lfit 是"等比缩进 2048 的框"，实测不会把小图放大；format,jpg 顺带把 PNG 转成
// 我们要的形态；ignore-error,1 让上游处理失败时退回原图，而不会让我们拿到一段错误 XML。
const inspirationReferenceProcessQuery = "x-oss-process=image/resize,m_lfit,w_2048,h_2048/format,jpg/ignore-error,1"

// inspirationReferenceUpstreamHostSuffix 是认这个处理的图床后缀。
//
// 只认已知图床：换成别的 CDN 时这串参数最多是噪音，但签过名的地址多一个查询串就是
// 403。池子里另一个 host（liblibai-online.liblib.cloud）不在其列，它的产出图本来就不大。
const inspirationReferenceUpstreamHostSuffix = ".liblib.art"

// harvestInspirationReferenceImages 把一条作品的参考图收进本地资源库，返回资源 ID。
//
// 为什么要落本地、而不是像成片那样热链：参考图必须能被用户"带走"——使用时它会作为
// 参考素材进入用户自己的资源库，再把字节从上游拉一遍。落本地意味着出口只剩平台自己的
// 签名地址，上游加防盗链也不会让"复刻"在点下去的那一刻才发现图没了。
//
// 单张失败只是少一张参考图，不影响这条作品的其它配方；调用方据 failed 计数汇报。
func (s *Service) harvestInspirationReferenceImages(
	ctx context.Context,
	client *http.Client,
	store *asset.FileStore,
	sources []string,
	overwrite bool,
) ([]string, int, error) {
	ids := make([]string, 0, len(sources))
	failed := 0
	var firstErr error
	for _, source := range sources {
		resourceID, err := s.harvestInspirationReferenceImage(ctx, client, store, source, overwrite)
		if err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		ids = append(ids, resourceID)
	}
	return ids, failed, firstErr
}

// harvestInspirationReferenceImage 处理单张参考图：已有产物直接复用，否则下载并重编码。
func (s *Service) harvestInspirationReferenceImage(
	ctx context.Context,
	client *http.Client,
	store *asset.FileStore,
	source string,
	overwrite bool,
) (string, error) {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		return "", errors.New("参考图地址为空")
	}
	resourceID := inspirationReferenceResourceID(trimmed)
	existing, err := s.repo.Resource(resourceID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	objectKey := inspirationReferenceObjectKey(resourceID)
	if existing != nil && !overwrite && localResourceFileExists(store, existing.ObjectKey) {
		return resourceID, nil
	}
	body, err := downloadInspirationReferenceImage(ctx, client, trimmed)
	if err != nil {
		return "", err
	}
	decoded, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		// 解不出来就不收：宁可在命令输出里报一条失败，也不要往资源库里塞一张前台
		// 显示不了、生成侧也读不懂的二进制（webp 之类需要额外解码器）。
		return "", fmt.Errorf("参考图无法解码：%w", err)
	}
	encoded, err := downscaleToJPEG(decoded, inspirationReferenceMaxEdge, inspirationReferenceQuality)
	if err != nil {
		return "", err
	}
	if err := store.Write(objectKey, bytes.NewReader(encoded)); err != nil {
		return "", err
	}
	now := time.Now()
	resource := &model.Resource{
		ID:       resourceID,
		UserID:   InspirationReferenceOwnerID,
		Kind:     "image",
		Status:   model.ResourceStatusReady,
		Provider: "local",
		// 对象键固定，重跑覆盖不会留下孤儿文件。
		ObjectKey: objectKey,
		MimeType:  "image/jpeg",
		Size:      int64(len(encoded)),
	}
	if existing != nil {
		resource.CreatedAt = existing.CreatedAt
	} else {
		resource.CreatedAt = now
	}
	resource.UpdatedAt = now
	if err := s.repo.SaveResource(resource); err != nil {
		return "", err
	}
	return resourceID, nil
}

// inspirationReferenceResourceID 由源地址派生资源 ID。
//
// 确定性派生：同一个角色参考图会出现在多条作品里，重跑一次抓取必须命中同一行，否则
// 每跑一次就给资源表添一批新记录、给磁盘添一批新文件。
func inspirationReferenceResourceID(sourceURL string) string {
	sum := sha256.Sum256([]byte("inspiration-reference:" + strings.TrimSpace(sourceURL)))
	return hex.EncodeToString(sum[:16])
}

// inspirationReferenceObjectKey 给出确定的本地对象键。
//
// 不套用上传用的日期分片：广场参考图总共几百张、且由源地址唯一决定，固定路径让
// "重跑覆盖"这件事变得显然。
func inspirationReferenceObjectKey(resourceID string) string {
	return "users/" + InspirationReferenceOwnerID + "/image/inspiration-references/" + resourceID + ".jpg"
}

// inspirationReferenceFetchURL 给上游图床的地址挂上"先缩后传"。
//
// 只改真正发出去的请求，资源 ID 仍然由**原始**地址派生：换个缩图参数不该让同一张图
// 变成另一行资源。
func inspirationReferenceFetchURL(source string) string {
	parsed, err := url.Parse(strings.TrimSpace(source))
	if err != nil || parsed.Host == "" {
		return source
	}
	host := strings.ToLower(parsed.Hostname())
	if host != strings.TrimPrefix(inspirationReferenceUpstreamHostSuffix, ".") &&
		!strings.HasSuffix(host, inspirationReferenceUpstreamHostSuffix) {
		return source
	}
	// 地址自带处理参数时不动它：两串 x-oss-process 叠在一起，上游取哪一个都说不准。
	if parsed.RawQuery != "" {
		return source
	}
	parsed.RawQuery = inspirationReferenceProcessQuery
	return parsed.String()
}

// downloadInspirationReferenceImage 取回参考图原始字节。
func downloadInspirationReferenceImage(ctx context.Context, client *http.Client, source string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, inspirationReferenceFetchURL(source), nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("上游返回 %d", response.StatusCode)
	}
	// 多读一个字节判断越界，比信 Content-Length 可靠：上游可能不给长度。
	body, err := io.ReadAll(io.LimitReader(response.Body, inspirationReferenceMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errors.New("上游返回空响应")
	}
	if int64(len(body)) > inspirationReferenceMaxBytes {
		return nil, fmt.Errorf("参考图超过 %d 字节上限", inspirationReferenceMaxBytes)
	}
	return body, nil
}
