package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/workspace"

	"github.com/gin-gonic/gin"
)

// 托管形态下 /workspace/model-config 是平台级运营配置：普通账号读脱敏目录、
// 写被拒绝，管理员读写照旧，桌面形态完全不受影响。

const (
	testUserChannelSecret     = "user-private-key"
	testSystemChannelSecret   = "platform-system-key"
	testBeefAPIChannelSecret  = "platform-beefapi-secret"
	testBeefAPIChannelHeader  = "Bearer platform-header"
	testHostedModelConfigUser = "account-1"
)

func newHostedModelConfigTestRouter(t *testing.T, role model.UserRole) (*gin.Engine, *workspace.ProviderConfig) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	store, err := workspace.NewProviderConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	service := app.New(nil, dir)
	router := gin.New()
	// 宿主侧中间件在真实请求里注入会话账号与工作区作用域，这里按同样的顺序复现。
	router.Use(func(c *gin.Context) {
		SetSessionUser(c, model.User{ID: testHostedModelConfigUser, DisplayName: "tester", Role: role, Status: model.UserStatusActive})
		c.Next()
	})
	router.Use(WorkspaceMiddleware(workspace.Context{ID: testHostedModelConfigUser, DataDir: dir}))
	router.Use(RuntimeDependenciesMiddleware(RuntimeDependencies{ProviderConfig: store}))
	RegisterWorkspaceRoutes(router.Group("/api"), service)
	return router, store
}

func seedModelConfigChannels(t *testing.T, store *workspace.ProviderConfig) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"channels": []any{
			map[string]any{
				"id": "beefapi", "name": "BeefAPI", "pinned": true, "enabled": true,
				"apiKey": testBeefAPIChannelSecret, "headers": map[string]any{"Authorization": testBeefAPIChannelHeader},
			},
			map[string]any{"id": "CHANNEL_000003", "name": "Replicate · 主账号", "scope": "system", "apiKey": testSystemChannelSecret},
			map[string]any{"id": "user-relay", "name": "我的中转", "scope": "user", "apiKey": testUserChannelSecret},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLocalModelConfig(body); err != nil {
		t.Fatal(err)
	}
}

func getModelConfig(t *testing.T, router *gin.Engine) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/workspace/model-config", nil))
	return recorder
}

func modelConfigChannelIDs(t *testing.T, data map[string]any) []string {
	t.Helper()
	config, _ := data["config"].(map[string]any)
	channels, _ := config["channels"].([]any)
	ids := make([]string, 0, len(channels))
	for _, raw := range channels {
		channel, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := channel["id"].(string)
		ids = append(ids, id)
	}
	return ids
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestHostedModelConfigRestrictsRegularAccountRead(t *testing.T) {
	router, store := newHostedModelConfigTestRouter(t, model.UserRoleUser)
	seedModelConfigChannels(t, store)

	recorder := getModelConfig(t, router)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	data := modelConfigResponseData(t, recorder)
	if data["source"] != "platform-catalog" {
		t.Fatalf("普通账号应拿到脱敏目录视图：%#v", data["source"])
	}
	ids := modelConfigChannelIDs(t, data)
	if !containsString(ids, "beefapi") || !containsString(ids, "CHANNEL_000003") {
		t.Fatalf("平台渠道被误删：%#v", ids)
	}
	if containsString(ids, "user-relay") {
		t.Fatalf("用户自定义渠道不该出现在平台目录里：%#v", ids)
	}
	for _, secret := range []string{testBeefAPIChannelSecret, testBeefAPIChannelHeader, testSystemChannelSecret, testUserChannelSecret} {
		if bytes.Contains(recorder.Body.Bytes(), []byte(secret)) {
			t.Fatalf("脱敏目录泄露了凭据 %q：%s", secret, recorder.Body.String())
		}
	}
}

func TestHostedModelConfigRejectsRegularAccountWrite(t *testing.T) {
	router, store := newHostedModelConfigTestRouter(t, model.UserRoleUser)
	seedModelConfigChannels(t, store)
	before, err := store.ReadLocalModelConfig()
	if err != nil {
		t.Fatal(err)
	}

	recorder := putModelConfig(t, router, 1, "must-not-write")
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("普通账号写入应 403，实际 = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code   int    `json:"code"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != http.StatusForbidden || envelope.Reason != "forbidden" {
		t.Fatalf("需要机器可读的 403 forbidden，实际 = %#v", envelope)
	}

	after, err := store.ReadLocalModelConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("被拒绝的写入仍然改动了配置：\n之前 %s\n之后 %s", before, after)
	}
}

func TestHostedModelConfigAllowsAdminReadAndWrite(t *testing.T) {
	router, store := newHostedModelConfigTestRouter(t, model.UserRoleAdmin)
	seedModelConfigChannels(t, store)

	get := getModelConfig(t, router)
	if get.Code != http.StatusOK {
		t.Fatalf("管理员读取失败 status = %d body=%s", get.Code, get.Body.String())
	}
	data := modelConfigResponseData(t, get)
	if data["source"] != "builtin+local" {
		t.Fatalf("管理员应拿到完整视图：%#v", data["source"])
	}
	if ids := modelConfigChannelIDs(t, data); !containsString(ids, "user-relay") {
		t.Fatalf("管理员看不到完整渠道列表：%#v", ids)
	}

	put := putModelConfig(t, router, 1, "admin-rewrite")
	if put.Code != http.StatusOK {
		t.Fatalf("管理员写入失败 status = %d body=%s", put.Code, put.Body.String())
	}
}

func TestLocalModelConfigKeepsFullAccess(t *testing.T) {
	router, store := newModelConfigTestRouter(t)
	seedModelConfigChannels(t, store)

	get := getModelConfig(t, router)
	if get.Code != http.StatusOK {
		t.Fatalf("本地读取失败 status = %d body=%s", get.Code, get.Body.String())
	}
	data := modelConfigResponseData(t, get)
	if ids := modelConfigChannelIDs(t, data); !containsString(ids, "user-relay") {
		t.Fatalf("本地形态不该被裁剪渠道：%#v", ids)
	}
	put := putModelConfig(t, router, 1, "local-rewrite")
	if put.Code != http.StatusOK {
		t.Fatalf("本地写入失败 status = %d body=%s", put.Code, put.Body.String())
	}
}
