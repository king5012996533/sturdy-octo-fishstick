package auth

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// 忘记密码的两条公开路由。
//
// 单独成文件而不是塞进 RegisterRoutes 的大函数里：这两条是唯一一组"未登录也要能用、
// 且能改账号凭据"的接口，安全评审时要能一眼看全它的入参与出参。
func registerPasswordResetRoutes(group *gin.RouterGroup, service *Service) {
	group.POST("/password/reset/code", func(c *gin.Context) {
		var payload struct {
			MethodType MethodType `json:"methodType"`
			Target     string     `json:"target"`
		}
		if err := c.ShouldBindJSON(&payload); err != nil {
			respondError(c, invalidArgument("请求参数无效"))
			return
		}
		output, err := service.SendPasswordResetCode(c.Request.Context(), RequestPasswordResetCodeInput{
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

	group.POST("/password/reset", func(c *gin.Context) {
		var payload struct {
			MethodType  MethodType `json:"methodType"`
			Target      string     `json:"target"`
			Code        string     `json:"code"`
			NewPassword string     `json:"newPassword"`
		}
		if err := c.ShouldBindJSON(&payload); err != nil {
			respondError(c, invalidArgument("请求参数无效"))
			return
		}
		// 带上当前会话（如果有）：在用户中心里重置的人不该被顺带登出。登录页上
		// 没有会话，取到空串，语义是"全部吊销"。
		result, err := service.ResetPassword(ResetPasswordInput{
			MethodType:       payload.MethodType,
			Target:           strings.TrimSpace(payload.Target),
			Code:             strings.TrimSpace(payload.Code),
			NewPassword:      payload.NewPassword,
			CurrentTokenHash: SessionTokenHash(ReadSessionToken(c.Request)),
		})
		if err != nil {
			respondError(c, err)
			return
		}
		respondOK(c, result)
	})
}
