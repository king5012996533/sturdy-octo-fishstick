package hosted

import (
	"net/http"
	"strings"

	"infinite-canvas/backend/internal/auth"

	"github.com/gin-gonic/gin"
)

// 用户中心的「登录设备」接口。
//
// 每个写操作都回读整份列表：设备列表是账号安全页上最容易被并发操作的一块（用户在
// 两台设备上同时清理），只回一个 204 会让另一台设备上的列表停留在过期状态，而用户
// 会据此以为"那台还没踢掉"，再点一次。
func (e *Extension) registerAccountSessionRoutes(api *gin.RouterGroup) {
	api.GET("/finance/account/sessions", e.handleAccountSessionList)
	api.DELETE("/finance/account/sessions/:id", e.handleAccountSessionRevoke)
	api.POST("/finance/account/sessions/revoke-others", e.handleAccountSessionRevokeOthers)
}

func (e *Extension) handleAccountSessionList(c *gin.Context) {
	user, ok := e.sessionUser(c)
	if !ok {
		return
	}
	views, err := e.service.ListSessions(user.ID, auth.SessionTokenHash(auth.ReadSessionToken(c.Request)))
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"sessions": views})
}

func (e *Extension) handleAccountSessionRevoke(c *gin.Context) {
	user, ok := e.sessionUser(c)
	if !ok {
		return
	}
	sessionID := strings.TrimSpace(c.Param("id"))
	if sessionID == "" {
		respondFailure(c, http.StatusBadRequest, "缺少要下线的设备标识")
		return
	}
	views, err := e.service.RevokeSession(user.ID, sessionID, auth.SessionTokenHash(auth.ReadSessionToken(c.Request)))
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"sessions": views})
}

func (e *Extension) handleAccountSessionRevokeOthers(c *gin.Context) {
	user, ok := e.sessionUser(c)
	if !ok {
		return
	}
	views, revoked, err := e.service.RevokeOtherSessions(user.ID, auth.SessionTokenHash(auth.ReadSessionToken(c.Request)))
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"sessions": views, "revoked": revoked})
}
