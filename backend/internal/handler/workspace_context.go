package handler

import (
	"errors"
	"net/http"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/workspace"

	"github.com/gin-gonic/gin"
)

const workspaceContextKey = "canvas.workspace"

// sessionUserKey 保存托管登录解析出的账号。
//
// 本包不允许依赖认证实现（桌面二进制的依赖图里没有它），因此中间件只写入这个
// 纯数据类型，业务侧按需读取。
const sessionUserKey = "canvas.sessionUser"

// SetSessionUser 记录当前请求所属账号。
func SetSessionUser(c *gin.Context, user model.User) {
	if c == nil {
		return
	}
	c.Set(sessionUserKey, user)
}

// SessionUser 读取当前请求所属账号；未启用托管登录时返回 false。
func SessionUser(c *gin.Context) (model.User, bool) {
	if c == nil {
		return model.User{}, false
	}
	value, exists := c.Get(sessionUserKey)
	if !exists {
		return model.User{}, false
	}
	user, ok := value.(model.User)
	if !ok {
		return model.User{}, false
	}
	return user, true
}

func WorkspaceMiddleware(scope workspace.Context) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(workspaceContextKey, scope)
		c.Next()
	}
}

// SetWorkspaceScope 写入工作区作用域。
//
// 托管登录模式下作用域来自会话，由宿主侧中间件注入；这里保持写入逻辑唯一，
// 避免出现第二处直接操作上下文字段的地方。
func SetWorkspaceScope(c *gin.Context, scope workspace.Context) {
	if c == nil {
		return
	}
	c.Set(workspaceContextKey, scope)
}

// AbortUnauthorized 输出统一信封的 401 并中止请求。
func AbortUnauthorized(c *gin.Context, message string) {
	if c == nil {
		return
	}
	fail(c, http.StatusUnauthorized, errors.New(message))
	c.Abort()
}

// AbortInternal 输出统一信封的 500 并中止请求，不向客户端回显原始错误。
func AbortInternal(c *gin.Context, err error) {
	if c == nil {
		return
	}
	failInternal(c, http.StatusInternalServerError, err)
	c.Abort()
}

func CurrentWorkspace(c *gin.Context) (workspace.Context, error) {
	value, exists := c.Get(workspaceContextKey)
	if !exists {
		return workspace.Context{}, errors.New("本地工作区上下文未初始化")
	}
	scope, ok := value.(workspace.Context)
	if !ok {
		return workspace.Context{}, errors.New("本地工作区上下文无效")
	}
	if err := scope.Validate(); err != nil {
		return workspace.Context{}, err
	}
	return scope, nil
}
