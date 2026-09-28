package hosted

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/bootstrap"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/handler"
	"infinite-canvas/backend/internal/repository"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newTestExtension 装配一套完整的托管登录：独立的账号库 + 画布库。
//
// 两张库都回传给用例：审计与调用量落在画布库，账号与角色落在账号库，
// 管理端读数是两者合并的结果，断言必须能分别落到底。
func newTestExtension(t *testing.T) (bootstrap.HostedExtension, *gorm.DB, *gorm.DB, *app.Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	canvasDB, err := database.Open(database.Config{Driver: "sqlite", DataDir: filepath.Join(dir, "canvas")})
	if err != nil {
		t.Fatalf("打开画布库失败: %v", err)
	}
	if err := database.MigrateLocalSchema(canvasDB); err != nil {
		t.Fatalf("初始化画布库失败: %v", err)
	}
	// 与托管运行时一致：审计流水是托管专属表，本地结构迁移刻意不含它。
	if err := database.MigrateHostedSharedSchema(canvasDB); err != nil {
		t.Fatalf("初始化托管共享结构失败: %v", err)
	}
	service := app.NewLocal(repository.New(canvasDB), filepath.Join(dir, "canvas"))

	authPath := filepath.Join(dir, "auth.db")
	extension, err := New(bootstrap.HostedDeps{DataDir: dir, Service: service}, Options{
		DatabaseDriver: "sqlite",
		DatabaseURL:    authPath,
		StateSecret:    "test-state-secret",
	})
	if err != nil {
		t.Fatalf("装配托管扩展失败: %v", err)
	}
	authDB, err := gorm.Open(sqlite.Open(authPath), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("打开账号库失败: %v", err)
	}
	return extension, authDB, canvasDB, service
}

func newTestRouter(extension bootstrap.HostedExtension, service *app.Service) *gin.Engine {
	router := gin.New()
	router.Use(extension.WorkspaceMiddleware())
	api := router.Group("/api")
	extension.RegisterRoutes(api)
	// 业务路由用真实注册表：内容审核要在用户自己的画布读写路径上生效，用假路由
	// 覆盖不到那一段，测出来的"下架"就只是接口存在而已。
	handler.RegisterDesktopCanvasAPI(api, service)
	// 用测试专属路径而不是真实的 /workspace/bootstrap：真实路由由业务注册表提供，
	// 这里只验证"会话被映射成了工作区"，重复注册同一个路径会直接 panic。
	api.GET("/test-workspace", func(c *gin.Context) {
		scope, err := handler.CurrentWorkspace(c)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"msg": err.Error()})
			return
		}
		user, _ := handler.SessionUser(c)
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"workspaceId": scope.ID,
			"userId":      user.ID,
			"role":        string(user.Role),
			"displayName": user.DisplayName,
		}})
	})
	return router
}

func perform(router *gin.Engine, method string, path string, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func sessionCookie(t *testing.T, recorder *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == "canana_session" && cookie.Value != "" {
			return cookie
		}
	}
	t.Fatalf("登录响应没有下发会话 Cookie: %s", recorder.Body.String())
	return nil
}

// TestHostedRegisterScopesWorkspaceToAccount 覆盖「未登录 → 协议 → 邮箱验证码注册 → 会话 → 工作区」全链路。
func TestHostedRegisterScopesWorkspaceToAccount(t *testing.T) {
	extension, authDB, _, service := newTestExtension(t)
	defer extension.Close()
	router := newTestRouter(extension, service)

	// 未登录访问业务接口必须被拦下。
	if recorder := perform(router, http.MethodGet, "/api/test-workspace", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录应返回 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 登录方式列表属于公开路由，且开发库应已补齐邮箱验证码。
	recorder := perform(router, http.MethodGet, "/api/auth/methods", "", nil)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "EMAIL_CODE") {
		t.Fatalf("登录方式列表异常：%d %s", recorder.Code, recorder.Body.String())
	}

	// 协议同样是公开路由：注册页必须在未登录时就能拿到版本与正文。
	recorder = perform(router, http.MethodGet, "/api/auth/agreements", "", nil)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "用户协议") {
		t.Fatalf("协议接口异常：%d %s", recorder.Code, recorder.Body.String())
	}
	var agreements struct {
		Data struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &agreements); err != nil || agreements.Data.Version == "" {
		t.Fatalf("解析协议响应失败: %v %s", err, recorder.Body.String())
	}

	// 下发验证码：响应体里不得出现验证码本身。
	const target = "e2e@example.com"
	recorder = perform(router, http.MethodPost, "/api/auth/verification-code",
		`{"methodType":"EMAIL_CODE","target":"`+target+`"}`, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("下发验证码失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var record struct {
		Code string `gorm:"column:code"`
	}
	if err := authDB.Table("auth_verification_codes").
		Where("target = ? AND used_at IS NULL", target).
		Order("created_at desc").First(&record).Error; err != nil {
		t.Fatalf("账号库里没有验证码记录: %v", err)
	}
	if record.Code == "" || strings.Contains(recorder.Body.String(), record.Code) {
		t.Fatalf("验证码泄漏到响应体：%s", recorder.Body.String())
	}

	// 用验证码注册：登录不再自动建号，建号必须走注册入口并带上协议版本。
	recorder = perform(router, http.MethodPost, "/api/auth/register",
		`{"methodType":"EMAIL_CODE","target":"`+target+`","code":"`+record.Code+`","agreementVersion":"`+agreements.Data.Version+`"}`, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("注册失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var login struct {
		Data struct {
			User struct {
				ID    string `json:"id"`
				Email string `json:"email"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &login); err != nil {
		t.Fatalf("解析注册响应失败: %v", err)
	}
	if login.Data.User.ID == "" || login.Data.User.Email != target {
		t.Fatalf("注册响应缺少用户信息：%s", recorder.Body.String())
	}

	// 留痕必须落库：注册成功却没有协议记录等于没有同意过。
	var agreementCount int64
	if err := authDB.Table("app_user_agreements").Where("user_id = ?", login.Data.User.ID).Count(&agreementCount).Error; err != nil {
		t.Fatalf("读取协议留痕失败: %v", err)
	}
	if agreementCount != 2 {
		t.Fatalf("协议留痕应为 2 条，实际 %d 条", agreementCount)
	}

	// 携带会话访问业务接口：工作区必须等于账号 ID。
	cookie := sessionCookie(t, recorder)
	recorder = perform(router, http.MethodGet, "/api/test-workspace", "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("已登录访问失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), login.Data.User.ID) {
		t.Fatalf("工作区未绑定到账号：want=%s got=%s", login.Data.User.ID, recorder.Body.String())
	}
	// 身份必须来自会话账号，而不是本地模式里那个固定管理员。
	var bootstrap struct {
		Data struct {
			UserID      string `json:"userId"`
			Role        string `json:"role"`
			DisplayName string `json:"displayName"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &bootstrap); err != nil {
		t.Fatalf("解析业务响应失败: %v", err)
	}
	if bootstrap.Data.UserID != login.Data.User.ID {
		t.Fatalf("业务侧身份不是会话账号：got=%s want=%s", bootstrap.Data.UserID, login.Data.User.ID)
	}
	if bootstrap.Data.Role != "user" || bootstrap.Data.DisplayName == "" {
		t.Fatalf("普通账号不应被当成管理员：%+v", bootstrap.Data)
	}
}

// TestWorkspaceMiddlewareRejectsForeignSession 确认伪造令牌无法换取工作区。
func TestWorkspaceMiddlewareRejectsForeignSession(t *testing.T) {
	extension, _, _, service := newTestExtension(t)
	defer extension.Close()
	router := newTestRouter(extension, service)

	forged := &http.Cookie{Name: "canana_session", Value: "not-a-real-session-token"}
	if recorder := perform(router, http.MethodGet, "/api/test-workspace", "", forged); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("伪造会话应返回 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	// 认证路由不参与会话校验，否则用户无法登录。
	if recorder := perform(router, http.MethodGet, "/api/auth/methods", "", forged); recorder.Code != http.StatusOK {
		t.Fatalf("认证路由不应被会话中间件拦截：%d %s", recorder.Code, recorder.Body.String())
	}
}

// TestHostedPublicAppearanceIsAnonymous 确认登录页在拿到会话之前就能读到平台品牌。
//
// 这一条是运营可见面的回归线：登录页的 Logo 与文案来自公开外观接口，如果它被会话
// 中间件拦住，后台改完品牌后访客看到的仍是内置默认值（默认值与线上恰好一致时，
// 页面上看不出任何异常，问题会被一直掩盖）。
func TestHostedPublicAppearanceIsAnonymous(t *testing.T) {
	extension, _, _, service := newTestExtension(t)
	defer extension.Close()
	router := newTestRouter(extension, service)

	recorder := perform(router, http.MethodGet, "/api/public/appearance", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("匿名读取公开外观应返回 200，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Appearance struct {
				BrandName string `json:"brandName"`
			} `json:"appearance"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil || payload.Data.Appearance.BrandName == "" {
		t.Fatalf("公开外观响应缺少品牌名：%s", recorder.Body.String())
	}

	// 未配置资源时应落到处理器的 404（未配置），而不是中间件的 401。
	assetRecorder := perform(router, http.MethodGet, "/api/public/appearance/assets/logo", "", nil)
	if assetRecorder.Code == http.StatusUnauthorized {
		t.Fatalf("匿名读取外观资源不应被会话中间件拦截：%s", assetRecorder.Body.String())
	}

	// 其余业务接口仍然必须登录：放行的只有上面那两类路径。
	if recorder := perform(router, http.MethodGet, "/api/admin/users", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("管理端仍应要求登录，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
}
