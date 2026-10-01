package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const contextUserKey = "auth.currentUser"

// ReadSessionToken 从请求 Cookie 中读取会话令牌。
func ReadSessionToken(req *http.Request) string {
	if req == nil {
		return ""
	}
	cookie, err := req.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}

// WriteSessionCookie 写入会话 Cookie。
//
// HttpOnly 让它对脚本不可见；生产环境由 CookieOptions.Secure 追加 Secure。
func WriteSessionCookie(c *gin.Context, token string, maxAgeSeconds int, options CookieOptions) {
	if c == nil {
		return
	}
	sameSite := options.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteLaxMode
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAgeSeconds,
		HttpOnly: true,
		Secure:   options.Secure,
		SameSite: sameSite,
	})
}

// ClearSessionCookie 清除会话 Cookie。
func ClearSessionCookie(c *gin.Context, options CookieOptions) {
	if c == nil {
		return
	}
	sameSite := options.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteLaxMode
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   options.Secure,
		SameSite: sameSite,
	})
}

// RequireSession 校验会话并把用户写入请求上下文。
//
// 这是宿主把「请求」映射到「用户」的唯一入口：业务代码只读取上下文里的用户，
// 不自行解析 Cookie，避免出现第二套身份来源。
func (s *Service) RequireSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := ReadSessionToken(c.Request)
		user, err := s.SessionUser(c.Request.Context(), token)
		if err != nil {
			respondError(c, err)
			c.Abort()
			return
		}
		c.Set(contextUserKey, user)
		c.Next()
	}
}

// OptionalSession 解析会话但不强制要求已登录。
func (s *Service) OptionalSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := ReadSessionToken(c.Request)
		if token == "" {
			c.Next()
			return
		}
		if user, err := s.SessionUser(c.Request.Context(), token); err == nil {
			c.Set(contextUserKey, user)
		}
		c.Next()
	}
}

// RequireAdmin 在已登录基础上要求管理员角色。
func (s *Service) RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := CurrentUser(c)
		if !ok {
			respondError(c, ErrNotAuthenticated)
			c.Abort()
			return
		}
		if user.Role != RoleAdmin {
			respondError(c, forbidden("当前账号没有后台管理权限"))
			c.Abort()
			return
		}
		c.Next()
	}
}

// CurrentUser 读取上下文中的登录用户。
func CurrentUser(c *gin.Context) (*AuthUser, bool) {
	if c == nil {
		return nil, false
	}
	value, exists := c.Get(contextUserKey)
	if !exists {
		return nil, false
	}
	user, ok := value.(*AuthUser)
	if !ok || user == nil {
		return nil, false
	}
	return user, true
}

// CurrentUserID 返回当前用户 ID；未登录时返回空串。
func CurrentUserID(c *gin.Context) string {
	user, ok := CurrentUser(c)
	if !ok {
		return ""
	}
	return user.ID
}

// SessionTokenHash 是会话令牌的摘要。
//
// 对外暴露摘要而不是明文令牌：调用方（宿主的用户中心）需要用它判断"哪一条会话是当前
// 这一台"，而判断这件事只需要摘要——把明文令牌散进更多层，等于给它更多泄漏面。
func SessionTokenHash(token string) string {
	return hashSessionToken(strings.TrimSpace(token))
}
