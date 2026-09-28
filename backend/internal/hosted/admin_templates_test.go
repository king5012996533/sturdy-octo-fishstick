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

// newTemplateRouter 在标准测试路由上补挂模板路由。
//
// 生产装配由合并方在 registerAdminRoutes / RegisterRoutes 里显式接一次；这里直接
// 调用两个注册函数，等于提前验证了它们的签名与守卫要求。
func newTemplateRouter(t *testing.T) (bootstrap.HostedExtension, *gin.Engine, *gorm.DB, *gorm.DB) {
	t.Helper()
	extension, authDB, canvasDB, service := newTestExtension(t)
	// 表已由 MigrateHostedSharedSchema 建好，这里再建一次只是让用例不依赖迁移时序。
	if err := canvasDB.AutoMigrate(&model.CanvasTemplate{}); err != nil {
		t.Fatalf("建画布模板表失败：%v", err)
	}
	router := newTestRouter(extension, service)
	return extension, router, authDB, canvasDB
}

type templatePayload struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Status   string `json:"status"`
	Featured bool   `json:"featured"`
}

func createTemplate(t *testing.T, router *gin.Engine, cookie *http.Cookie, body string) templatePayload {
	t.Helper()
	recorder := perform(router, http.MethodPost, "/api/admin/templates", body, cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("新建模板失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Template templatePayload `json:"template"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析新建模板响应失败：%v %s", err, recorder.Body.String())
	}
	return payload.Data.Template
}

func templateList(t *testing.T, router *gin.Engine, path string, cookie *http.Cookie) []templatePayload {
	t.Helper()
	recorder := perform(router, http.MethodGet, path, "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取模板列表失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Templates []templatePayload `json:"templates"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析模板列表失败：%v %s", err, recorder.Body.String())
	}
	return payload.Data.Templates
}

func templateCodesOf(items []templatePayload) []string {
	codes := make([]string, 0, len(items))
	for _, item := range items {
		codes = append(codes, item.Code)
	}
	return codes
}

// TestHostedTemplateAdminCRUDAndAudit 覆盖后台增删改、校验与审计留痕。
func TestHostedTemplateAdminCRUDAndAudit(t *testing.T) {
	extension, router, authDB, canvasDB := newTemplateRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "admin-template@example.com")
	promoteToAdmin(t, authDB, adminID)

	created := createTemplate(t, router, adminCookie,
		`{"code":"drama","name":"短剧分镜","description":"三格分镜","category":"","coverUrl":"https://cdn.example.com/drama.png","payloadJson":"{\"nodes\":[]}","status":"ONLINE","featured":true,"sortOrder":5}`)
	if created.ID == "" || created.Code != "drama" {
		t.Fatalf("新建结果缺少主键或标识：%#v", created)
	}
	// 空分类必须回落到默认「通用」，否则用户端筛选会少一档。
	if created.Category != "通用" || created.Status != "ONLINE" || !created.Featured {
		t.Fatalf("新建结果分类/状态/推荐位不符：%#v", created)
	}

	if list := templateList(t, router, "/api/admin/templates", adminCookie); len(list) != 1 {
		t.Fatalf("后台列表应含 1 条：%#v", list)
	}

	// 更新：改成下架，并改名称。
	updateBody := `{"code":"drama","name":"短剧分镜 v2","payloadJson":"{}","status":"OFFLINE","featured":false,"sortOrder":1}`
	recorder := perform(router, http.MethodPut, "/api/admin/templates/"+created.ID, updateBody, adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("更新模板失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "短剧分镜 v2") || !strings.Contains(recorder.Body.String(), "OFFLINE") {
		t.Fatalf("更新响应未反映新值：%s", recorder.Body.String())
	}

	// 非法标识与重复标识都要被拒。
	if recorder := perform(router, http.MethodPost, "/api/admin/templates",
		`{"code":"Bad_Code","name":"非法","status":"ONLINE"}`, adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("大写/下划线标识应返回 400，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodPost, "/api/admin/templates",
		`{"code":"drama","name":"重复","status":"ONLINE"}`, adminCookie); recorder.Code != http.StatusConflict {
		t.Fatalf("重复标识应返回 409，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodPost, "/api/admin/templates",
		`{"code":"draft","name":"草稿","status":"DRAFT"}`, adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("非法状态应返回 400，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 删除后回全量列表，且列表里不再有它。
	recorder = perform(router, http.MethodDelete, "/api/admin/templates/"+created.ID, "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("删除模板失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), created.ID) {
		t.Fatalf("删除后的全量列表仍包含被删模板：%s", recorder.Body.String())
	}
	if recorder := perform(router, http.MethodDelete, "/api/admin/templates/ctpl-missing", "", adminCookie); recorder.Code != http.StatusNotFound {
		t.Fatalf("删除不存在的模板应 404，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 审计必须落在画布库，三个动作各留一条。
	for _, action := range []string{"template.create", "template.update", "template.delete"} {
		var count int64
		if err := canvasDB.Table("admin_audit_events").Where("action = ?", action).Count(&count).Error; err != nil {
			t.Fatalf("读取审计失败：%v", err)
		}
		if count != 1 {
			t.Fatalf("动作 %s 应留 1 条审计，实际 %d 条", action, count)
		}
	}
}

// TestHostedTemplateCatalogOnlyOnline 覆盖用户端目录的登录要求与上下架过滤。
func TestHostedTemplateCatalogOnlyOnline(t *testing.T) {
	extension, router, authDB, _ := newTemplateRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "admin-catalog@example.com")
	promoteToAdmin(t, authDB, adminID)
	createTemplate(t, router, adminCookie, `{"code":"public","name":"公开模板","status":"ONLINE","sortOrder":1}`)
	createTemplate(t, router, adminCookie, `{"code":"hidden","name":"下架模板","status":"OFFLINE","sortOrder":2}`)

	// 未登录必须 401，且不能泄露任何模板名。
	if recorder := perform(router, http.MethodGet, "/api/templates", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录读取目录应 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	userCookie, _ := registerAccount(t, router, authDB, "reader@example.com")
	items := templateList(t, router, "/api/templates", userCookie)
	if codes := strings.Join(templateCodesOf(items), ","); codes != "public" {
		t.Fatalf("用户端目录应只含已上架模板：%v", codes)
	}
}

// TestHostedTemplateAdminRequiresAdminRole 确认模板管理不是登录即可访问。
func TestHostedTemplateAdminRequiresAdminRole(t *testing.T) {
	extension, router, authDB, _ := newTemplateRouter(t)
	defer extension.Close()

	cookie, _ := registerAccount(t, router, authDB, "plain-template@example.com")
	if recorder := perform(router, http.MethodGet, "/api/admin/templates", "", cookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("普通账号读取模板列表应 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodPost, "/api/admin/templates", `{"code":"x","name":"x","status":"ONLINE"}`, cookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("普通账号新建模板应 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
}
