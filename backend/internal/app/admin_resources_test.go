package app

import (
	"strings"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 产物 ID 用 32 位定长串：引用判定是 payload_json 里的子串匹配，测试数据里若有一个 ID
// 是另一个的前缀，就会像真实误判一样把引用关系算错。
const (
	adminResourceA = "res0000000000000000000000000001"
	adminResourceB = "res0000000000000000000000000002"
	adminResourceC = "res0000000000000000000000000003"
	adminResourceD = "res0000000000000000000000000004"
)

func newAdminResourceTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	// 预览地址要现场签名，签名依赖公网基址；不配的话这里签不出地址，就测不到预览这一层。
	t.Setenv("CANVAS_PUBLIC_BASE_URL", "https://example.com")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Resource{}, &model.Asset{}, &model.CanvasProject{}, &model.Workspace{}); err != nil {
		t.Fatal(err)
	}
	return &Service{repo: repository.New(db), dataDir: t.TempDir()}, db
}

func seedAdminResourceFixture(t *testing.T, db *gorm.DB) time.Time {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	if err := db.Create(&[]model.Workspace{
		{ID: "user-1", Name: "甲账号", CreatedAt: now, UpdatedAt: now},
		{ID: "user-2", Name: "乙账号", CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]model.Resource{
		// A、B 分别被素材库与画布引用；C 是「上游产出、用户没拿到」；D 还没 ready，不能签预览。
		{ID: adminResourceA, UserID: "user-1", Kind: "image", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "users/user-1/image/one.png", MimeType: "image/png", Size: 1000, Width: 512, Height: 512, CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: now},
		{ID: adminResourceB, UserID: "user-1", Kind: "video", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "users/user-1/video/two.mp4", MimeType: "video/mp4", Size: 2000, DurationMs: 5000, CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now},
		{ID: adminResourceC, UserID: "user-2", Kind: "image", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "users/user-2/image/three.png", MimeType: "image/png", Size: 3000, CreatedAt: now.Add(-time.Hour), UpdatedAt: now},
		{ID: adminResourceD, UserID: "user-2", Kind: "image", Status: model.ResourceStatusPending, Provider: "beefapi", ObjectKey: "remote/four", Size: 500, CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Asset{
		ID: "asset-1", UserID: "user-1", Kind: "image", Category: model.AssetCategoryMaterial,
		Status: model.AssetVersionStatusConfirmed, Title: "海边素材",
		PayloadJSON: `{"resourceId":"` + adminResourceA + `"}`, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CanvasProject{
		ID: "canvas-1", UserID: "user-1", Title: "画布",
		PayloadJSON: `{"nodes":[{"resourceId":"` + adminResourceB + `"}]}`, Revision: 1,
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return now
}

func TestAdminResourcePageMarksUnreferencedAndSignsPreview(t *testing.T) {
	service, db := newAdminResourceTestService(t)
	now := seedAdminResourceFixture(t, db)

	page, err := service.AdminResourcePage(repository.AdminResourceFilter{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 4 || len(page.Resources) != 4 {
		t.Fatalf("产物列表 = total %d rows %d; want 4 / 4", page.Total, len(page.Resources))
	}
	if page.Totals.Total != 4 || page.Totals.Unreferenced != 2 || page.Totals.Users != 2 || page.Totals.TotalBytes != 6500 {
		t.Fatalf("顶部读数异常：%#v", page.Totals)
	}
	byID := map[string]AdminResourceView{}
	for _, row := range page.Resources {
		byID[row.ID] = row
	}
	if !byID[adminResourceA].Referenced || !byID[adminResourceB].Referenced {
		t.Fatalf("素材库与画布里的产物应判定为已引用：%#v", byID)
	}
	if byID[adminResourceC].Referenced || byID[adminResourceD].Referenced {
		t.Fatalf("没人引用的产物不应判定为已引用：%#v", byID)
	}
	if byID[adminResourceA].UserName != "甲账号" {
		t.Fatalf("owner 昵称 = %q; want 甲账号", byID[adminResourceA].UserName)
	}
	if !strings.Contains(byID[adminResourceA].PreviewURL, "/api/public/resources/"+adminResourceA+"/file/") {
		t.Fatalf("ready 的本地产物应带签名预览地址：%q", byID[adminResourceA].PreviewURL)
	}
	if byID[adminResourceD].PreviewURL != "" {
		t.Fatalf("未 ready 的产物不该有预览地址：%q", byID[adminResourceD].PreviewURL)
	}

	// 只看用户没拿到：这正是对账要捞的那批。
	page, err = service.AdminResourcePage(repository.AdminResourceFilter{UnreferencedOnly: true, Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 {
		t.Fatalf("只看未引用 = %d; want 2", page.Total)
	}
	for _, row := range page.Resources {
		if row.Referenced {
			t.Fatalf("未引用筛选混入了已引用产物：%#v", row)
		}
	}
	// 顶部读数不随筛选变化，否则看不出整体规模。
	if page.Totals.Total != 4 || page.Totals.Unreferenced != 2 {
		t.Fatalf("筛选后顶部读数不该跟着变：%#v", page.Totals)
	}

	// 时间范围与类型过滤。
	page, err = service.AdminResourcePage(repository.AdminResourceFilter{Kind: "image", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 {
		t.Fatalf("按类型筛选 = %d; want 3", page.Total)
	}
	page, err = service.AdminResourcePage(repository.AdminResourceFilter{
		Since: now.Add(-90 * time.Minute), Until: now.Add(time.Minute), Page: 1, PageSize: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 {
		t.Fatalf("按时间筛选 = %d; want 2", page.Total)
	}
	// 关键字同时命中账号昵称与产物 ID。
	page, err = service.AdminResourcePage(repository.AdminResourceFilter{Keyword: "甲账号", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 {
		t.Fatalf("按账号搜索 = %d; want 2", page.Total)
	}
	page, err = service.AdminResourcePage(repository.AdminResourceFilter{Keyword: adminResourceC, Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Resources[0].ID != adminResourceC {
		t.Fatalf("按产物 ID 搜索 = %#v; want 仅 C", page.Resources)
	}
}

func TestAdminResourcePageKeepsPageOrderAndClampsSize(t *testing.T) {
	service, db := newAdminResourceTestService(t)
	seedAdminResourceFixture(t, db)

	// 分页顺序按创建时间倒序，且第二页不与第一页重复。
	first, err := service.AdminResourcePage(repository.AdminResourceFilter{Page: 1, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Resources) != 2 || first.Resources[0].ID != adminResourceD || first.Resources[1].ID != adminResourceC {
		t.Fatalf("第一页顺序异常：%#v", first.Resources)
	}
	second, err := service.AdminResourcePage(repository.AdminResourceFilter{Page: 2, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Resources) != 2 || second.Resources[0].ID != adminResourceB || second.Resources[1].ID != adminResourceA {
		t.Fatalf("第二页顺序异常：%#v", second.Resources)
	}

	// 页码与每页条数都要兜底，避免把 -1 页算成负偏移。
	clamped, err := service.AdminResourcePage(repository.AdminResourceFilter{Page: -3, PageSize: 9999})
	if err != nil {
		t.Fatal(err)
	}
	if clamped.Page != 1 || clamped.PageSize != adminResourceMaxPageSize {
		t.Fatalf("分页兜底 = page %d size %d; want 1 / %d", clamped.Page, clamped.PageSize, adminResourceMaxPageSize)
	}
}
