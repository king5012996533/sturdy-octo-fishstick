package hosted

import (
	"encoding/json"
	"net/http"
	"testing"

	"infinite-canvas/backend/internal/bootstrap"
	"infinite-canvas/backend/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// newVendorRouter 在标准测试路由上补挂厂商路由。
//
// 生产装配由合并方在 registerAdminRoutes 里显式接一次；这里先检查路由是否已经
// 存在，已存在就跳过，因此合并方接线后这条测试不会因为重复注册同一批路径而 panic。
func newVendorRouter(t *testing.T) (bootstrap.HostedExtension, *gin.Engine, *gorm.DB, *gorm.DB) {
	t.Helper()
	extension, authDB, canvasDB, service := newTestExtension(t)
	// 厂商表属于托管共享结构，合并方需要在 MigrateHostedSharedSchema 里注册；
	// 这里先补建，让用例不依赖那一步的时序。
	if err := canvasDB.AutoMigrate(&model.ModelVendor{}, &model.VendorCredential{}); err != nil {
		t.Fatalf("建厂商表失败：%v", err)
	}
	router := newTestRouter(extension, service)
	ensureVendorRoutes(t, extension, router)
	return extension, router, authDB, canvasDB
}

func ensureVendorRoutes(t *testing.T, extension bootstrap.HostedExtension, router *gin.Engine) {
	t.Helper()
	for _, route := range router.Routes() {
		if route.Method == http.MethodGet && route.Path == "/api/admin/vendors" {
			return
		}
	}
	hosted, ok := extension.(*Extension)
	if !ok {
		t.Fatalf("测试扩展类型不符：%T", extension)
	}
	group := router.Group("/api/admin")
	group.Use(hosted.requireAdmin())
	hosted.registerAdminVendorRoutes(group)
}

type vendorPayload struct {
	ID              string `json:"id"`
	Code            string `json:"code"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	Enabled         bool   `json:"enabled"`
	CredentialCount int    `json:"credentialCount"`
	ModelCount      int    `json:"modelCount"`
}

type credentialPayload struct {
	ID         string `json:"id"`
	VendorID   string `json:"vendorId"`
	ChannelID  string `json:"channelId"`
	Name       string `json:"name"`
	KeyHint    string `json:"keyHint"`
	Weight     int    `json:"weight"`
	ModelCount int    `json:"modelCount"`
	HasAPIKey  bool   `json:"hasApiKey"`
}

func createVendor(t *testing.T, router *gin.Engine, cookie *http.Cookie, body string) vendorPayload {
	t.Helper()
	recorder := perform(router, http.MethodPost, "/api/admin/vendors", body, cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("新建厂商失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Vendor vendorPayload `json:"vendor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析新建厂商响应失败：%v %s", err, recorder.Body.String())
	}
	return payload.Data.Vendor
}

func vendorList(t *testing.T, router *gin.Engine, cookie *http.Cookie) []vendorPayload {
	t.Helper()
	recorder := perform(router, http.MethodGet, "/api/admin/vendors", "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取厂商列表失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Vendors []vendorPayload `json:"vendors"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析厂商列表失败：%v %s", err, recorder.Body.String())
	}
	return payload.Data.Vendors
}

// TestHostedVendorAdminRequiresAdminRole 确认厂商管理不是“登录即可访问”。
func TestHostedVendorAdminRequiresAdminRole(t *testing.T) {
	extension, router, authDB, _ := newVendorRouter(t)
	defer extension.Close()

	if recorder := perform(router, http.MethodGet, "/api/admin/vendors", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录读取厂商列表应 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	cookie, _ := registerAccount(t, router, authDB, "plain-vendor@example.com")
	if recorder := perform(router, http.MethodGet, "/api/admin/vendors", "", cookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("普通账号读取厂商列表应 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodPost, "/api/admin/vendors", `{"code":"openai","name":"OpenAI"}`, cookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("普通账号新建厂商应 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
}

// TestHostedVendorCatalogAndCRUDWithAudit 覆盖目录、厂商与凭据全链路及审计留痕。
func TestHostedVendorCatalogAndCRUDWithAudit(t *testing.T) {
	extension, router, authDB, canvasDB := newVendorRouter(t)
	defer extension.Close()
	// 凭据创建会校验出站域名可达性；用例地址固定指向回环，只放行这一个字面量主机。
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")

	adminCookie, adminID := registerAccount(t, router, authDB, "admin-vendor@example.com")
	promoteToAdmin(t, authDB, adminID)

	// 目录来自协议注册表，必须能拿到 slug 化的内置厂商。
	recorder := perform(router, http.MethodGet, "/api/admin/vendors/catalog", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取厂商目录失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if !containsText(recorder.Body.String(), "volcengine-ark") || !containsText(recorder.Body.String(), "TEXT") {
		t.Fatalf("厂商目录缺少内置厂商或能力：%s", recorder.Body.String())
	}

	created := createVendor(t, router, adminCookie, `{"code":"openai","name":"OpenAI","enabled":true,"sortOrder":3}`)
	if created.ID == "" || created.Code != "openai" || created.Kind != model.ModelVendorKindBuiltin {
		t.Fatalf("新建厂商结果不符：%#v", created)
	}

	// 重复标识与非法标识都要被拒。
	if recorder := perform(router, http.MethodPost, "/api/admin/vendors", `{"code":"openai","name":"重复"}`, adminCookie); recorder.Code != http.StatusConflict {
		t.Fatalf("重复标识应返回 409，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodPost, "/api/admin/vendors", `{"code":"Bad_Code","name":"非法"}`, adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("非法标识应返回 400，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 更新名称：列表应反映新值。
	recorder = perform(router, http.MethodPut, "/api/admin/vendors/"+created.ID, `{"code":"openai","name":"OpenAI 主链路","enabled":true}`, adminCookie)
	if recorder.Code != http.StatusOK || !containsText(recorder.Body.String(), "OpenAI 主链路") {
		t.Fatalf("更新厂商失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if vendors := vendorList(t, router, adminCookie); len(vendors) != 1 || vendors[0].Name != "OpenAI 主链路" {
		t.Fatalf("厂商列表未反映更新：%#v", vendors)
	}

	// 新建凭据：必须真的落出一条 system 渠道，并带上密钥尾号。
	recorder = perform(router, http.MethodPost, "/api/admin/vendors/"+created.ID+"/credentials",
		`{"name":"主账号","baseUrl":"http://127.0.0.1:18080/v1","apiKey":"sk-abc12345","models":["gpt-4o"],"weight":200}`,
		adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("新建凭据失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var credentialPayloadWrapper struct {
		Data struct {
			Credential credentialPayload `json:"credential"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &credentialPayloadWrapper); err != nil {
		t.Fatalf("解析凭据响应失败：%v %s", err, recorder.Body.String())
	}
	credential := credentialPayloadWrapper.Data.Credential
	if credential.ChannelID == "" || credential.KeyHint != "2345" || credential.Weight != 200 {
		t.Fatalf("凭据结果不符：%#v", credential)
	}
	var channelCount int64
	if err := canvasDB.Table("model_channels").Where("id = ? AND scope = ?", credential.ChannelID, model.ChannelScopeSystem).Count(&channelCount).Error; err != nil {
		t.Fatalf("读取渠道失败：%v", err)
	}
	if channelCount != 1 {
		t.Fatalf("凭据没有落出 system 渠道：%d", channelCount)
	}

	// 凭据列表与模型清单。
	recorder = perform(router, http.MethodGet, "/api/admin/vendors/"+created.ID+"/credentials", "", adminCookie)
	if recorder.Code != http.StatusOK || !containsText(recorder.Body.String(), "2345") {
		t.Fatalf("读取凭据列表失败：%d %s", recorder.Code, recorder.Body.String())
	}
	recorder = perform(router, http.MethodGet, "/api/admin/vendors/"+created.ID+"/credentials/"+credential.ID+"/models", "", adminCookie)
	if recorder.Code != http.StatusOK || !containsText(recorder.Body.String(), "gpt-4o") {
		t.Fatalf("读取凭据模型失败：%d %s", recorder.Code, recorder.Body.String())
	}

	// 编辑留空 API Key：尾号不变。
	recorder = perform(router, http.MethodPut, "/api/admin/vendors/"+created.ID+"/credentials/"+credential.ID,
		`{"name":"主账号 v2","baseUrl":"http://127.0.0.1:18080/v1","weight":200}`, adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("更新凭据失败：%d %s", recorder.Code, recorder.Body.String())
	}
	credentialPayloadWrapper = struct {
		Data struct {
			Credential credentialPayload `json:"credential"`
		} `json:"data"`
	}{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &credentialPayloadWrapper); err != nil {
		t.Fatalf("解析更新凭据响应失败：%v %s", err, recorder.Body.String())
	}
	if credentialPayloadWrapper.Data.Credential.KeyHint != "2345" || credentialPayloadWrapper.Data.Credential.Name != "主账号 v2" {
		t.Fatalf("留空密钥的编辑结果不符：%#v", credentialPayloadWrapper.Data.Credential)
	}

	// 有凭据时不允许删厂商。
	if recorder := perform(router, http.MethodDelete, "/api/admin/vendors/"+created.ID, "", adminCookie); recorder.Code != http.StatusConflict {
		t.Fatalf("有凭据的厂商应返回 409，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 删凭据 → 删厂商：两次都应回 {"deleted": true}。
	recorder = perform(router, http.MethodDelete, "/api/admin/vendors/"+created.ID+"/credentials/"+credential.ID, "", adminCookie)
	if recorder.Code != http.StatusOK || !containsText(recorder.Body.String(), `"deleted":true`) {
		t.Fatalf("删除凭据响应不符：%d %s", recorder.Code, recorder.Body.String())
	}
	recorder = perform(router, http.MethodDelete, "/api/admin/vendors/"+created.ID, "", adminCookie)
	if recorder.Code != http.StatusOK || !containsText(recorder.Body.String(), `"deleted":true`) {
		t.Fatalf("删除厂商响应不符：%d %s", recorder.Code, recorder.Body.String())
	}

	// 审计必须落在画布库：每个写动作至少一条。
	for _, action := range []string{"vendor.create", "vendor.update", "credential.create", "credential.update", "credential.delete", "vendor.delete"} {
		var count int64
		if err := canvasDB.Table("admin_audit_events").Where("action = ?", action).Count(&count).Error; err != nil {
			t.Fatalf("读取审计失败：%v", err)
		}
		if count < 1 {
			t.Fatalf("动作 %s 应留下审计，实际 %d 条", action, count)
		}
	}
	// 审计元数据里不允许出现密钥本体。
	var leaked int64
	if err := canvasDB.Table("admin_audit_events").
		Where("metadata_json LIKE ? OR metadata_json LIKE ?", "%sk-abc12345%", "%secret%").
		Count(&leaked).Error; err != nil {
		t.Fatalf("读取审计失败：%v", err)
	}
	if leaked != 0 {
		t.Fatalf("审计元数据泄露了密钥：%d 条", leaked)
	}
}

func containsText(haystack string, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack string, needle string) int {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return index
		}
	}
	return -1
}
