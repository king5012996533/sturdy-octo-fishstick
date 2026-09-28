package hosted

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"infinite-canvas/backend/internal/bootstrap"
	"infinite-canvas/backend/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// newAssetAdminRouter 复用标准托管路由（含管理员守卫）。
//
// 素材路由已由 registerAdminRoutes 在生产装配里挂载，这里不再重复注册：同一方法+路径
// 注册两次 gin 会直接 panic（这曾经真的发生过）。
func newAssetAdminRouter(t *testing.T) (bootstrap.HostedExtension, *gin.Engine, *gorm.DB, *gorm.DB) {
	t.Helper()
	extension, authDB, canvasDB, service := newTestExtension(t)
	// 表已由 MigrateHostedSharedSchema 建好，这里显式再建一次只是为了用例不依赖
	// 生产迁移的执行顺序。
	if err := canvasDB.AutoMigrate(&model.AssetModeration{}); err != nil {
		t.Fatalf("初始化素材审核表失败: %v", err)
	}
	router := newTestRouter(extension, service)
	return extension, router, authDB, canvasDB
}

func seedHostedAsset(t *testing.T, canvasDB *gorm.DB, ownerID string, assetID string, title string, kind string, updatedAt time.Time) {
	t.Helper()
	if err := canvasDB.Create(&model.Asset{
		ID: assetID, UserID: ownerID, Kind: kind, Category: model.AssetCategoryMaterial,
		Status: model.AssetVersionStatusConfirmed, Title: title,
		PayloadJSON: `{"id":"` + assetID + `"}`, CreatedAt: updatedAt, UpdatedAt: updatedAt,
	}).Error; err != nil {
		t.Fatalf("写入素材失败: %v", err)
	}
}

type adminAssetListBody struct {
	Data struct {
		Assets []struct {
			ID               string `json:"id"`
			Title            string `json:"title"`
			Kind             string `json:"kind"`
			UserName         string `json:"userName"`
			VersionCount     int64  `json:"versionCount"`
			PayloadBytes     int64  `json:"payloadBytes"`
			ModerationStatus string `json:"moderationStatus"`
			ModerationReason string `json:"moderationReason"`
		} `json:"assets"`
		Total    int64 `json:"total"`
		Page     int   `json:"page"`
		PageSize int   `json:"pageSize"`
		Totals   struct {
			Total   int64 `json:"total"`
			Hidden  int64 `json:"hidden"`
			Removed int64 `json:"removed"`
			Users   int64 `json:"users"`
		} `json:"totals"`
	} `json:"data"`
}

func TestHostedAdminAssetListAndModeration(t *testing.T) {
	extension, router, authDB, canvasDB := newAssetAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "admin-assets@example.com")
	promoteToAdmin(t, authDB, adminID)
	_, ownerID := registerAccount(t, router, authDB, "asset-owner@example.com")

	now := time.Now().UTC()
	if err := canvasDB.Create(&model.Workspace{ID: ownerID, Name: "素材作者", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("写入工作区失败: %v", err)
	}
	seedHostedAsset(t, canvasDB, ownerID, "asset-a", "海边素材", "image", now.Add(2*time.Second))
	seedHostedAsset(t, canvasDB, ownerID, "asset-b", "提示词素材", "text", now)
	if err := canvasDB.Create(&model.AssetVersion{
		ID: "asset-a-version-1", AssetID: "asset-a", Version: 1,
		Status: model.AssetVersionStatusConfirmed, DefinitionJSON: `{"definition":true}`,
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("写入素材版本失败: %v", err)
	}

	recorder := perform(router, http.MethodGet, "/api/admin/assets", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("素材列表失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var list adminAssetListBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &list); err != nil {
		t.Fatalf("解析素材列表失败: %v %s", err, recorder.Body.String())
	}
	if list.Data.Total != 2 || len(list.Data.Assets) != 2 || list.Data.Page != 1 || list.Data.PageSize != 20 {
		t.Fatalf("素材列表分页异常：%s", recorder.Body.String())
	}
	first := list.Data.Assets[0]
	if first.ID != "asset-a" || first.UserName == "" || first.VersionCount != 1 || first.PayloadBytes <= 0 || first.ModerationStatus != "NORMAL" {
		t.Fatalf("列表首行异常：%#v", first)
	}
	if list.Data.Totals.Total != 2 || list.Data.Totals.Users != 1 {
		t.Fatalf("顶部读数异常：%#v", list.Data.Totals)
	}

	// 类型过滤只保留文本素材。
	recorder = perform(router, http.MethodGet, "/api/admin/assets?kind=text", "", adminCookie)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "提示词素材") || strings.Contains(recorder.Body.String(), "海边素材") {
		t.Fatalf("按类型过滤异常：%d %s", recorder.Code, recorder.Body.String())
	}
	// 关键字命中标题与账号两条路径都要能查到。
	recorder = perform(router, http.MethodGet, "/api/admin/assets?keyword=%E6%B5%B7%E8%BE%B9", "", adminCookie)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "海边素材") {
		t.Fatalf("按标题搜索异常：%d %s", recorder.Code, recorder.Body.String())
	}
	recorder = perform(router, http.MethodGet, "/api/admin/assets?keyword=%E7%B4%A0%E6%9D%90%E4%BD%9C%E8%80%85", "", adminCookie)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"total":2`) {
		t.Fatalf("按账号搜索异常：%d %s", recorder.Code, recorder.Body.String())
	}
	// 空结果必须是数组而不是 null。
	recorder = perform(router, http.MethodGet, "/api/admin/assets?keyword=zzz-nothing", "", adminCookie)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"assets":[]`) {
		t.Fatalf("空结果应为空数组：%d %s", recorder.Code, recorder.Body.String())
	}

	// 详情。
	recorder = perform(router, http.MethodGet, "/api/admin/assets/asset-a", "", adminCookie)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "海边素材") {
		t.Fatalf("素材详情异常：%d %s", recorder.Code, recorder.Body.String())
	}
	if recorder = perform(router, http.MethodGet, "/api/admin/assets/missing", "", adminCookie); recorder.Code != http.StatusNotFound {
		t.Fatalf("缺失素材详情应 404，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 处置校验：空理由、非法状态、缺失素材。
	if recorder := perform(router, http.MethodPost, "/api/admin/assets/asset-a/moderation", `{"status":"HIDDEN"}`, adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("空理由隐藏应 400，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodPost, "/api/admin/assets/asset-a/moderation", `{"status":"DELETED","reason":"x"}`, adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("非法状态应 400，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodPost, "/api/admin/assets/missing/moderation", `{"status":"HIDDEN","reason":"x"}`, adminCookie); recorder.Code != http.StatusNotFound {
		t.Fatalf("缺失素材处置应 404，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 正常 → 隐藏 → 删除 → 恢复。
	recorder = perform(router, http.MethodPost, "/api/admin/assets/asset-a/moderation", `{"status":"HIDDEN","reason":"含违规内容"}`, adminCookie)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"moderationStatus":"HIDDEN"`) || !strings.Contains(recorder.Body.String(), "含违规内容") {
		t.Fatalf("隐藏失败：%d %s", recorder.Code, recorder.Body.String())
	}
	recorder = perform(router, http.MethodGet, "/api/admin/assets?status=HIDDEN", "", adminCookie)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"total":1`) || !strings.Contains(recorder.Body.String(), "含违规内容") {
		t.Fatalf("按已隐藏筛选异常：%d %s", recorder.Code, recorder.Body.String())
	}
	if recorder = perform(router, http.MethodGet, "/api/admin/assets/asset-a", "", adminCookie); recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "含违规内容") {
		t.Fatalf("处置后详情缺少理由：%d %s", recorder.Code, recorder.Body.String())
	}
	recorder = perform(router, http.MethodPost, "/api/admin/assets/asset-a/moderation", `{"status":"REMOVED","reason":"确认违规"}`, adminCookie)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"moderationStatus":"REMOVED"`) {
		t.Fatalf("删除失败：%d %s", recorder.Code, recorder.Body.String())
	}
	recorder = perform(router, http.MethodPost, "/api/admin/assets/asset-a/moderation", `{"status":"NORMAL","reason":"误判，已恢复"}`, adminCookie)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"moderationStatus":"NORMAL"`) {
		t.Fatalf("恢复失败：%d %s", recorder.Code, recorder.Body.String())
	}
	// 恢复后按正常筛选能读到两条。
	recorder = perform(router, http.MethodGet, "/api/admin/assets?status=NORMAL", "", adminCookie)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"total":2`) {
		t.Fatalf("恢复后正常列表异常：%d %s", recorder.Code, recorder.Body.String())
	}

	// 每次处置都要留痕，且元数据里带理由。
	var audits []model.AdminAuditEvent
	if err := canvasDB.Where("action = ?", "asset.moderate").Find(&audits).Error; err != nil {
		t.Fatalf("读取审计失败: %v", err)
	}
	if len(audits) != 3 {
		t.Fatalf("三次处置应留下 3 条审计，实际 %d 条", len(audits))
	}
	withReason := false
	for _, event := range audits {
		if event.TargetID != "asset-a" || event.Summary != "素材处置" || !strings.Contains(event.MetadataJSON, `"status"`) {
			t.Fatalf("审计事件缺少目标或元数据：%#v", event)
		}
		if event.ActorUserID != adminID {
			t.Fatalf("审计主体 = %q; want %q", event.ActorUserID, adminID)
		}
		if strings.Contains(event.MetadataJSON, "含违规内容") {
			withReason = true
		}
	}
	if !withReason {
		t.Fatalf("审计元数据应带理由：%#v", audits)
	}
}

func TestHostedAdminAssetRequiresAdmin(t *testing.T) {
	extension, router, authDB, _ := newAssetAdminRouter(t)
	defer extension.Close()

	if recorder := perform(router, http.MethodGet, "/api/admin/assets", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录访问素材管理应 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	cookie, _ := registerAccount(t, router, authDB, "plain-assets@example.com")
	if recorder := perform(router, http.MethodGet, "/api/admin/assets", "", cookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("普通账号访问素材管理应 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodPost, "/api/admin/assets/whatever/moderation", `{"status":"HIDDEN","reason":"x"}`, cookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("普通账号处置素材应 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodGet, "/api/admin/assets/whatever", "", cookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("普通账号读取素材详情应 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
}
