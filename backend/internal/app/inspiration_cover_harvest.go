package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

// InspirationCoverOwnerID 是抓取回来的广场封面在资源表里的归属者。
//
// 用一个固定的虚拟用户，而不是某位管理员账号：广场封面是平台内容，既不该出现在
// 任何人的"我的资源"里，也不该在某位管理员被停用、被删除时跟着一起消失。
const InspirationCoverOwnerID = "platform-inspiration-covers"

// inspirationCoverMaxBytes 是单张封面的体积上限。
//
// 广场首屏一次要出几十张图：收进来一张十几兆的原图，用户拿到的是一面墙的加载
// 进度条，而不是灵感。宁可这张失败并保留原地址，也不要给整页拖后腿。
const inspirationCoverMaxBytes = 8 << 20

// inspirationCoverDownloadTimeout 是单张封面的下载预算。
// 上游图床慢或半死时，一次抓取应该坏掉一张图，而不是把整个命令卡死。
const inspirationCoverDownloadTimeout = 30 * time.Second

// InspirationCoverHarvestOptions 控制一次封面抓取。
type InspirationCoverHarvestOptions struct {
	// Overwrite 为真时重新下载已有封面，用于上游换了图但地址没变的情况。
	Overwrite bool
	// Limit 限制本次处理的条目数（0 表示不限制），便于先跑通几条再全量。
	Limit int
}

// InspirationCoverHarvestItem 是一条条目的处理结果，让命令输出的明细可以逐条核对。
type InspirationCoverHarvestItem struct {
	InspirationID string `json:"inspirationId"`
	Title         string `json:"title"`
	SourceURL     string `json:"sourceUrl"`
	ResourceID    string `json:"resourceId"`
	Bytes         int64  `json:"bytes"`
	// State 取 harvested / reused / failed，分别代表本次下载、复用已有产物、失败。
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

// InspirationCoverHarvestResult 汇总一次抓取，字段与命令输出一一对应。
type InspirationCoverHarvestResult struct {
	Scanned   int                           `json:"scanned"`
	Harvested int                           `json:"harvested"`
	Reused    int                           `json:"reused"`
	Failed    int                           `json:"failed"`
	Items     []InspirationCoverHarvestItem `json:"items"`
}

// HarvestInspirationCovers 把广场条目里仍指向外部图床的封面抓回本地资源库，
// 并把条目改成引用本地产物（接口返回时再现场签名）。
//
// 为什么要抓回来：此前封面直接热链上游图床，那边一加防盗链、一改目录规则，广场
// 就是一片死图，而死图在前台看起来和"这条灵感本身坏了"没有区别。抓回本地之后，
// 图片出口只剩平台自己的签名地址，上游怎么变都不影响前台。
//
// 只处理 http(s) 开头的地址：库里存本地路径或相对路径的条目已经是自己人了。
func (s *Service) HarvestInspirationCovers(ctx context.Context, options InspirationCoverHarvestOptions) (*InspirationCoverHarvestResult, error) {
	if s.repo == nil {
		return nil, errors.New("仓库未初始化")
	}
	records, err := s.repo.AdminCreationInspirations()
	if err != nil {
		return nil, err
	}
	store := asset.NewFileStore(s.dataDir)
	client := &http.Client{Timeout: inspirationCoverDownloadTimeout}
	result := &InspirationCoverHarvestResult{Items: make([]InspirationCoverHarvestItem, 0, len(records))}
	for index := range records {
		record := &records[index]
		if options.Limit > 0 && result.Scanned >= options.Limit {
			break
		}
		source := strings.TrimSpace(record.CoverURL)
		if !isHarvestableCoverURL(source) {
			continue
		}
		result.Scanned++
		item := s.harvestInspirationCover(ctx, client, store, record, source, options.Overwrite)
		switch item.State {
		case "harvested":
			result.Harvested++
		case "reused":
			result.Reused++
		default:
			result.Failed++
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// harvestInspirationCover 处理单条条目：复用已有产物或下载后落库，两种情况都要
// 把条目重新指到资源 ID 上。
func (s *Service) harvestInspirationCover(
	ctx context.Context,
	client *http.Client,
	store *asset.FileStore,
	record *model.CreationInspiration,
	source string,
	overwrite bool,
) InspirationCoverHarvestItem {
	item := InspirationCoverHarvestItem{
		InspirationID: record.ID,
		Title:         record.Title,
		SourceURL:     source,
		ResourceID:    inspirationCoverResourceID(source),
	}
	existing, err := s.repo.Resource(item.ResourceID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		item.State = "failed"
		item.Error = err.Error()
		return item
	}
	if existing != nil && !overwrite && localResourceFileExists(store, existing.ObjectKey) {
		// 资源 ID 由源地址派生：同一张封面重复抓到的是同一行、同一个文件，
		// 所以重跑只做"重新挂接"，不再花一次上游流量。
		if err := s.relinkInspirationCover(record, existing.ID); err != nil {
			item.State = "failed"
			item.Error = err.Error()
			return item
		}
		item.State = "reused"
		item.Bytes = existing.Size
		return item
	}

	body, mimeType, err := downloadInspirationCover(ctx, client, source)
	if err != nil {
		item.State = "failed"
		item.Error = err.Error()
		return item
	}
	extension := inspirationCoverExtension(mimeType)
	if extension == "" {
		item.State = "failed"
		item.Error = fmt.Sprintf("不支持的封面格式 %s", mimeType)
		return item
	}
	objectKey := inspirationCoverObjectKey(item.ResourceID, extension)
	if err := store.Write(objectKey, bytes.NewReader(body)); err != nil {
		item.State = "failed"
		item.Error = err.Error()
		return item
	}
	now := time.Now()
	resource := &model.Resource{
		ID:       item.ResourceID,
		UserID:   InspirationCoverOwnerID,
		Kind:     "image",
		Status:   model.ResourceStatusReady,
		Provider: "local",
		// 对象键是确定性的（见 inspirationCoverObjectKey），覆盖写不会留下孤儿文件。
		ObjectKey: objectKey,
		MimeType:  mimeType,
		Size:      int64(len(body)),
	}
	if existing != nil {
		resource.CreatedAt = existing.CreatedAt
	} else {
		resource.CreatedAt = now
	}
	resource.UpdatedAt = now
	if err := s.repo.SaveResource(resource); err != nil {
		item.State = "failed"
		item.Error = err.Error()
		return item
	}
	if err := s.relinkInspirationCover(record, resource.ID); err != nil {
		item.State = "failed"
		item.Error = err.Error()
		return item
	}
	item.State = "harvested"
	item.Bytes = resource.Size
	return item
}

// relinkInspirationCover 让条目引用本地资源。
//
// 已有的 CoverURL 保留不动：它是签名不可用时的兜底（比如没配 CANVAS_PUBLIC_BASE_URL），
// 留着它最坏也只是回到热链，而不是让卡片变成没有封面的方块。前台只有在签名成功时
// 才会用资源地址，此时外链根本不会出现在响应里。
func (s *Service) relinkInspirationCover(record *model.CreationInspiration, resourceID string) error {
	if record.ResourceID == resourceID {
		return nil
	}
	record.ResourceID = resourceID
	record.UpdatedAt = time.Now()
	return s.repo.SaveCreationInspiration(record)
}

// inspirationCoverResourceID 由源地址派生资源 ID。
//
// 确定性派生而不是随机 ID：重跑一次抓取必须命中同一行，否则每跑一次就给资源表
// 添一批新记录、给磁盘添一批新文件。
func inspirationCoverResourceID(sourceURL string) string {
	sum := sha256.Sum256([]byte("inspiration-cover:" + strings.TrimSpace(sourceURL)))
	return hex.EncodeToString(sum[:16])
}

// inspirationCoverObjectKey 给出确定的本地对象键。
//
// 这里不套用上传用的 users/<uid>/image/YYYY/MM/DD/<hash> 分片：那个形状是为了
// 让每天几十万次上传摊到不同目录，广场封面总共几十张、且由源地址唯一决定，
// 用固定路径反而让"重跑覆盖"这件事变得显然。
func inspirationCoverObjectKey(resourceID string, extension string) string {
	return "users/" + InspirationCoverOwnerID + "/image/inspiration-covers/" + resourceID + extension
}

func isHarvestableCoverURL(raw string) bool {
	return strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://")
}

func localResourceFileExists(store *asset.FileStore, objectKey string) bool {
	if store == nil || strings.TrimSpace(objectKey) == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(store.Root(), filepath.FromSlash(objectKey)))
	return err == nil
}

// downloadInspirationCover 取回封面字节并判定真实类型。
func downloadInspirationCover(ctx context.Context, client *http.Client, source string) ([]byte, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("上游返回 %d", response.StatusCode)
	}
	// 多读一个字节用于判断是否越界，比先看 Content-Length 可靠：上游可能不给长度。
	body, err := io.ReadAll(io.LimitReader(response.Body, inspirationCoverMaxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) == 0 {
		return nil, "", errors.New("上游返回空响应")
	}
	if int64(len(body)) > inspirationCoverMaxBytes {
		return nil, "", fmt.Errorf("封面超过 %d 字节上限", inspirationCoverMaxBytes)
	}
	// 不信上游声明的 Content-Type：防盗链提示页、失败页也常常带着 image/*。
	// 落进资源库的文件必须自己嗅一遍，否则存下来的是一张"图片扩展名的 HTML"。
	mimeType := http.DetectContentType(body)
	if !strings.HasPrefix(mimeType, "image/") {
		return nil, "", fmt.Errorf("上游返回的不是图片（%s）", mimeType)
	}
	return body, mimeType, nil
}

// inspirationCoverExtension 只接受浏览器能直接渲染的几种格式；
// 认不出来就失败，避免往广场塞一个谁也不知道怎么显示的二进制。
func inspirationCoverExtension(mimeType string) string {
	switch strings.TrimSpace(strings.Split(mimeType, ";")[0]) {
	case "image/webp":
		return ".webp"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	default:
		return ""
	}
}
