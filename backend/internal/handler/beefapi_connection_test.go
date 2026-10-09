package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/beefapi"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/workspace"

	"github.com/gin-gonic/gin"
)

func TestBeefAPIConnectionRoutesRequireWorkspaceAndHideSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	store, err := workspace.NewProviderConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := beefapi.New(beefapi.Options{
		DataDir: dir, Origin: "http://127.0.0.1:9", Provider: store, ClientVersion: "test", Hostname: "test",
		HTTPClient: http.DefaultClient, OpenURL: func(string) error { return nil }, Sleep: func(time.Duration) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	service := app.NewLocal(nil, dir)
	service.SetBeefAPI(connection)
	router := gin.New()
	router.Use(WorkspaceMiddleware(workspace.Context{ID: "local", DataDir: dir}))
	router.Use(RuntimeDependenciesMiddleware(RuntimeDependencies{
		RequestCoordinator: &stubRequestCoordinator{allowed: true},
		ProviderConfig:     store,
		BeefAPI:            connection,
	}))
	RegisterWorkspaceRoutes(router.Group("/api"), service)
	RegisterBeefAPIConnectionRoutes(router.Group("/api"), service)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/beefapi/connection", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data["state"] != beefapi.StateDisconnected {
		t.Fatalf("state = %#v", envelope.Data["state"])
	}
	if _, exists := envelope.Data["apiKey"]; exists {
		t.Fatalf("connection summary leaked apiKey: %#v", envelope.Data)
	}
}

// 托管形态下 beefapi 连接是平台级资源：整个进程一份，普通账号碰它就是碰所有账号的
// 上游出口。这里按整组逐个路由钉住，避免以后新增路由忘了挂校验。
func newBeefAPIHostedTestRouter(t *testing.T, role model.UserRole) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	store, err := workspace.NewProviderConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := beefapi.New(beefapi.Options{
		DataDir: dir, Origin: "http://127.0.0.1:9", Provider: store, ClientVersion: "test", Hostname: "test",
		HTTPClient: http.DefaultClient, OpenURL: func(string) error { return nil }, Sleep: func(time.Duration) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)

	service := app.New(nil, dir) // 托管形态
	service.SetBeefAPI(connection)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		SetSessionUser(c, model.User{ID: "account-1", DisplayName: "tester", Role: role, Status: model.UserStatusActive})
		c.Next()
	})
	router.Use(WorkspaceMiddleware(workspace.Context{ID: "account-1", DataDir: dir}))
	router.Use(RuntimeDependenciesMiddleware(RuntimeDependencies{ProviderConfig: store, BeefAPI: connection}))
	RegisterBeefAPIConnectionRoutes(router.Group("/api"), service)
	return router
}

func TestBeefAPIConnectionRejectsRegularAccountInHostedMode(t *testing.T) {
	router := newBeefAPIHostedTestRouter(t, model.UserRoleUser)
	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/beefapi/connection"},
		{http.MethodPost, "/api/beefapi/connection/start"},
		{http.MethodPost, "/api/beefapi/connection/cancel"},
		{http.MethodPost, "/api/beefapi/connection/disconnect"},
		{http.MethodPost, "/api/beefapi/connection/open-wallet"},
	}
	for _, route := range routes {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(route.method, route.path, nil))
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s %s = %d, want 403; body=%s", route.method, route.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestBeefAPIConnectionAllowsAdminInHostedMode(t *testing.T) {
	router := newBeefAPIHostedTestRouter(t, model.UserRoleAdmin)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/beefapi/connection", nil))
	if recorder.Code == http.StatusForbidden {
		t.Fatalf("管理员不该被拒绝：%s", recorder.Body.String())
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}
