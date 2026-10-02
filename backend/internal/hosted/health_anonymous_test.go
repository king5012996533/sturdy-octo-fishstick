package hosted

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"infinite-canvas/backend/internal/bootstrap"

	"github.com/gin-gonic/gin"
)

// 探活必须在会话建立之前可达：容器编排和外部监控用它判断实例是否可用，被登录
// 中间件拦成 401 会把健康实例判死。但放行范围要最小——只有 health 路径公开，
// 业务路径仍然要求会话。

func TestAnonymousPathsCoverOnlyHealthProbes(t *testing.T) {
	for _, path := range []string{"/api/health/live", "/api/health/ready"} {
		if !isAnonymousPath(path) {
			t.Fatalf("%s 应当匿名可达", path)
		}
	}
	for _, path := range []string{
		"/api/projects",
		"/api/workspace/model-config",
		"/api/health/startup",
		"/api/admin/accounts",
	} {
		if isAnonymousPath(path) {
			t.Fatalf("%s 不应匿名可达", path)
		}
	}
}

func TestHostedRuntimeServesHealthProbesWithoutSession(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.db")
	runtime := openHostedRuntimeForHealth(t, dir, authPath)

	live := serveRuntime(runtime, http.MethodGet, "/api/health/live", nil)
	if live.Code != http.StatusOK {
		t.Fatalf("探活应 200，实际 %d：%s", live.Code, live.Body.String())
	}
	if !strings.Contains(live.Body.String(), "build") {
		t.Fatalf("探活应带构建信息：%s", live.Body.String())
	}

	ready := serveRuntime(runtime, http.MethodGet, "/api/health/ready", nil)
	if ready.Code == http.StatusUnauthorized {
		t.Fatalf("就绪探针被登录中间件拦成 401：%s", ready.Body.String())
	}
	if ready.Code != http.StatusOK && ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("就绪探针状态异常 %d：%s", ready.Code, ready.Body.String())
	}
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(ready.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("探活响应不是预期结构：%v %s", err, ready.Body.String())
	}
	if envelope.Data.Status == "" {
		t.Fatalf("探活响应缺少状态字段：%s", ready.Body.String())
	}
	// 匿名响应只放状态与构建信息，不能顺带把账号库路径之类的部署细节吐出去。
	for _, response := range []*httptest.ResponseRecorder{live, ready} {
		if strings.Contains(response.Body.String(), authPath) {
			t.Fatalf("探活响应泄露了账号库路径：%s", response.Body.String())
		}
	}

	guarded := serveRuntime(runtime, http.MethodGet, "/api/projects", nil)
	if guarded.Code != http.StatusUnauthorized {
		t.Fatalf("业务路径仍应要求登录，实际 %d：%s", guarded.Code, guarded.Body.String())
	}
	config := serveRuntime(runtime, http.MethodGet, "/api/workspace/model-config", nil)
	if config.Code != http.StatusUnauthorized {
		t.Fatalf("模型配置仍应要求登录，实际 %d：%s", config.Code, config.Body.String())
	}
}

func TestHostedRuntimeKeepsBusinessRoutesWorkingWithSession(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.db")
	extension, authDB, _, _ := newTestExtensionWithPaths(t, dir, authPath)
	cookie, _ := registerAccount(t, newSessionRouter(extension), authDB, "health-probe@example.com")

	runtime := openHostedRuntimeForHealth(t, dir, authPath)
	if recorder := serveRuntime(runtime, http.MethodGet, "/api/health/live", nil); recorder.Code != http.StatusOK {
		t.Fatalf("登录用户访问探活应 200，实际 %d", recorder.Code)
	}
	bootstrapResponse := serveRuntime(runtime, http.MethodGet, "/api/workspace/bootstrap", cookie)
	if bootstrapResponse.Code != http.StatusOK {
		t.Fatalf("登录用户访问业务接口应 200，实际 %d：%s", bootstrapResponse.Code, bootstrapResponse.Body.String())
	}
}

// openHostedRuntimeForHealth 用真实托管扩展装配服务端运行时：健康路由、登录中间件
// 与业务路由都走生产同一条装配路径，测出来的放行名单才有意义。
func openHostedRuntimeForHealth(t *testing.T, dir string, authPath string) *bootstrap.Runtime {
	t.Helper()
	options := Options{
		DatabaseDriver: "sqlite",
		DatabaseURL:    authPath,
		StateSecret:    "test-state-secret",
	}
	runtime, err := bootstrap.Open(context.Background(), bootstrap.Config{
		Profile:         bootstrap.ProfileServer,
		DataDir:         dir,
		ListenAddr:      "127.0.0.1:0",
		AutoMigrate:     true,
		ShutdownTimeout: 5 * time.Second,
		HostedFactory: func(deps bootstrap.HostedDeps) (bootstrap.HostedExtension, error) {
			return New(deps, options)
		},
	})
	if err != nil {
		t.Fatalf("装配托管运行时失败: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	return runtime
}

func serveRuntime(runtime *bootstrap.Runtime, method string, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(recorder, request)
	return recorder
}

// newSessionRouter 只挂登录路由，用来在同一个账号库里造会话。
func newSessionRouter(extension bootstrap.HostedExtension) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(extension.WorkspaceMiddleware())
	extension.RegisterRoutes(router.Group("/api"))
	return router
}
