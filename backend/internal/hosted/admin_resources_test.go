package hosted

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"infinite-canvas/backend/internal/bootstrap"
	"infinite-canvas/backend/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// newResourceAdminRouter 复用标准托管路由（含管理员守卫）。
//
// 产物路由由 registerAdminRoutes 在生产装配里挂载，这里不再重复注册：同一方法+路径
// 注册两次 gin 会直接 panic。
func newResourceAdminRouter(t *testing.T) (bootstrap.HostedExtension, *gin.Engine, *gorm.DB, *gorm.DB) {
	t.Helper()
	extension, authDB, canvasDB, service := newTestExtension(t)
	if err := canvasDB.AutoMigrate(&model.Resource{}); err != nil {
		t.Fatalf("初始化产物表失败: %v", err)
	}
	return extension, newTestRouter(extension, service), authDB, canvasDB
}

type adminResourceListBody struct {
	Data struct {
		Resources []struct {
			ID         string `json:"id"`
			Kind       string `json:"kind"`
			UserName   string `json:"userName"`
			Referenced bool   `json:"referenced"`
			PreviewURL string `json:"previewUrl"`
		} `json:"resources"`
		Total    int64 `json:"total"`
		Page     int   `json:"page"`
		PageSize int   `json:"pageSize"`
		Totals   struct {
			Total        int64 `json:"total"`
			Unreferenced int64 `json:"unreferenced"`
			TotalBytes   int64 `json:"totalBytes"`
			Users        int64 `json:"users"`
		} `json:"totals"`
	} `json:"data"`
}

func TestHostedAdminResourceListRequiresAdminAndReportsUnreferenced(t *testing.T) {
	// 预览地址要现场签名，没有公网基址就签不出来；这一条用例要同时验证预览这一层。
	t.Setenv("CANVAS_PUBLIC_BASE_URL", "https://example.com")
	extension, router, authDB, canvasDB := newResourceAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "admin-resources@example.com")
	promoteToAdmin(t, authDB, adminID)
	_, ownerID := registerAccount(t, router, authDB, "resource-owner@example.com")

	now := time.Now().UTC()
	if err := canvasDB.Create(&model.Workspace{ID: ownerID, Name: "产物作者", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("写入工作区失败: %v", err)
	}
	referencedID := "resaaa0000000000000000000000001"
	strandedID := "resbbb0000000000000000000000002"
	if err := canvasDB.Create(&[]model.Resource{
		{ID: referencedID, UserID: ownerID, Kind: "image", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "users/x/image/one.png", MimeType: "image/png", Size: 2048, CreatedAt: now, UpdatedAt: now},
		// 没人引用：上游可能已产出，但用户端没拿到，正是对账要捞的那条。
		{ID: strandedID, UserID: ownerID, Kind: "video", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "users/x/video/two.mp4", MimeType: "video/mp4", Size: 4096, CreatedAt: now.Add(time.Second), UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("写入产物失败: %v", err)
	}
	if err := canvasDB.Create(&model.Asset{
		ID: "asset-ref", UserID: ownerID, Kind: "image", Category: model.AssetCategoryMaterial,
		Status: model.AssetVersionStatusConfirmed, Title: "引用产物",
		PayloadJSON: `{"resourceId":"` + referencedID + `"}`, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("写入素材失败: %v", err)
	}

	if recorder := perform(router, http.MethodGet, "/api/admin/resources", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("匿名访问产物列表 = %d; want 401", recorder.Code)
	}

	recorder := perform(router, http.MethodGet, "/api/admin/resources", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("管理员访问产物列表 = %d; want 200，响应 %s", recorder.Code, recorder.Body.String())
	}
	var body adminResourceListBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body.Data.Total != 2 || len(body.Data.Resources) != 2 {
		t.Fatalf("产物列表 = total %d rows %d; want 2 / 2", body.Data.Total, len(body.Data.Resources))
	}
	if body.Data.Totals.Total != 2 || body.Data.Totals.Unreferenced != 1 || body.Data.Totals.TotalBytes != 6144 || body.Data.Totals.Users != 1 {
		t.Fatalf("顶部读数异常：%#v", body.Data.Totals)
	}
	byID := map[string]bool{}
	previews := map[string]string{}
	for _, row := range body.Data.Resources {
		byID[row.ID] = row.Referenced
		previews[row.ID] = row.PreviewURL
	}
	if !byID[referencedID] || byID[strandedID] {
		t.Fatalf("引用判定异常：%#v", byID)
	}
	if previews[referencedID] == "" || previews[strandedID] == "" {
		t.Fatalf("ready 的本地产物都应带签名预览地址：%#v", previews)
	}

	// 「只看用户没拿到」必须能筛出那一条。
	recorder = perform(router, http.MethodGet, "/api/admin/resources?unreferenced=true", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("未引用筛选 = %d; want 200", recorder.Code)
	}
	var filtered adminResourceListBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &filtered); err != nil {
		t.Fatalf("解析筛选响应失败: %v", err)
	}
	if filtered.Data.Total != 1 || len(filtered.Data.Resources) != 1 || filtered.Data.Resources[0].ID != strandedID {
		t.Fatalf("未引用筛选结果异常：%#v", filtered.Data.Resources)
	}
	// 顶部读数不随筛选变化，否则看不出整体规模。
	if filtered.Data.Totals.Total != 2 || filtered.Data.Totals.Unreferenced != 1 {
		t.Fatalf("筛选后顶部读数不该跟着变：%#v", filtered.Data.Totals)
	}

	// 非法时间格式要当场拒绝，而不是悄悄按无筛选返回一堆数据。
	if recorder := perform(router, http.MethodGet, "/api/admin/resources?since=昨天", "", adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("非法 since = %d; want 400", recorder.Code)
	}
}
