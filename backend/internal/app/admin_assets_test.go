package app

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newAdminAssetTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "canvas.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	// 素材与审核状态同库；workspaces 提供 owner 昵称（画布库里就是这张表）。
	if err := db.AutoMigrate(&model.Asset{}, &model.AssetVersion{}, &model.AssetModeration{}, &model.Workspace{}, &model.Resource{}); err != nil {
		t.Fatal(err)
	}
	return New(repository.New(db), t.TempDir()), db
}

func seedAdminAssetFixture(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now().UTC()
	if err := db.Create(&[]model.Workspace{
		{ID: "user-1", Name: "甲账号", CreatedAt: now, UpdatedAt: now},
		{ID: "user-2", Name: "乙账号", CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]model.Asset{
		{ID: "asset-1", UserID: "user-1", Kind: "image", Category: model.AssetCategoryMaterial, Status: model.AssetVersionStatusConfirmed, Title: "海边素材", PayloadJSON: `{"id":"asset-1"}`, CreatedAt: now, UpdatedAt: now.Add(time.Second)},
		{ID: "asset-2", UserID: "user-1", Kind: "text", Category: model.AssetCategoryOther, Status: model.AssetVersionStatusConfirmed, Title: "提示词素材", PayloadJSON: `{"id":"asset-2"}`, CreatedAt: now, UpdatedAt: now},
		{ID: "asset-3", UserID: "user-2", Kind: "image", Category: model.AssetCategoryMaterial, Status: model.AssetVersionStatusConfirmed, Title: "他人海边素材", PayloadJSON: `{"id":"asset-3"}`, CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.AssetVersion{ID: "version-1", AssetID: "asset-1", Version: 1, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: `{"definition":true}`, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestAdminAssetPageFiltersAndPaginates(t *testing.T) {
	service, db := newAdminAssetTestService(t)
	seedAdminAssetFixture(t, db)

	// 关键字命中标题。
	page, err := service.AdminAssetPage(repository.AdminAssetFilter{Keyword: "海边", Page: 1, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Assets) != 1 || page.Page != 1 || page.PageSize != 1 {
		t.Fatalf("关键字分页 = total %d rows %d page %d size %d; want 2 / 1 / 1 / 1", page.Total, len(page.Assets), page.Page, page.PageSize)
	}
	if page.Assets[0].UserName == "" || page.Assets[0].PayloadBytes <= 0 {
		t.Fatalf("列表行缺少账号或占用近似值：%#v", page.Assets[0])
	}
	if page.Totals.Total != 3 || page.Totals.Users != 2 || page.Totals.TotalBytes <= 0 {
		t.Fatalf("顶部读数异常：%#v", page.Totals)
	}

	// 关键字命中账号（画布库 workspaces.name）。
	page, err = service.AdminAssetPage(repository.AdminAssetFilter{Keyword: "甲账号", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 {
		t.Fatalf("按账号搜索 = %d; want 2", page.Total)
	}

	// 类型过滤。
	page, err = service.AdminAssetPage(repository.AdminAssetFilter{Kind: "image", Page: 1, PageSize: 20})
	if err != nil || page.Total != 2 {
		t.Fatalf("按类型过滤 = %d err %v; want 2", page.Total, err)
	}

	// 所有者过滤。
	page, err = service.AdminAssetPage(repository.AdminAssetFilter{UserID: "user-1", Page: 1, PageSize: 20})
	if err != nil || page.Total != 2 {
		t.Fatalf("按账号过滤 = %d err %v; want 2", page.Total, err)
	}

	// 空结果必须是空数组而不是 null。
	page, err = service.AdminAssetPage(repository.AdminAssetFilter{Keyword: "不存在的素材", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || page.Assets == nil || len(page.Assets) != 0 {
		t.Fatalf("空结果应为空数组：total %d assets %#v", page.Total, page.Assets)
	}

	// 非法状态筛选直接拒绝，而不是静默返回全量。
	if _, err := service.AdminAssetPage(repository.AdminAssetFilter{Status: "BOGUS"}); err == nil {
		t.Fatal("非法状态筛选应报错")
	} else {
		var appErr *AppError
		if !errors.As(err, &appErr) || appErr.Status != 400 {
			t.Fatalf("非法状态筛选错误 = %v; want 400", err)
		}
	}
}

func TestAdminAssetDetailReturnsNotFound(t *testing.T) {
	service, db := newAdminAssetTestService(t)
	seedAdminAssetFixture(t, db)

	if _, err := service.AdminAssetDetail("missing"); err == nil {
		t.Fatal("缺失素材应报 404")
	} else {
		var appErr *AppError
		if !errors.As(err, &appErr) || appErr.Status != 404 {
			t.Fatalf("缺失素材错误 = %v; want 404", err)
		}
	}

	view, err := service.AdminAssetDetail("asset-1")
	if err != nil {
		t.Fatal(err)
	}
	if view.Title != "海边素材" || view.VersionCount != 1 || view.ModerationStatus != string(model.AssetModerationNormal) {
		t.Fatalf("详情 = %#v", view)
	}
}

func TestAdminAssetModerationFlowRequiresReasonAndValidStatus(t *testing.T) {
	service, db := newAdminAssetTestService(t)
	seedAdminAssetFixture(t, db)

	expectStatus := func(t *testing.T, err error, status int) {
		t.Helper()
		var appErr *AppError
		if !errors.As(err, &appErr) || appErr.Status != status {
			t.Fatalf("错误 = %v; want %d", err, status)
		}
	}

	// 隐藏/删除必须带理由。
	if _, err := service.ModerateAsset("asset-1", "HIDDEN", "   ", "admin-1"); err == nil {
		t.Fatal("空理由隐藏应报 400")
	} else {
		expectStatus(t, err, 400)
	}
	// 非法状态。
	if _, err := service.ModerateAsset("asset-1", "DELETED", "x", "admin-1"); err == nil {
		t.Fatal("非法状态应报 400")
	} else {
		expectStatus(t, err, 400)
	}
	// 素材不存在。
	if _, err := service.ModerateAsset("missing", "HIDDEN", "含违规内容", "admin-1"); err == nil {
		t.Fatal("缺失素材应报 404")
	} else {
		expectStatus(t, err, 404)
	}

	hidden, err := service.ModerateAsset("asset-1", "HIDDEN", "含违规内容", "admin-1")
	if err != nil {
		t.Fatal(err)
	}
	if hidden.ModerationStatus != string(model.AssetModerationHidden) || hidden.ModerationReason != "含违规内容" {
		t.Fatalf("隐藏结果 = %#v", hidden)
	}

	// 详情与列表都要能读到处置结论。
	detail, err := service.AdminAssetDetail("asset-1")
	if err != nil || detail.ModerationStatus != string(model.AssetModerationHidden) || detail.ModerationReason != "含违规内容" {
		t.Fatalf("处置后详情 = %#v err %v", detail, err)
	}
	page, err := service.AdminAssetPage(repository.AdminAssetFilter{Status: "HIDDEN", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Assets) != 1 || page.Assets[0].ID != "asset-1" || page.Assets[0].ModerationReason != "含违规内容" {
		t.Fatalf("按已隐藏筛选 = %#v", page)
	}
	if page.Totals.Hidden != 1 {
		t.Fatalf("顶部隐藏计数 = %d; want 1", page.Totals.Hidden)
	}
	normal, err := service.AdminAssetPage(repository.AdminAssetFilter{Status: string(model.AssetModerationNormal), Page: 1, PageSize: 20})
	if err != nil || normal.Total != 2 {
		t.Fatalf("正常素材数 = %d err %v; want 2", normal.Total, err)
	}

	// 记录首次处置时间，确认后续处置不会重置审计起点。
	var first model.AssetModeration
	if err := db.Where("asset_id = ?", "asset-1").First(&first).Error; err != nil {
		t.Fatal(err)
	}

	removed, err := service.ModerateAsset("asset-1", "REMOVED", "确认违规", "admin-1")
	if err != nil {
		t.Fatal(err)
	}
	if removed.ModerationStatus != string(model.AssetModerationRemoved) || removed.ModerationReason != "确认违规" {
		t.Fatalf("删除结果 = %#v", removed)
	}

	restored, err := service.ModerateAsset("asset-1", "NORMAL", "", "admin-1")
	if err != nil {
		t.Fatal(err)
	}
	if restored.ModerationStatus != string(model.AssetModerationNormal) || restored.ModerationReason != "" {
		t.Fatalf("恢复结果 = %#v", restored)
	}
	var last model.AssetModeration
	if err := db.Where("asset_id = ?", "asset-1").First(&last).Error; err != nil {
		t.Fatal(err)
	}
	if !last.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("恢复不应重置创建时间：%v -> %v", first.CreatedAt, last.CreatedAt)
	}
	if last.ActorID != "admin-1" {
		t.Fatalf("处置人 = %q; want admin-1", last.ActorID)
	}
}
