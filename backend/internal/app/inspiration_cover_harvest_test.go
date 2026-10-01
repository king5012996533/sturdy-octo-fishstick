package app

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"infinite-canvas/backend/internal/model"
)

// sampleCoverPNG 造一张真图：抓取只认嗅探出来的图片类型，
// 拿一段随便的字节当夹具，测的就成了"能否存下任意文件"。
func sampleCoverPNG(t *testing.T) []byte {
	t.Helper()
	source := image.NewRGBA(image.Rect(0, 0, 2, 2))
	source.Set(0, 0, color.RGBA{R: 200, G: 30, B: 90, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, source); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func seedPlatformInspiration(t *testing.T, svc *Service, id string, coverURL string) *model.CreationInspiration {
	t.Helper()
	record := &model.CreationInspiration{
		ID:           id,
		Title:        "示例灵感",
		CoverURL:     coverURL,
		Prompt:       "一段提示词",
		Mode:         "video",
		Status:       model.CreationInspirationOnline,
		Origin:       model.CreationInspirationOriginPlatform,
		ReviewStatus: model.CreationInspirationReviewApproved,
	}
	if err := svc.repo.SaveCreationInspiration(record); err != nil {
		t.Fatal(err)
	}
	return record
}

// 广场封面此前直接热链上游图床：那边一加防盗链，前台就是一片死图。
// 抓取必须真的把字节落到本地资源目录，而不是只在库里挂一个资源 ID。
func TestHarvestInspirationCoversStoresLocalCopy(t *testing.T) {
	t.Setenv("CANVAS_PUBLIC_BASE_URL", "https://example.com")
	svc := newInspirationTestService(t)
	payload := sampleCoverPNG(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	record := seedPlatformInspiration(t, svc, "INSP_COVER_1", server.URL+"/cover.png")
	result, err := svc.HarvestInspirationCovers(context.Background(), InspirationCoverHarvestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 1 || result.Harvested != 1 || result.Failed != 0 {
		t.Fatalf("预期 1 条被抓取，实际 %+v", result)
	}

	resource, err := svc.repo.Resource(result.Items[0].ResourceID)
	if err != nil {
		t.Fatalf("封面资源未落库: %v", err)
	}
	if resource.Provider != "local" || resource.Status != model.ResourceStatusReady || resource.Kind != "image" {
		t.Fatalf("资源属性不符合本地图片约定: %+v", resource)
	}
	if resource.UserID != InspirationCoverOwnerID {
		t.Fatalf("封面应归属平台虚拟用户，实际 %q", resource.UserID)
	}
	if resource.MimeType != "image/png" {
		t.Fatalf("MIME 应按内容判定，实际 %q", resource.MimeType)
	}
	onDisk, err := os.ReadFile(filepath.Join(svc.dataDir, "resources", filepath.FromSlash(resource.ObjectKey)))
	if err != nil {
		t.Fatalf("封面字节未落到本地: %v", err)
	}
	if !bytes.Equal(onDisk, payload) {
		t.Fatal("本地文件与上游内容不一致")
	}

	updated, err := svc.repo.CreationInspirationByID(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ResourceID != resource.ID {
		t.Fatalf("条目未改挂本地资源，实际 %q", updated.ResourceID)
	}
	if updated.CoverURL != server.URL+"/cover.png" {
		t.Fatalf("外链应保留为兜底，实际 %q", updated.CoverURL)
	}
	signed := svc.creationInspirationCoverURL(updated)
	parsed, err := url.Parse(signed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(parsed.Path, "/api/public/resources/"+resource.ID+"/file/") {
		t.Fatalf("对外地址应走平台签名出口，实际 %s", parsed.Path)
	}
	if parsed.Query().Get("signature") == "" || parsed.Query().Get("expires") == "" {
		t.Fatalf("签名地址缺少参数: %s", signed)
	}
}

// 重跑必须只做重新挂接：资源 ID 由源地址派生，重复抓取不该既加流量又加孤儿文件。
func TestHarvestInspirationCoversIsIdempotent(t *testing.T) {
	svc := newInspirationTestService(t)
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(sampleCoverPNG(t))
	}))
	defer server.Close()
	seedPlatformInspiration(t, svc, "INSP_COVER_2", server.URL+"/cover.png")

	first, err := svc.HarvestInspirationCovers(context.Background(), InspirationCoverHarvestOptions{})
	if err != nil || first.Harvested != 1 {
		t.Fatalf("首次抓取失败: %v %+v", err, first)
	}
	second, err := svc.HarvestInspirationCovers(context.Background(), InspirationCoverHarvestOptions{})
	if err != nil || second.Reused != 1 || second.Harvested != 0 {
		t.Fatalf("二次抓取应复用已有产物: %v %+v", err, second)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("二次抓取不应再访问上游，实际请求 %d 次", got)
	}
	if first.Items[0].ResourceID != second.Items[0].ResourceID {
		t.Fatal("资源 ID 必须由源地址确定性派生")
	}
}

// 防盗链提示页、失败页常常带着 200 与 image/* 返回；抓取必须按内容嗅探拒收，
// 否则广场上会出现一批"图片扩展名的 HTML"。
func TestHarvestInspirationCoversRejectsNonImageBody(t *testing.T) {
	svc := newInspirationTestService(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("<html><body>referer is not allowed</body></html>"))
	}))
	defer server.Close()
	record := seedPlatformInspiration(t, svc, "INSP_COVER_3", server.URL+"/cover.png")

	result, err := svc.HarvestInspirationCovers(context.Background(), InspirationCoverHarvestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 1 || result.Harvested != 0 {
		t.Fatalf("非图片响应应判失败，实际 %+v", result)
	}
	if result.Items[0].Error == "" {
		t.Fatal("失败明细必须带原因，否则命令输出无法定位问题")
	}
	updated, err := svc.repo.CreationInspirationByID(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ResourceID != "" {
		t.Fatalf("失败时不应改挂资源，实际 %q", updated.ResourceID)
	}
}

// 没配 CANVAS_PUBLIC_BASE_URL 时签名签不出来，此时要退回库里存的地址，
// 而不是让卡片变成没有封面的方块。
func TestCreationInspirationCoverURLFallsBackWhenSigningUnavailable(t *testing.T) {
	t.Setenv("CANVAS_PUBLIC_BASE_URL", "")
	svc := newInspirationTestService(t)
	record := seedPlatformInspiration(t, svc, "INSP_COVER_4", "https://cdn.example.com/cover.webp")
	record.ResourceID = "missing-resource"
	if err := svc.repo.SaveCreationInspiration(record); err != nil {
		t.Fatal(err)
	}
	if got := svc.creationInspirationCoverURL(record); got != "https://cdn.example.com/cover.webp" {
		t.Fatalf("签名不可用时应回落外链，实际 %q", got)
	}
	if got := svc.creationInspirationCoverURL(&model.CreationInspiration{CoverURL: "https://cdn.example.com/a.webp"}); got != "https://cdn.example.com/a.webp" {
		t.Fatalf("没有资源的条目应原样返回地址，实际 %q", got)
	}
}

// 运营改了封面地址就是要覆盖：本地资源引用必须一起失效，
// 否则签名地址会一直压过刚填的 URL，封面看起来"改不动"。
func TestSaveCreationInspirationClearsHarvestedCover(t *testing.T) {
	svc := newInspirationTestService(t)
	record := seedPlatformInspiration(t, svc, "INSP_COVER_5", "https://cdn.example.com/old.webp")
	record.ResourceID = "harvested-resource"
	if err := svc.repo.SaveCreationInspiration(record); err != nil {
		t.Fatal(err)
	}

	saved, err := svc.SaveCreationInspiration(CreationInspirationInput{
		ID:       record.ID,
		Title:    record.Title,
		Prompt:   record.Prompt,
		Mode:     "video",
		CoverURL: "https://cdn.example.com/new.webp",
		Status:   "ONLINE",
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := svc.repo.CreationInspirationByID(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ResourceID != "" {
		t.Fatalf("改封面后旧资源引用应失效，实际 %q", updated.ResourceID)
	}
	if saved.CoverURL != "https://cdn.example.com/new.webp" {
		t.Fatalf("对外地址应为新填的 URL，实际 %q", saved.CoverURL)
	}
}
