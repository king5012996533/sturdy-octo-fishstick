package hosted

import (
	"net/http"
	"strings"

	"infinite-canvas/backend/internal/auth"

	"github.com/gin-gonic/gin"
)

// 用户中心的「身份绑定」接口。
//
// 换绑分两步（发码 → 确认）而不是一个提交：新地址必须先证明自己收得到信，否则用户
// 一个手误就能把账号绑到一个永远收不到验证码的邮箱上，而那正是最难自救的一种状态。
func (e *Extension) registerAccountBindingRoutes(api *gin.RouterGroup) {
	api.GET("/finance/account/bindings", e.handleAccountBindings)
	api.POST("/finance/account/bindings/code", e.handleAccountBindingSendCode)
	api.POST("/finance/account/bindings", e.handleAccountBindingConfirm)
}

func (e *Extension) handleAccountBindings(c *gin.Context) {
	user, ok := e.sessionUser(c)
	if !ok {
		return
	}
	view, err := e.service.Bindings(user.ID)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, view)
}

func (e *Extension) handleAccountBindingSendCode(c *gin.Context) {
	user, ok := e.sessionUser(c)
	if !ok {
		return
	}
	var input struct {
		Channel string `json:"channel"`
		Target  string `json:"target"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "换绑参数格式错误")
		return
	}
	result, err := e.service.SendBindingCode(c.Request.Context(), auth.BindingCodeInput{
		UserID:      user.ID,
		Channel:     auth.VerificationChannel(strings.ToUpper(strings.TrimSpace(input.Channel))),
		Target:      input.Target,
		RequesterIP: c.ClientIP(),
		UserAgent:   c.Request.UserAgent(),
	})
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, result)
}

func (e *Extension) handleAccountBindingConfirm(c *gin.Context) {
	user, ok := e.sessionUser(c)
	if !ok {
		return
	}
	var input struct {
		Channel string `json:"channel"`
		Target  string `json:"target"`
		Code    string `json:"code"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "换绑参数格式错误")
		return
	}
	view, err := e.service.ConfirmBinding(c.Request.Context(), auth.BindingConfirmInput{
		UserID:  user.ID,
		Channel: auth.VerificationChannel(strings.ToUpper(strings.TrimSpace(input.Channel))),
		Target:  input.Target,
		Code:    input.Code,
	})
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, view)
}
