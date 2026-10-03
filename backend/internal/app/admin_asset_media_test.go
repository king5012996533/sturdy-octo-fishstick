package app

import (
	"strings"
	"testing"
	"time"

	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/gorm"
)

// 素材本体的资源 ID：32 位十六进制，和线上真实数据同形，便于验证 URL 拼接。
const (
	assetPreviewResourceID = "69d04d29eb059e0746fe85119514803b"
	assetPreviewBaseURL    = "https://example.com"
)

func seedAdminAssetResource(t *testing.T, db *gorm.DB, resource model.Resource) {
	t.Helper()
	if err := db.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}
}

func readyLocalResource(id string, userID string) model.Resource {
	now := time.Now().UTC()
	return model.Resource{
		ID: id, UserID: userID, Kind: "image", Status: model.ResourceStatusReady,
		Provider: "local", ObjectKey: "resources/ab/cd/" + id, MimeType: "image/png",
		CreatedAt: now, UpdatedAt: now,
	}
}

func setAdminAssetPayload(t *testing.T, db *gorm.DB, assetID string, payload string) {
	t.Helper()
	if err := db.Model(&model.Asset{}).Where("id = ?", assetID).Update("payload_json", payload).Error; err != nil {
		t.Fatal(err)
	}
}

// 后台看素材必须能直接看到本体：列表与详情都要带上现场签发的预览地址。
func TestAdminAssetPreviewSignsReferencedResource(t *testing.T) {
	t.Setenv("CANVAS_PUBLIC_BASE_URL", assetPreviewBaseURL)
	service, db := newAdminAssetTestService(t)
	seedAdminAssetFixture(t, db)
	seedAdminAssetResource(t, db, readyLocalResource(assetPreviewResourceID, "user-1"))
	// 线上图片素材的真实形态：coverUrl 与 data.dataUrl 指向同一条资源，blob 地址不该被当引用。
	setAdminAssetPayload(t, db, "asset-1", `{"id":"asset-1","coverUrl":"`+assets.FileURL(assetPreviewResourceID)+`","data":{"dataUrl":"`+assets.FileURL(assetPreviewResourceID)+`","storageKey":"resource:`+assetPreviewResourceID+`"}}`)

	detail, err := service.AdminAssetDetail("asset-1")
	if err != nil {
		t.Fatal(err)
	}
	if detail.ResourceID != assetPreviewResourceID || detail.MediaKind != "image" || detail.MimeType != "image/png" {
		t.Fatalf("详情本体字段 = %#v", detail)
	}
	if !strings.Contains(detail.PreviewURL, "/api/public/resources/"+assetPreviewResourceID+"/file") {
		t.Fatalf("详情预览地址 = %q", detail.PreviewURL)
	}
	if !strings.Contains(detail.PreviewURL, "signature=") {
		t.Fatalf("预览地址必须带签名 = %q", detail.PreviewURL)
	}

	page, err := service.AdminAssetPage(repository.AdminAssetFilter{Keyword: "海边", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	// 列表也要带预览：只看详情不够，运营是先在列表里扫一遍再点进去的。
	var listed *AdminAssetRowView
	for index := range page.Assets {
		if page.Assets[index].ID == "asset-1" {
			listed = &page.Assets[index]
		}
	}
	if listed == nil || listed.ResourceID != assetPreviewResourceID || listed.PreviewURL == "" {
		t.Fatalf("列表预览 = %#v", page.Assets)
	}
}

// 纯文本素材没有本体，引用失效或指向别人资源时也不该硬凑一个地址出来。
func TestAdminAssetPreviewSkipsUnplayableReferences(t *testing.T) {
	t.Setenv("CANVAS_PUBLIC_BASE_URL", assetPreviewBaseURL)
	service, db := newAdminAssetTestService(t)
	seedAdminAssetFixture(t, db)

	notReady := readyLocalResource("11111111111111111111111111111111", "user-1")
	notReady.Status = "uploading"
	seedAdminAssetResource(t, db, notReady)
	foreign := readyLocalResource("22222222222222222222222222222222", "user-2")
	seedAdminAssetResource(t, db, foreign)

	cases := []struct {
		name    string
		assetID string
		payload string
	}{
		{"纯文本没有引用", "asset-2", `{"id":"asset-2","data":{"text":"一段提示词"}}`},
		{"引用已不存在的资源", "asset-1", `{"id":"asset-1","data":{"storageKey":"resource:33333333333333333333333333333333"}}`},
		{"引用还没就绪的资源", "asset-1", `{"id":"asset-1","data":{"storageKey":"resource:11111111111111111111111111111111"}}`},
		{"引用别的账号的资源", "asset-1", `{"id":"asset-1","data":{"storageKey":"resource:22222222222222222222222222222222"}}`},
		{"只有浏览器本地 blob 地址", "asset-1", `{"id":"asset-1","data":{"url":"blob:https://kinotv.example.com/9f0d"}}`},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			setAdminAssetPayload(t, db, item.assetID, item.payload)
			detail, err := service.AdminAssetDetail(item.assetID)
			if err != nil {
				t.Fatal(err)
			}
			if detail.PreviewURL != "" || detail.ResourceID != "" {
				t.Fatalf("不该给预览：%#v", detail)
			}
		})
	}
}

// 处置的返回值会直接刷新抽屉：隐藏或删除后预览必须还在，否则管理员刚看完的素材会变空白。
func TestAdminAssetModerationKeepsPreview(t *testing.T) {
	t.Setenv("CANVAS_PUBLIC_BASE_URL", assetPreviewBaseURL)
	service, db := newAdminAssetTestService(t)
	seedAdminAssetFixture(t, db)
	seedAdminAssetResource(t, db, readyLocalResource(assetPreviewResourceID, "user-1"))
	setAdminAssetPayload(t, db, "asset-1", `{"id":"asset-1","coverUrl":"`+assets.FileURL(assetPreviewResourceID)+`"}`)

	view, err := service.ModerateAsset("asset-1", "HIDDEN", "含未授权品牌素材", "admin-1")
	if err != nil {
		t.Fatal(err)
	}
	if view.ModerationStatus != string(model.AssetModerationHidden) {
		t.Fatalf("处置结果 = %#v", view)
	}
	if view.PreviewURL == "" || view.ResourceID != assetPreviewResourceID {
		t.Fatalf("处置后预览丢失：%#v", view)
	}
}
