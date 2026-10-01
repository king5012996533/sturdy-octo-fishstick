package hosted

import (
	"net/http"
	"strings"

	"infinite-canvas/backend/internal/auth"

	"github.com/gin-gonic/gin"
)

// 用户中心的「密码」接口。
//
// 设置（POST）与修改（PUT）分成两个动作而不是一个"保存密码"接口：两者的证明方式
// 不同——设置要验证码，修改要旧密码。合并之后服务端就得靠"当前有没有密码"来猜
// 该验哪个，而猜错的那一刻就是一次认证绕过。
func (e *Extension) registerAccountPasswordRoutes(api *gin.RouterGroup) {
	api.GET("/finance/account/password", e.handleAccountPasswordState)
	api.POST("/finance/account/password", e.handleAccountPasswordSet)
	api.PUT("/finance/account/password", e.handleAccountPasswordChange)
}

func (e *Extension) handleAccountPasswordState(c *gin.Context) {
	user, ok := e.sessionUser(c)
	if !ok {
		return
	}
	view, err := e.service.PasswordState(user.ID)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, view)
}

func (e *Extension) handleAccountPasswordSet(c *gin.Context) {
	user, ok := e.sessionUser(c)
	if !ok {
		return
	}
	var input struct {
		MethodType  string `json:"methodType"`
		Code        string `json:"code"`
		NewPassword string `json:"newPassword"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "设置密码参数格式错误")
		return
	}
	result, err := e.service.SetPassword(c.Request.Context(), auth.SetPasswordInput{
		UserID:           user.ID,
		MethodType:       auth.MethodType(strings.ToUpper(strings.TrimSpace(input.MethodType))),
		Code:             input.Code,
		NewPassword:      input.NewPassword,
		CurrentTokenHash: auth.SessionTokenHash(auth.ReadSessionToken(c.Request)),
	})
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, result)
}

func (e *Extension) handleAccountPasswordChange(c *gin.Context) {
	user, ok := e.sessionUser(c)
	if !ok {
		return
	}
	var input struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "修改密码参数格式错误")
		return
	}
	result, err := e.service.ChangePassword(auth.ChangePasswordInput{
		UserID:           user.ID,
		CurrentPassword:  input.CurrentPassword,
		NewPassword:      input.NewPassword,
		CurrentTokenHash: auth.SessionTokenHash(auth.ReadSessionToken(c.Request)),
	})
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, result)
}
