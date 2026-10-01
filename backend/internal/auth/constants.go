package auth

import (
	"net/http"
	"strings"
	"time"
)

// 路由常量。
//
// 路径与 CanvasMind 保持一致：同一个前端的登录页不需要因为宿主不同而分叉。
const (
	BasePath          = "/api/auth"
	MethodsPath       = BasePath + "/methods"
	VerificationPath  = BasePath + "/verification-code"
	LoginPath         = BasePath + "/login"
	RegisterPath      = BasePath + "/register"
	AgreementsPath    = BasePath + "/agreements"
	AuthorizePath     = BasePath + "/oauth/authorize"
	OAuthCallbackPath = BasePath + "/oauth/callback"
	SessionPath       = BasePath + "/session"
	LogoutPath        = BasePath + "/logout"

	// 忘记密码：下发重置验证码，以及带码重置。两条都在登录态之外可达。
	PasswordResetCodePath = BasePath + "/password/reset/code"
	PasswordResetPath     = BasePath + "/password/reset"
)

// codeScene 固定为 login，与 CanvasMind 的验证码场景取值一致。
const codeScene = "login"

// passwordResetScene 是重置密码验证码的场景值。
//
// 与登录共用码表但分开场景，是因为冷却按 (通道, 目标, 场景) 计数：用户在登录页点过
// 一次"发送验证码"，转头点"忘记密码"时如果共用一个场景，会被上一分钟的冷却挡住，
// 而他能看到的只有一句"发送过于频繁"。
const passwordResetScene = "password_reset"

// sendCodeScene 归一验证码场景，留空即登录场景。
//
// 场景只由服务端决定：下发接口不接收客户端传来的场景值。多一个可选择场景就是给
// 验证码开一条互不消耗的旁路——谁能选场景，谁就能绕过冷却无限发码。
func sendCodeScene(scene string) string {
	if trimmed := strings.TrimSpace(scene); trimmed != "" {
		return trimmed
	}
	return codeScene
}

// CookieName 是会话 Cookie 名，与 CanvasMind 共用，便于两个前端共享登录态。
const CookieName = SessionCookieName

func codeTTL(minutes int) time.Duration {
	return time.Duration(minutes) * time.Minute
}

// CookieOptions 描述会话 Cookie 的写入参数。
type CookieOptions struct {
	// Secure 由部署形态决定：生产必须为 true。
	Secure bool
	// SameSite 默认 Lax；Lax 已阻止跨站 POST 携带 Cookie，
	// 但跨站顶链跳转仍会带上，因此回调路径仍需 state 校验。
	SameSite http.SameSite
}
