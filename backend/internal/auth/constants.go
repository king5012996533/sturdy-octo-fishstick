package auth

import (
	"net/http"
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
)

// codeScene 固定为 login，与 CanvasMind 的验证码场景取值一致。
const codeScene = "login"

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
