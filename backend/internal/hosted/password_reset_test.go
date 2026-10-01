package hosted

import (
	"encoding/json"
	"net/http"
	"testing"

	"gorm.io/gorm"
)

// 忘记密码的 HTTP 契约。
//
// 这一组挂在 /api/auth 下，也就是那条整体公开的路径上——它是整个后端里唯一一组
// "不需要会话就能改账号凭据"的接口，因此用例只盯三件事：路由真的在、公开可达但不越权、
// 验证码场景没有串台。

// codeInScene 取指定场景下最新的可用验证码。
//
// 不能复用 latestCode：那个只按 target 取最新一条，而本组用例刻意在同一个目标上同时
// 存在登录码与重置码，取错场景就测不出"场景隔离"这件事本身。
func unusedCodeCount(t *testing.T, authDB *gorm.DB, target string, scene string) int64 {
	t.Helper()
	var count int64
	if err := authDB.Table("auth_verification_codes").
		Where("target = ? AND scene = ? AND used_at IS NULL", target, scene).
		Count(&count).Error; err != nil {
		t.Fatalf("统计 %q 场景的验证码失败: %v", scene, err)
	}
	return count
}

func codeInScene(t *testing.T, authDB *gorm.DB, target string, scene string) string {
	t.Helper()
	var record struct {
		Code string `gorm:"column:code"`
	}
	if err := authDB.Table("auth_verification_codes").
		Where("target = ? AND scene = ? AND used_at IS NULL", target, scene).
		Order("created_at desc").First(&record).Error; err != nil {
		t.Fatalf("账号库里没有 %q 场景的可用验证码: %v", scene, err)
	}
	return record.Code
}

// TestHostedPasswordResetFlow 覆盖「忘记密码 → 换掉密码 → 旧会话全部失效」整条链路。
func TestHostedPasswordResetFlow(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	cookie, _ := registerAccount(t, router, authDB, "forgot@example.com")

	// 下发重置验证码：不带会话也要能拿到，否则登录页上那个"忘记密码"根本走不通。
	recorder := perform(router, http.MethodPost, "/api/auth/password/reset/code",
		`{"methodType":"EMAIL_CODE","target":"forgot@example.com"}`, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("下发重置验证码失败：%d %s", recorder.Code, recorder.Body.String())
	}
	// 场景必须落在 password_reset 上：注册时那条登录码已经被核销，如果这里又出现一条
	// 未使用的 login 码，说明下发走的是登录场景，冷却也会跟着串在一起。
	if unused := unusedCodeCount(t, authDB, "forgot@example.com", "login"); unused != 0 {
		t.Fatalf("重置验证码不该落在登录场景里，login 场景出现了 %d 条未使用的码", unused)
	}
	if unused := unusedCodeCount(t, authDB, "forgot@example.com", "password_reset"); unused != 1 {
		t.Fatalf("重置验证码应落在 password_reset 场景，实际 %d 条", unused)
	}
	code := codeInScene(t, authDB, "forgot@example.com", "password_reset")

	recorder = perform(router, http.MethodPost, "/api/auth/password/reset",
		`{"methodType":"EMAIL_CODE","target":"forgot@example.com","code":"`+code+`","newPassword":"brandnew9876"}`, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("重置密码失败：%d %s", recorder.Code, recorder.Body.String())
	}

	// 重置是在没有会话的情况下发起的，因此注册时那条会话也必须失效。
	if recorder = perform(router, http.MethodGet, "/api/finance/account/profile", "", cookie); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("重置后旧会话应失效，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 新密码能登录，说明落库的是用户刚设的那一串（而不是被哈希成了别的什么）。
	recorder = perform(router, http.MethodPost, "/api/auth/login",
		`{"methodType":"PASSWORD","target":"forgot@example.com","password":"brandnew9876"}`, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("用新密码登录失败：%d %s", recorder.Code, recorder.Body.String())
	}
	recorder = perform(router, http.MethodPost, "/api/auth/login",
		`{"methodType":"PASSWORD","target":"forgot@example.com","password":"whatever123"}`, nil)
	if recorder.Code == http.StatusOK {
		t.Fatal("随便一个密码不该能登录")
	}
}

// TestHostedPasswordResetRefusesLoginSceneCode 是这条接口最要紧的一条边界：
// 登录场景的码不能拿来改密码，否则登录页上的验证码就成了一张改密授权书。
func TestHostedPasswordResetRefusesLoginSceneCode(t *testing.T) {
	extension, router, authDB, _ := newAdminRouter(t)
	defer extension.Close()

	_, _ = registerAccount(t, router, authDB, "forgot-scene@example.com")
	loginCode := issueAccountCode(t, router, authDB, "forgot-scene@example.com")

	recorder := perform(router, http.MethodPost, "/api/auth/password/reset",
		`{"methodType":"EMAIL_CODE","target":"forgot-scene@example.com","code":"`+loginCode+`","newPassword":"brandnew9876"}`, nil)
	if recorder.Code == http.StatusOK {
		t.Fatalf("登录场景的验证码不该能重置密码：%s", recorder.Body.String())
	}
}

// TestHostedPasswordResetRoutesAreAnonymousAndValidated 确认这两条在公开路径上
// 返回的是业务错误而不是 401，同时非法入参被挡在业务层之前。
func TestHostedPasswordResetRoutesAreAnonymousAndValidated(t *testing.T) {
	extension, router, _, _ := newAdminRouter(t)
	defer extension.Close()

	recorder := perform(router, http.MethodPost, "/api/auth/password/reset/code", `{"methodType":"PASSWORD","target":"x"}`, nil)
	if recorder.Code == http.StatusUnauthorized {
		t.Fatal("重置路由必须公开可达：未登录时不该被会话中间件拦下")
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("密码通道不该能用于重置，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	recorder = perform(router, http.MethodPost, "/api/auth/password/reset", `{"methodType":"EMAIL_CODE","target":"nobody@example.com","code":"123456","newPassword":"brandnew9876"}`, nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("未注册的标识应返回 404，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 新密码要过长度规则；错误码必须是 400 而不是把用户引向"系统处理失败"。
	var envelope struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	recorder = perform(router, http.MethodPost, "/api/auth/password/reset", `not json`, nil)
	if recorder.Code != http.StatusBadRequest || json.Unmarshal(recorder.Body.Bytes(), &envelope) != nil {
		t.Fatalf("非法请求体应返回 400 业务信封，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
}
