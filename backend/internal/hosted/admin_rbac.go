package hosted

import (
	"net/http"
	"strings"

	"infinite-canvas/backend/internal/auth"

	"github.com/gin-gonic/gin"
)

// 角色与权限管理。
//
// 这一组接口读写 auth_roles / auth_user_roles，因此挂在托管层：只有这里同时持有账号
// 服务与审计出口。整组再用 requirePermission(auth.PermRbacManage) 收一道口——上游的
// requireAdmin 只回答"是不是管理员"，而"谁能改角色"必须能独立授予，否则想给运营开
// 一项权限，就只剩"把他提成管理员"这一条路。
//
// 角色本身不带任何凭据字段（密码、令牌、渠道密钥），读写都只在角色定义与账号绑定上，
// 因此这一层不需要像网关/支付那样做脱敏，反而要保证不把凭据类字段混进响应。

// registerAdminRbacRoutes 挂载 /api/admin 下的角色与权限路由（group 已带管理员守卫）。
//
// 每条路由再挂一次 requirePermission：分组守卫回答"是不是管理员"，这里回答"能不能
// 管权限"。两者独立，任一不满足都必须在解析请求体之前被拦下。
func (e *Extension) registerAdminRbacRoutes(group *gin.RouterGroup) {
	guard := e.requirePermission(auth.PermRbacManage)
	group.GET("/permissions", guard, e.handleAdminPermissions)
	group.GET("/roles", guard, e.handleAdminRoles)
	group.POST("/roles", guard, e.handleAdminRoleCreate)
	group.PUT("/roles/:id", guard, e.handleAdminRoleUpdate)
	group.DELETE("/roles/:id", guard, e.handleAdminRoleDelete)
	group.GET("/users/:id/roles", guard, e.handleAdminUserRoles)
	group.PUT("/users/:id/roles", guard, e.handleAdminAssignUserRoles)
}

// requirePermission 收敛细粒度权限：app_users.role == "ADMIN" 的账号直接放行（既有超管语义不变），
// 其余账号按 auth_user_roles 里角色的权限并集判断，缺失即 403。
//
// 会话在这里自己解析，而不是读 requireAdmin 写进上下文的键：
//   - 这枚守卫要能独立挂在任何分组上（契约里也是这么用的），依赖上游的上下文键会让它
//     只在"恰好排在 requireAdmin 之后"时可用，那是一种看不见的隐式耦合；
//   - 解析结果只用于授权判定，账号已经由会话校验过，因此这里不需要再查一遍账号表。
//
// ADMIN 旁路不能去掉：平台的既有超管用的是 app_users.role，若某次迁移漏了 auth_user_roles
// 的播种，ADMIN 仍然必须进得来，否则一次数据问题会把所有人锁在后台之外。
func (e *Extension) requirePermission(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, err := e.service.SessionUser(c.Request.Context(), auth.ReadSessionToken(c.Request))
		if err != nil {
			respondFailure(c, http.StatusUnauthorized, "当前未登录或登录已失效")
			c.Abort()
			return
		}
		// 顺手写入上下文主体：审计需要"是谁操作的"，而挂载顺序不该影响留痕的完整性。
		c.Set(hostedAdminUserKey, user)

		if user.Role == auth.RoleAdmin {
			c.Next()
			return
		}
		permissions, err := e.service.EffectivePermissions(user.ID)
		if err != nil {
			respondServiceError(c, err)
			c.Abort()
			return
		}
		for _, code := range permissions {
			if code == permission {
				c.Next()
				return
			}
		}
		respondFailure(c, http.StatusForbidden, "缺少必要的操作权限")
		c.Abort()
	}
}

func (e *Extension) handleAdminPermissions(c *gin.Context) {
	respondOK(c, gin.H{"permissions": e.service.PermissionCatalog()})
}

func (e *Extension) handleAdminRoles(c *gin.Context) {
	views, err := e.service.AdminRoles()
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, gin.H{"roles": billingList(views)})
}

func (e *Extension) handleAdminRoleCreate(c *gin.Context) {
	var input auth.RoleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "角色内容格式错误")
		return
	}
	// POST 一律按新建处理：带上 ID 的报文语义含糊，而且 ID 由服务端分配，客户端不该决定。
	input.ID = ""
	view, err := e.service.SaveRole(input)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "role.create", "role", view.ID, "新建角色「"+view.Name+"」", gin.H{
		"code":        view.Code,
		"permissions": view.Permissions,
	})
	respondOK(c, gin.H{"role": view})
}

func (e *Extension) handleAdminRoleUpdate(c *gin.Context) {
	var input auth.RoleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "角色内容格式错误")
		return
	}
	// 以路径参数为准：报文里的 ID 与路径不一致时，按路径操作才能让"改的是哪一个"可见。
	input.ID = strings.TrimSpace(c.Param("id"))
	view, err := e.service.SaveRole(input)
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "role.update", "role", view.ID, "更新角色「"+view.Name+"」", gin.H{
		"code":        view.Code,
		"permissions": view.Permissions,
	})
	respondOK(c, gin.H{"role": view})
}

func (e *Extension) handleAdminRoleDelete(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if err := e.service.DeleteRole(id); err != nil {
		respondServiceError(c, err)
		return
	}
	// 角色已经被删，审计只剩 ID 可留：这不是缺字段，而是"删了才来记"的必然结果。
	e.recordAudit(c, "role.delete", "role", id, "删除角色", nil)
	respondOK(c, gin.H{"id": id})
}

func (e *Extension) handleAdminUserRoles(c *gin.Context) {
	view, err := e.service.UserRoleViewFor(c.Param("id"))
	if err != nil {
		respondServiceError(c, err)
		return
	}
	respondOK(c, view)
}

func (e *Extension) handleAdminAssignUserRoles(c *gin.Context) {
	var input struct {
		RoleCodes []string `json:"roleCodes"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		respondFailure(c, http.StatusBadRequest, "角色分配格式错误")
		return
	}
	target := strings.TrimSpace(c.Param("id"))
	view, err := e.service.AssignUserRoles(target, input.RoleCodes, adminActorID(c))
	if err != nil {
		respondServiceError(c, err)
		return
	}
	e.recordAudit(c, "user.roles.update", "user", target, "更新账号角色", gin.H{
		"roleCodes": view.RoleCodes,
	})
	respondOK(c, view)
}
