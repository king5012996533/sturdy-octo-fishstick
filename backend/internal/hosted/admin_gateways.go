package hosted

import (
	"net/http"
	"strings"

	"infinite-canvas/backend/internal/auth"

	"github.com/gin-gonic/gin"
)

// 验证码投递网关（SMTP / 短信）。
//
// 这一组接口放在托管层：配置存在账号库，只有这里同时持有账号服务与审计出口。
// 密钥只写不读——读接口永远不回传密码与 AccessKeySecret，避免后台日志、截图与
// 浏览器缓存变成第二条泄露路径。

func (e *Extension) registerGatewayRoutes(group *gin.RouterGroup) {
	group.GET("/gateways", e.handleAdminGateways)
	group.PUT("/gateways/:channel", e.handleAdminUpdateGateway)
	group.POST("/gateways/:channel/test", e.handleAdminTestGateway)
}

func normalizeGatewayChannel(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case auth.GatewayChannelSMTP:
		return auth.GatewayChannelSMTP
	case auth.GatewayChannelSMS:
		return auth.GatewayChannelSMS
	default:
		return ""
	}
}

func (e *Extension) handleAdminGateways(c *gin.Context) {
	view, err := e.service.AdminGateways()
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, view)
}

func (e *Extension) handleAdminUpdateGateway(c *gin.Context) {
	channel := normalizeGatewayChannel(c.Param("channel"))
	if channel == "" {
		respondFailure(c, http.StatusBadRequest, "未知的网关通道")
		return
	}
	var input auth.GatewayUpdateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "网关配置格式错误")
		return
	}
	actor := adminActor(c)
	view, err := e.service.UpdateGateway(channel, input, actor.ID)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	// 只记"改过哪个通道、是开还是关"：密钥本身不进审计，否则留痕反而成了泄露源。
	e.recordAudit(c, "gateway.update", "gateway", channel, "更新验证码投递网关配置", gin.H{"channel": channel, "enabled": input.Enabled})
	respondOK(c, view)
}

func (e *Extension) handleAdminTestGateway(c *gin.Context) {
	channel := normalizeGatewayChannel(c.Param("channel"))
	if channel == "" {
		respondFailure(c, http.StatusBadRequest, "未知的网关通道")
		return
	}
	var input struct {
		Target string `json:"target"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "测试参数格式错误")
		return
	}
	message, err := e.service.TestGateway(c.Request.Context(), channel, input.Target)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "gateway.test", "gateway", channel, "发送网关测试验证码", nil)
	respondOK(c, gin.H{"message": message})
}
