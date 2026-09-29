package hosted

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/bootstrap"
	"infinite-canvas/backend/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// newInspirationRouter 在标准测试路由上补建灵感表。
//
// 生产装配由 RegisterRoutes / registerAdminRoutes 显式接一次；这里复用同一套装配，
// 等于顺带验证了新路由的挂载点与守卫要求。
func newInspirationRouter(t *testing.T) (bootstrap.HostedExtension, *gin.Engine, *gorm.DB, *gorm.DB) {
	t.Helper()
	extension, authDB, canvasDB, service := newTestExtension(t)
	// 表已由 MigrateHostedSharedSchema 建好，这里再建一次只是让用例不依赖迁移时序。
	if err := canvasDB.AutoMigrate(&model.CreationInspiration{}); err != nil {
		t.Fatalf("建精选灵感表失败：%v", err)
	}
	router := newTestRouter(extension, service)
	return extension, router, authDB, canvasDB
}

type inspirationPayload struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Mode     string `json:"mode"`
	Category string `json:"category"`
	Status   string `json:"status"`
	Featured bool   `json:"featured"`
	Author   string `json:"author"`
	Likes    int    `json:"likes"`
}

func createInspiration(t *testing.T, router *gin.Engine, cookie *http.Cookie, body string) inspirationPayload {
	t.Helper()
	recorder := perform(router, http.MethodPost, "/api/admin/inspirations", body, cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("新建灵感失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Inspiration inspirationPayload `json:"inspiration"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析新建灵感响应失败：%v %s", err, recorder.Body.String())
	}
	return payload.Data.Inspiration
}

func inspirationTitles(t *testing.T, router *gin.Engine, path string, cookie *http.Cookie) []string {
	t.Helper()
	recorder := perform(router, http.MethodGet, path, "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取灵感列表失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Inspirations []inspirationPayload `json:"inspirations"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析灵感列表失败：%v %s", err, recorder.Body.String())
	}
	titles := make([]string, 0, len(payload.Data.Inspirations))
	for _, item := range payload.Data.Inspirations {
		titles = append(titles, item.Title)
	}
	return titles
}

// TestHostedInspirationAdminCRUDAndAudit 覆盖后台增删改、校验与审计留痕。
func TestHostedInspirationAdminCRUDAndAudit(t *testing.T) {
	extension, router, authDB, canvasDB := newInspirationRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "admin-inspiration@example.com")
	promoteToAdmin(t, authDB, adminID)

	created := createInspiration(t, router, adminCookie,
		`{"title":"雨夜霓虹","description":"宽银幕构图","coverUrl":"https://cdn.example.com/neon.jpg","prompt":"雨夜城市街口","mode":"VIDEO","category":"","author":"","likes":-3,"sourceUrl":"","source":"","status":"online","featured":true,"sortOrder":5}`)
	if created.ID == "" || created.Title != "雨夜霓虹" {
		t.Fatalf("新建结果缺少主键或标题：%#v", created)
	}
	// 空分类必须回落到默认「精选」，否则前台筛选会少一档；负点赞归零。
	if created.Category != "精选" || created.Status != "ONLINE" || created.Mode != "video" || created.Likes != 0 || !created.Featured {
		t.Fatalf("新建结果的分类/状态/模式/点赞不符：%#v", created)
	}

	if titles := inspirationTitles(t, router, "/api/admin/inspirations", adminCookie); len(titles) != 1 {
		t.Fatalf("后台列表应含 1 条：%#v", titles)
	}

	// 更新：改成下架，并改署名字段。
	updateBody := `{"title":"雨夜霓虹 v2","mode":"image","author":"YOUNG","likes":12,"sourceUrl":"https://www.liblib.tv/detail/abc","status":"OFFLINE","featured":false,"sortOrder":1}`
	recorder := perform(router, http.MethodPut, "/api/admin/inspirations/"+created.ID, updateBody, adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("更新灵感失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "雨夜霓虹 v2") || !strings.Contains(recorder.Body.String(), "OFFLINE") {
		t.Fatalf("更新响应未反映新值：%s", recorder.Body.String())
	}

	// 非法模式与空标题都要被拒。
	if recorder := perform(router, http.MethodPost, "/api/admin/inspirations",
		`{"title":"非法模式","mode":"agent","status":"ONLINE"}`, adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("非法模式应返回 400，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodPost, "/api/admin/inspirations",
		`{"title":"  ","mode":"video","status":"ONLINE"}`, adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("空标题应返回 400，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodPost, "/api/admin/inspirations",
		`{"title":"草稿","mode":"video","status":"DRAFT"}`, adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("非法状态应返回 400，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 删除后回全量列表，且列表里不再有它。
	recorder = perform(router, http.MethodDelete, "/api/admin/inspirations/"+created.ID, "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("删除灵感失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), created.ID) {
		t.Fatalf("删除后的全量列表仍包含被删灵感：%s", recorder.Body.String())
	}
	if recorder := perform(router, http.MethodDelete, "/api/admin/inspirations/INSP-missing", "", adminCookie); recorder.Code != http.StatusNotFound {
		t.Fatalf("删除不存在的灵感应 404，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 审计必须落在画布库，三个动作各留一条。
	for _, action := range []string{"inspiration.create", "inspiration.update", "inspiration.delete"} {
		var count int64
		if err := canvasDB.Table("admin_audit_events").Where("action = ?", action).Count(&count).Error; err != nil {
			t.Fatalf("读取审计失败：%v", err)
		}
		if count != 1 {
			t.Fatalf("动作 %s 应留 1 条审计，实际 %d 条", action, count)
		}
	}
}

// TestHostedInspirationCatalogOnlyOnline 覆盖前台目录的登录要求与上下架过滤。
func TestHostedInspirationCatalogOnlyOnline(t *testing.T) {
	extension, router, authDB, _ := newInspirationRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "admin-inspiration-catalog@example.com")
	promoteToAdmin(t, authDB, adminID)
	createInspiration(t, router, adminCookie, `{"title":"公开灵感","mode":"video","status":"ONLINE","sortOrder":1}`)
	createInspiration(t, router, adminCookie, `{"title":"下架灵感","mode":"video","status":"OFFLINE","sortOrder":2}`)

	// 未登录必须 401，且不能泄露任何标题。
	if recorder := perform(router, http.MethodGet, "/api/inspirations", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录读取目录应 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	userCookie, _ := registerAccount(t, router, authDB, "inspiration-reader@example.com")
	if titles := inspirationTitles(t, router, "/api/inspirations", userCookie); strings.Join(titles, ",") != "公开灵感" {
		t.Fatalf("前台目录应只含已上架灵感：%#v", titles)
	}
}

// TestHostedInspirationAdminRequiresAdminRole 确认灵感管理不是登录即可访问。
func TestHostedInspirationAdminRequiresAdminRole(t *testing.T) {
	extension, router, authDB, _ := newInspirationRouter(t)
	defer extension.Close()

	cookie, _ := registerAccount(t, router, authDB, "plain-inspiration@example.com")
	if recorder := perform(router, http.MethodGet, "/api/admin/inspirations", "", cookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("普通账号读取灵感列表应 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodPost, "/api/admin/inspirations", `{"title":"x","mode":"video","status":"ONLINE"}`, cookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("普通账号新建灵感应 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
}
