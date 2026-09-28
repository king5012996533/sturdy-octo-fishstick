package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"

	"github.com/gin-gonic/gin"
)

// stubHostedExtension 代表服务端注入的托管能力：认证路由公开，业务路由要求会话。
type stubHostedExtension struct {
	routesRegistered bool
	closed           bool
}

func (s *stubHostedExtension) WorkspaceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/auth/") {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": http.StatusUnauthorized, "data": nil, "msg": "当前未登录或登录已失效"})
	}
}

func (s *stubHostedExtension) RegisterRoutes(api *gin.RouterGroup) {
	s.routesRegistered = true
	api.GET("/auth/methods", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"methods": []any{}}, "msg": "ok"})
	})
}

func (s *stubHostedExtension) Close() error {
	s.closed = true
	return nil
}

func openRuntimeForRoutes(t *testing.T, config Config) *Runtime {
	t.Helper()
	config.DataDir = t.TempDir()
	config.AutoMigrate = true
	config.ShutdownTimeout = 5 * time.Second
	runtime, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("启动运行时失败: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	return runtime
}

func serve(runtime *Runtime, method string, path string, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	if token != "" {
		request.Header.Set("X-Desktop-Token", token)
	}
	recorder := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(recorder, request)
	return recorder
}

// TestRuntimeWithoutHostedFactoryKeepsLocalSingleWorkspace 守住桌面形态：
// 没有注入托管能力时，登录路由不存在，业务接口也不要求会话。
func TestRuntimeWithoutHostedFactoryKeepsLocalSingleWorkspace(t *testing.T) {
	runtime := openRuntimeForRoutes(t, Config{Profile: ProfileDesktop})
	token := runtime.LaunchToken()

	if recorder := serve(runtime, http.MethodGet, "/api/auth/methods", token); recorder.Code != http.StatusNotFound {
		t.Fatalf("桌面形态不应存在登录路由，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := serve(runtime, http.MethodGet, "/api/workspace/bootstrap", token); recorder.Code != http.StatusOK {
		t.Fatalf("桌面形态的业务接口不应要求登录，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
}

// TestRuntimeWithHostedFactoryGatesBusinessRoutes 确认托管能力由工厂注入：
// 登录路由被挂载，业务路由在无会话时被拦下，关闭时释放扩展。
func TestRuntimeWithHostedFactoryGatesBusinessRoutes(t *testing.T) {
	extension := &stubHostedExtension{}
	runtime := openRuntimeForRoutes(t, Config{
		Profile: ProfileServer,
		HostedFactory: func(deps HostedDeps) (HostedExtension, error) {
			if deps.Service == nil {
				t.Error("托管能力应当拿到业务服务")
			}
			return extension, nil
		},
	})
	if !extension.routesRegistered {
		t.Fatal("托管登录路由未被注册")
	}

	if recorder := serve(runtime, http.MethodGet, "/api/auth/methods", ""); recorder.Code != http.StatusOK {
		t.Fatalf("登录路由应当公开，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := serve(runtime, http.MethodGet, "/api/workspace/bootstrap", ""); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("托管形态的业务接口应当要求登录，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("关闭运行时失败: %v", err)
	}
	if !extension.closed {
		t.Fatal("关闭运行时未释放托管扩展")
	}
}

// TestRuntimeWithHostedFactoryCreatesHostedSharedTables 守住审计表责任边界。
//
// admin_audit_events 被 LocalModels 刻意排除（桌面产物必须能证明结构里没有托管表），
// 所以托管实例的建表只能在装配托管能力时完成。漏掉它不影响启动，却会让第一个
// 管理员写操作在运行期 500 —— 这类缺口必须在启动阶段就被测试拦下。
func TestRuntimeWithHostedFactoryCreatesHostedSharedTables(t *testing.T) {
	runtime := openRuntimeForRoutes(t, Config{
		Profile: ProfileServer,
		HostedFactory: func(deps HostedDeps) (HostedExtension, error) {
			return &stubHostedExtension{}, nil
		},
	})
	if !runtime.db.Migrator().HasTable(&model.AdminAuditEvent{}) {
		t.Fatal("托管实例缺少 admin_audit_events，管理员写操作会失败")
	}
}
