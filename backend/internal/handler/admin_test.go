package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type adminEnvelope struct {
	Code int             `json:"code"`
	Data json.RawMessage `json:"data"`
	Msg  string          `json:"msg"`
}

func newAdminTestEnv(t *testing.T, role model.UserRole) (*gin.Engine, *app.Service, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	// 管理端会触碰渠道、模型、路由和审计表：直接用本地结构清单加托管审计表，
	// 避免逐个列举后随上游结构变化而失效。
	if err := db.AutoMigrate(append(database.LocalModels(), &model.AdminAuditEvent{})...); err != nil {
		t.Fatal(err)
	}
	svc := app.New(repository.New(db), t.TempDir())
	t.Cleanup(func() { _ = svc.Close() })

	router := gin.New()
	group := router.Group("/api")
	group.Use(RuntimeDependenciesMiddleware(defaultRuntimeDependencies(svc)))
	group.Use(func(c *gin.Context) {
		SetSessionUser(c, model.User{ID: "admin-1", DisplayName: "管理员", Role: role})
	})
	RegisterAdminRoutes(group, svc)
	return router, svc, db
}

func adminCall(t *testing.T, router *gin.Engine, method string, path string, body any) (*httptest.ResponseRecorder, adminEnvelope) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	var envelope adminEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("%s %s returned non-JSON body: %s", method, path, recorder.Body.String())
	}
	return recorder, envelope
}

func TestAdminRoutesRejectNonAdminSession(t *testing.T) {
	router, _, _ := newAdminTestEnv(t, model.UserRoleUser)

	recorder, envelope := adminCall(t, router, http.MethodGet, "/api/admin/channels", nil)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if envelope.Code != http.StatusForbidden {
		t.Fatalf("envelope code = %d", envelope.Code)
	}
}

// 协议下拉必须由服务端下发：管理端如果把协议清单写死在前端，注册表一旦演进，
// 就会出现"下拉里能选中、保存时报协议无效"的分裂。这里守住它至少含内置协议。
func TestAdminProtocolCatalogListsBuiltinProtocols(t *testing.T) {
	router, _, _ := newAdminTestEnv(t, model.UserRoleAdmin)

	recorder, envelope := adminCall(t, router, http.MethodGet, "/api/admin/protocols", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("protocols status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Providers []app.PluginProviderCatalogItem `json:"providers"`
	}
	if err := json.Unmarshal(envelope.Data, &payload); err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]bool, len(payload.Providers))
	for _, provider := range payload.Providers {
		ids[provider.ID] = true
	}
	// chat-completion 是渠道模型保存时的最小可用协议；缺失意味着管理端建不出文本模型。
	if !ids["chat-completion"] {
		t.Fatalf("protocol catalog missing chat-completion: %v", ids)
	}

	// 能力过滤要走服务端而不是前端筛选，过滤结果不能混入其他能力。
	recorder, envelope = adminCall(t, router, http.MethodGet, "/api/admin/protocols?capability=text", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("filtered protocols status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(envelope.Data, &payload); err != nil {
		t.Fatal(err)
	}
	for _, provider := range payload.Providers {
		if len(provider.Categories) == 0 || provider.Categories[0] != "text" {
			t.Fatalf("capability filter leaked %s: %v", provider.ID, provider.Categories)
		}
	}
}

func TestAdminChannelAndModelLifecycle(t *testing.T) {
	router, _, _ := newAdminTestEnv(t, model.UserRoleAdmin)

	recorder, envelope := adminCall(t, router, http.MethodPost, "/api/admin/channels", map[string]any{
		"name": "测试平台渠道", "baseUrl": "https://example.com/v1", "apiKey": "sk-admin-secret",
		"models": []string{}, "enabled": true,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("create channel status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	var created app.PublicModelChannel
	if err := json.Unmarshal(envelope.Data, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || !created.HasAPIKey {
		t.Fatalf("created channel = %+v", created)
	}
	// 管理视图只回"是否已配置密钥"，绝不回显密钥本身。
	if strings.Contains(string(envelope.Data), "sk-admin-secret") {
		t.Fatal("admin channel response leaked the API key")
	}

	channelPath := "/api/admin/channels/" + created.ID
	recorder, _ = adminCall(t, router, http.MethodGet, channelPath, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("detail status = %d body = %s", recorder.Code, recorder.Body.String())
	}

	recorder, envelope = adminCall(t, router, http.MethodPost, channelPath+"/models", map[string]any{
		"modelKey": "kino-text", "providerModelKey": "upstream-text", "displayName": "平台文本模型",
		"capability": "text", "protocol": "chat-completion", "enabled": true,
		"capabilityConfig": app.DefaultModelCapabilityConfigForModel("chat-completion", "kino-text"),
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("create model status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	var createdModel model.ChannelModel
	if err := json.Unmarshal(envelope.Data, &createdModel); err != nil {
		t.Fatal(err)
	}
	if createdModel.ID == "" || createdModel.ModelKey != "kino-text" {
		t.Fatalf("created model = %+v", createdModel)
	}

	recorder, envelope = adminCall(t, router, http.MethodGet, channelPath+"/models", nil)
	if recorder.Code != http.StatusOK || !strings.Contains(string(envelope.Data), "kino-text") {
		t.Fatalf("model list status = %d body = %s", recorder.Code, recorder.Body.String())
	}

	recorder, _ = adminCall(t, router, http.MethodPut, channelPath+"/models/"+createdModel.ID, map[string]any{
		"modelKey": "kino-text", "providerModelKey": "upstream-text-v2", "displayName": "平台文本模型 v2",
		"capability": "text", "protocol": "chat-completion", "enabled": false,
		"capabilityConfig": app.DefaultModelCapabilityConfigForModel("chat-completion", "kino-text"),
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update model status = %d body = %s", recorder.Code, recorder.Body.String())
	}

	recorder, _ = adminCall(t, router, http.MethodDelete, channelPath+"/models/"+createdModel.ID, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete model status = %d body = %s", recorder.Code, recorder.Body.String())
	}

	recorder, _ = adminCall(t, router, http.MethodPut, channelPath, map[string]any{
		"name": "测试平台渠道", "baseUrl": "https://example.com/v1", "apiKey": "", "enabled": false,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update channel status = %d body = %s", recorder.Code, recorder.Body.String())
	}

	recorder, envelope = adminCall(t, router, http.MethodGet, "/api/admin/channels?status=disabled", nil)
	if recorder.Code != http.StatusOK || !strings.Contains(string(envelope.Data), created.ID) {
		t.Fatalf("disabled list status = %d body = %s", recorder.Code, recorder.Body.String())
	}

	recorder, _ = adminCall(t, router, http.MethodDelete, channelPath, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete channel status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	recorder, envelope = adminCall(t, router, http.MethodGet, "/api/admin/channels", nil)
	if recorder.Code != http.StatusOK || strings.Contains(string(envelope.Data), created.ID) {
		t.Fatalf("channel still listed after delete: %s", envelope.Data)
	}
}

func TestAdminChannelModelRejectsInvalidCapability(t *testing.T) {
	router, _, _ := newAdminTestEnv(t, model.UserRoleAdmin)
	_, envelope := adminCall(t, router, http.MethodPost, "/api/admin/channels", map[string]any{
		"name": "渠道", "baseUrl": "https://example.com/v1", "apiKey": "sk-x",
	})
	var channel app.PublicModelChannel
	if err := json.Unmarshal(envelope.Data, &channel); err != nil {
		t.Fatal(err)
	}

	recorder, _ := adminCall(t, router, http.MethodPost, "/api/admin/channels/"+channel.ID+"/models", map[string]any{
		"modelKey": "bad-model", "capability": "not-a-capability", "protocol": "chat-completion",
		"capabilityConfig": app.DefaultModelCapabilityConfigForModel("chat-completion", "bad-model"),
	})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminFeaturesToggleRoundTrip(t *testing.T) {
	router, svc, _ := newAdminTestEnv(t, model.UserRoleAdmin)

	recorder, envelope := adminCall(t, router, http.MethodPut, "/api/admin/features", app.FeatureAvailability{
		CustomChannelsEnabled: false, FrontendModelsEnabled: false,
		ShortDramaEnabled: true, TaskCenterEnabled: true, PluginCenterEnabled: true,
		SystemPluginsVisibleToUsers: true, TimelineTranscriptionEnabled: true,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("features update status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	enabled, err := svc.FeatureEnabled(app.FeatureFrontendModels)
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Fatal("frontendModels must stay disabled in platform-managed mode")
	}

	recorder, envelope = adminCall(t, router, http.MethodGet, "/api/admin/features", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("features read status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	var features map[string]any
	if err := json.Unmarshal(envelope.Data, &features); err != nil {
		t.Fatal(err)
	}
	if features["customChannelsEnabled"] != false || features["configured"] != true {
		t.Fatalf("features = %v", features)
	}
}

func TestAdminRuntimePolicyRoundTripAndReset(t *testing.T) {
	router, _, _ := newAdminTestEnv(t, model.UserRoleAdmin)

	recorder, envelope := adminCall(t, router, http.MethodGet, "/api/admin/runtime-policy", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("policy read status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	// data 就是 RuntimePolicySetting 的形状（内嵌字段），可以直接回写。
	recorder, _ = adminCall(t, router, http.MethodPut, "/api/admin/runtime-policy", json.RawMessage(envelope.Data))
	if recorder.Code != http.StatusOK {
		t.Fatalf("policy update status = %d body = %s", recorder.Code, recorder.Body.String())
	}

	recorder, _ = adminCall(t, router, http.MethodDelete, "/api/admin/runtime-policy", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("policy reset status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminChannelOrderRoundTrip(t *testing.T) {
	router, _, _ := newAdminTestEnv(t, model.UserRoleAdmin)
	for _, name := range []string{"渠道 A", "渠道 B"} {
		recorder, _ := adminCall(t, router, http.MethodPost, "/api/admin/channels", map[string]any{
			"name": name, "baseUrl": "https://example.com/v1", "apiKey": "sk-x",
		})
		if recorder.Code != http.StatusOK {
			t.Fatalf("seed channel status = %d body = %s", recorder.Code, recorder.Body.String())
		}
	}

	recorder, envelope := adminCall(t, router, http.MethodGet, "/api/admin/order", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("order read status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Items []app.ChannelOrderItem `json:"items"`
	}
	if err := json.Unmarshal(envelope.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 2 {
		t.Fatalf("order items = %+v", payload.Items)
	}

	reversed := []string{payload.Items[1].ID, payload.Items[0].ID}
	expected := []string{payload.Items[0].ID, payload.Items[1].ID}
	recorder, _ = adminCall(t, router, http.MethodPut, "/api/admin/order", map[string]any{
		"ids": reversed, "expectedIds": expected,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("order save status = %d body = %s", recorder.Code, recorder.Body.String())
	}

	// 期望列表过期必须冲突，避免两个管理员互相覆盖排序。
	recorder, _ = adminCall(t, router, http.MethodPut, "/api/admin/order", map[string]any{
		"ids": expected, "expectedIds": expected,
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("stale order save status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}
