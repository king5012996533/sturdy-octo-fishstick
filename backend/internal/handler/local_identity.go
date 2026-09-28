package handler

import (
	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/model"

	"github.com/gin-gonic/gin"
)

// currentUser resolves the account a request belongs to.
//
// 托管登录启用时用会话账号，桌面本地模式回落到固定的本地拥有者。两条路径都必须
// 经由工作区作用域，避免出现「身份」与「数据归属」来自不同来源的情况。
func currentUser(c *gin.Context, svc *app.Service) (*model.User, error) {
	if user, ok := SessionUser(c); ok {
		return &user, nil
	}
	if _, exists := c.Get(workspaceContextKey); exists {
		scope, err := CurrentWorkspace(c)
		if err != nil {
			return nil, err
		}
		return svc.WorkspaceOwner(scope.ID)
	}
	return svc.LocalWorkspaceOwner()
}
