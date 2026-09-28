package auth

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"infinite-canvas/backend/internal/kernel"

	"github.com/gin-gonic/gin"
)

// respondOK 输出统一业务信封。
func respondOK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"code": kernel.CodeOK, "data": data, "msg": "ok"})
}

// respondError 把模块错误投影成 HTTP 状态 + 业务 code + reason。
//
// 未分类错误一律 500 且不回显原文，避免上游响应体或凭据进入响应和日志。
func respondError(c *gin.Context, err error) {
	var authErr *Error
	if errors.As(err, &authErr) {
		body := gin.H{"code": authErr.Code, "data": nil, "msg": authErr.Message}
		if authErr.Reason != "" {
			body["reason"] = string(authErr.Reason)
		}
		if authErr.Status >= http.StatusInternalServerError {
			log.Printf("auth request failed: status=%d error_type=%T", authErr.Status, authErr.Cause)
		}
		c.JSON(authErr.Status, body)
		return
	}
	log.Printf("auth request failed: status=%d error_type=%T", http.StatusInternalServerError, err)
	c.JSON(http.StatusInternalServerError, gin.H{
		"code":   kernel.CodeInternal,
		"data":   nil,
		"msg":    "系统处理失败，请稍后重试",
		"reason": string(kernel.ReasonInternal),
	})
}

type methodsResponse struct {
	Methods []methodView `json:"methods"`
}

type methodView struct {
	MethodType  MethodType     `json:"methodType"`
	Category    MethodCategory `json:"category"`
	DisplayName string         `json:"displayName"`
	Description string         `json:"description"`
	IconType    string         `json:"iconType"`
	SortOrder   int            `json:"sortOrder"`
	AllowSignUp bool           `json:"allowSignUp"`
}

// RegisterRoutes 挂载认证路由。
//
// 只注册本模块自身的路由；宿主如何把中间件接到工作区作用域，由宿主决定，
// 避免认证模块反向依赖业务包。
func RegisterRoutes(api *gin.RouterGroup, service *Service, cookie CookieOptions) {
	if api == nil || service == nil {
		return
	}
	group := api.Group("/auth")

	group.GET("/methods", func(c *gin.Context) {
		configs, err := service.EnabledMethods(c.Request.Context())
		if err != nil {
			respondError(c, internalFailure(err))
			return
		}
		views := make([]methodView, 0, len(configs))
		for _, config := range configs {
			view := methodView{
				MethodType:  config.MethodType,
				Category:    config.Category,
				DisplayName: config.DisplayName,
				SortOrder:   config.SortOrder,
				AllowSignUp: config.AllowSignUp,
			}
			if config.Description != nil {
				view.Description = *config.Description
			}
			if config.IconType != nil {
				view.IconType = *config.IconType
			}
			views = append(views, view)
		}
		respondOK(c, methodsResponse{Methods: views})
	})

	group.POST("/verification-code", func(c *gin.Context) {
		var payload struct {
			MethodType MethodType `json:"methodType"`
			Target     string     `json:"target"`
		}
		if err := c.ShouldBindJSON(&payload); err != nil {
			respondError(c, invalidArgument("请求参数无效"))
			return
		}
		output, err := service.SendCode(c.Request.Context(), SendCodeInput{
			MethodType:  payload.MethodType,
			Target:      strings.TrimSpace(payload.Target),
			RequesterIP: c.ClientIP(),
			UserAgent:   c.Request.UserAgent(),
		})
		if err != nil {
			respondError(c, err)
			return
		}
		respondOK(c, output)
	})

	group.POST("/login", func(c *gin.Context) {
		var payload struct {
			MethodType MethodType `json:"methodType"`
			Target     string     `json:"target"`
			Code       string     `json:"code"`
			Password   string     `json:"password"`
		}
		if err := c.ShouldBindJSON(&payload); err != nil {
			respondError(c, invalidArgument("请求参数无效"))
			return
		}
		output, err := service.Login(c.Request.Context(), LoginInput{
			MethodType:  payload.MethodType,
			Target:      strings.TrimSpace(payload.Target),
			Code:        strings.TrimSpace(payload.Code),
			Password:    payload.Password,
			RequesterIP: c.ClientIP(),
			UserAgent:   c.Request.UserAgent(),
		})
		if err != nil {
			respondError(c, err)
			return
		}
		WriteSessionCookie(c, output.Token, service.SessionCookieMaxAge(), cookie)
		respondOK(c, gin.H{"user": output.User, "expiresAt": output.ExpiresAt})
	})

	group.GET("/agreements", func(c *gin.Context) {
		payload, err := service.AgreementsPayload()
		if err != nil {
			respondError(c, err)
			return
		}
		respondOK(c, payload)
	})

	// 重新同意：发布新版本后，已登录用户在这里补一次留痕，而不是被强制登出再注册。
	group.POST("/agreements/accept", func(c *gin.Context) {
		var payload struct {
			Version string `json:"version"`
		}
		if err := c.ShouldBindJSON(&payload); err != nil {
			respondError(c, invalidArgument("请求参数无效"))
			return
		}
		user, err := service.SessionUser(c.Request.Context(), ReadSessionToken(c.Request))
		if err != nil {
			respondError(c, ErrNotAuthenticated)
			return
		}
		if err := service.AcceptCurrentAgreements(user.ID, payload.Version, c.ClientIP(), c.Request.UserAgent()); err != nil {
			respondError(c, err)
			return
		}
		respondOK(c, gin.H{"accepted": true})
	})

	group.POST("/register", func(c *gin.Context) {
		var payload struct {
			MethodType       MethodType `json:"methodType"`
			Target           string     `json:"target"`
			Code             string     `json:"code"`
			Password         string     `json:"password"`
			AgreementVersion string     `json:"agreementVersion"`
		}
		if err := c.ShouldBindJSON(&payload); err != nil {
			respondError(c, invalidArgument("请求参数无效"))
			return
		}
		output, err := service.Register(c.Request.Context(), RegisterInput{
			MethodType:       payload.MethodType,
			Target:           strings.TrimSpace(payload.Target),
			Code:             strings.TrimSpace(payload.Code),
			Password:         payload.Password,
			AgreementVersion: strings.TrimSpace(payload.AgreementVersion),
			RequesterIP:      c.ClientIP(),
			UserAgent:        c.Request.UserAgent(),
		})
		if err != nil {
			respondError(c, err)
			return
		}
		WriteSessionCookie(c, output.Token, service.SessionCookieMaxAge(), cookie)
		respondOK(c, gin.H{"user": output.User, "expiresAt": output.ExpiresAt})
	})

	group.POST("/oauth/authorize", func(c *gin.Context) {
		var payload struct {
			MethodType  MethodType `json:"methodType"`
			RedirectURI string     `json:"redirectUri"`
			State       string     `json:"state"`
		}
		if err := c.ShouldBindJSON(&payload); err != nil {
			respondError(c, invalidArgument("请求参数无效"))
			return
		}
		output, err := service.AuthorizeURL(c.Request.Context(), AuthorizeInput{
			MethodType:  payload.MethodType,
			RedirectURI: payload.RedirectURI,
			State:       payload.State,
		})
		if err != nil {
			respondError(c, err)
			return
		}
		respondOK(c, output)
	})

	group.GET("/oauth/callback", func(c *gin.Context) {
		output, err := service.HandleCallback(c.Request.Context(), CallbackInput{
			MethodType:  MethodType(strings.TrimSpace(c.Query("methodType"))),
			Code:        strings.TrimSpace(c.Query("code")),
			State:       strings.TrimSpace(c.Query("state")),
			RedirectURI: strings.TrimSpace(c.Query("redirectUri")),
			RequesterIP: c.ClientIP(),
			UserAgent:   c.Request.UserAgent(),
		})
		if err != nil {
			respondError(c, err)
			return
		}
		WriteSessionCookie(c, output.Token, service.SessionCookieMaxAge(), cookie)
		respondOK(c, gin.H{"user": output.User, "expiresAt": output.ExpiresAt})
	})

	group.GET("/session", func(c *gin.Context) {
		user, err := service.SessionUser(c.Request.Context(), ReadSessionToken(c.Request))
		if err != nil {
			// 未登录不是错误：前端需要在首屏静默判断登录态。
			respondOK(c, gin.H{"user": nil})
			return
		}
		// 顺带回协议状态：前端据此判断是否需要弹出"重新同意"，不必再单独探一次。
		currentVersion, acceptedVersion, agreementErr := service.AgreementStatus(user.ID)
		if agreementErr != nil {
			respondError(c, agreementErr)
			return
		}
		respondOK(c, gin.H{
			"user": user,
			"agreements": gin.H{
				"currentVersion":  currentVersion,
				"acceptedVersion": acceptedVersion,
				"accepted":        acceptedVersion != "" && acceptedVersion == currentVersion,
			},
		})
	})

	group.POST("/logout", func(c *gin.Context) {
		if err := service.Logout(ReadSessionToken(c.Request)); err != nil {
			respondError(c, err)
			return
		}
		ClearSessionCookie(c, cookie)
		respondOK(c, gin.H{"success": true})
	})
}
