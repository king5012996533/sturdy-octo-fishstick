package handler

import (
	"errors"
	"net/http"
	"time"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/beefapi"

	"github.com/gin-gonic/gin"
)

func RegisterBeefAPIConnectionRoutes(r *gin.RouterGroup, svc *app.Service) {
	// beefapi 是平台级企业连接：整个进程只有一份，渠道、凭据、钱包都挂在这上面。
	// 普通账号若能 start/cancel/disconnect，等于能改所有账号共用的上游出口。
	// 用子组承载，避免把这条限制加到共享的 /api 组上、波及其他接口。
	group := r.Group("")
	group.Use(func(c *gin.Context) {
		if !requireBeefAPIAdmin(c, svc) {
			c.Abort()
		}
	})

	group.GET("/beefapi/connection", func(c *gin.Context) {
		if _, err := workspaceForLocalRequest(c, svc); err != nil {
			fail(c, http.StatusUnauthorized, err)
			return
		}
		connection, err := requestBeefAPI(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, connection.Status())
	})
	group.POST("/beefapi/connection/start", func(c *gin.Context) {
		if _, err := workspaceForLocalRequest(c, svc); err != nil {
			fail(c, http.StatusUnauthorized, err)
			return
		}
		if !enforceRateLimit(c, "beefapi-start", 10, time.Minute) {
			return
		}
		connection, err := requestBeefAPI(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		summary, err := connection.Start(c.Request.Context())
		if err != nil && summary.State == "" {
			failService(c, app.BadAuthRequest(err.Error()))
			return
		}
		ok(c, summary)
	})
	group.POST("/beefapi/connection/cancel", func(c *gin.Context) {
		if _, err := workspaceForLocalRequest(c, svc); err != nil {
			fail(c, http.StatusUnauthorized, err)
			return
		}
		connection, err := requestBeefAPI(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		summary, err := connection.Cancel(c.Request.Context())
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, summary)
	})
	group.POST("/beefapi/connection/disconnect", func(c *gin.Context) {
		if _, err := workspaceForLocalRequest(c, svc); err != nil {
			fail(c, http.StatusUnauthorized, err)
			return
		}
		connection, err := requestBeefAPI(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		summary, err := connection.Disconnect(c.Request.Context())
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, summary)
	})
	group.POST("/beefapi/connection/open-wallet", func(c *gin.Context) {
		if _, err := workspaceForLocalRequest(c, svc); err != nil {
			fail(c, http.StatusUnauthorized, err)
			return
		}
		connection, err := requestBeefAPI(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := connection.OpenWallet(); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"opened": true})
	})
}

// requireBeefAPIAdmin 在托管形态下把 beefapi 连接操作收敛到管理员。
//
// 桌面形态没有管理员概念，且桌面服务只监听回环，照旧放行——否则本地单用户形态会被
// 直接锁死。托管形态下普通账号一律 403，只读也一样：连状态都能反推出平台上游的
// 登录态与余额，不属于普通账号该看到的信息。
func requireBeefAPIAdmin(c *gin.Context, svc *app.Service) bool {
	if svc == nil || svc.IsLocalMode() {
		return true
	}
	user, err := currentUser(c, svc)
	if err != nil {
		fail(c, http.StatusUnauthorized, err)
		return false
	}
	if err := svc.RequireAdmin(user); err != nil {
		failService(c, err)
		return false
	}
	return true
}

func requestBeefAPI(c *gin.Context, svc *app.Service) (*beefapi.Service, error) {
	if dependencies, ok := runtimeDependencies(c); ok && dependencies.BeefAPI != nil {
		return dependencies.BeefAPI, nil
	}
	if svc != nil {
		if connection := svc.BeefAPI(); connection != nil {
			return connection, nil
		}
	}
	return nil, errors.New("企业连接服务尚未初始化")
}
