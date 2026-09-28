package hosted

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/auth"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RBAC 响应的读模型只声明被断言的字段：服务端多出的字段由响应全文兜底检查，
// 这里不跟着改动，类型就不会因为后端加字段而失效。
type rbacRoleRow struct {
	ID          string   `json:"id"`
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	Builtin     bool     `json:"builtin"`
	Permissions []string `json:"permissions"`
	UserCount   int64    `json:"userCount"`
}

type rbacRolesData struct {
	Roles []rbacRoleRow `json:"roles"`
	Role  rbacRoleRow   `json:"role"`
}

type rbacCatalogData struct {
	Permissions []struct {
		Code  string `json:"code"`
		Name  string `json:"name"`
		Group string `json:"group"`
	} `json:"permissions"`
}

type rbacUserRolesData struct {
	UserID      string   `json:"userId"`
	RoleCodes   []string `json:"roleCodes"`
	Permissions []string `json:"permissions"`
}

func rbacDecode[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()
	var payload struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析 RBAC 响应失败: %v %s", err, recorder.Body.String())
	}
	return payload.Data
}

// newRbacAdminRouter 复用标准托管路由（RBAC 路由已由 registerAdminRoutes 挂载）。
//
// 断言覆盖的就是上线的那组 URL（/api/admin/roles 等），不在这里重复注册——
// 同一方法+路径注册两次 gin 会直接 panic。
func newRbacAdminRouter(t *testing.T) (*Extension, *gin.Engine, *gorm.DB) {
	t.Helper()
	extension, authDB, _, service := newTestExtension(t)
	if err := auth.EnsureRbacSchema(authDB); err != nil {
		t.Fatalf("初始化 RBAC 表失败: %v", err)
	}
	router := newTestRouter(extension, service)
	hostedExt := extension.(*Extension)

	// 探针路由只挂 requirePermission：真实的 /api/admin 分组前面还有 requireAdmin，
	// 非管理员根本走不到细粒度守卫，而"给了权限就要能进"这条正是要单独验证的行为。
	probe := router.Group("/api/rbac-probe")
	probe.Use(hostedExt.requirePermission(auth.PermRbacManage))
	probe.GET("/roles", func(c *gin.Context) { respondOK(c, gin.H{"ok": true}) })
	return hostedExt, router, authDB
}

// TestHostedRbacRoleLifecycle 覆盖角色 CRUD、内置边界与账号分配的全链路。
func TestHostedRbacRoleLifecycle(t *testing.T) {
	extension, router, authDB := newRbacAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "rbac-admin@example.com")
	promoteToAdmin(t, authDB, adminID)
	victimCookie, victimID := registerAccount(t, router, authDB, "rbac-victim@example.com")

	// 权限点目录：冻结的 10 项，且带分组供后台多选。
	recorder := perform(router, http.MethodGet, "/api/admin/permissions", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("权限点目录失败：%d %s", recorder.Code, recorder.Body.String())
	}
	catalog := rbacDecode[rbacCatalogData](t, recorder).Permissions
	if len(catalog) != 10 {
		t.Fatalf("权限点目录应为 10 项，实际 %d 项：%s", len(catalog), recorder.Body.String())
	}
	for _, permission := range catalog {
		if permission.Group == "" || permission.Name == "" {
			t.Fatalf("权限点缺少中文名或分组：%+v", permission)
		}
	}

	// 内置角色：四个，全部带内置标记；响应里不得混进任何凭据字段。
	recorder = perform(router, http.MethodGet, "/api/admin/roles", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("角色列表失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if containsAnySecret(recorder.Body.String()) {
		t.Fatalf("角色列表泄漏凭据字段：%s", recorder.Body.String())
	}
	roles := rbacDecode[rbacRolesData](t, recorder).Roles
	if len(roles) != 4 {
		t.Fatalf("内置角色应为 4 个，实际 %d 个：%s", len(roles), recorder.Body.String())
	}
	builtinIDs := map[string]string{}
	for _, role := range roles {
		if !role.Builtin {
			t.Fatalf("内置角色 %s 缺少 builtin 标记", role.Code)
		}
		builtinIDs[role.Code] = role.ID
	}

	// 新建自定义角色：标识归一成大写，权限按目录顺序重排。
	recorder = perform(router, http.MethodPost, "/api/admin/roles",
		`{"code":"auditor","name":"审计","description":"只读审计","permissions":["audit.view","dashboard.view"]}`, adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("新建角色失败：%d %s", recorder.Code, recorder.Body.String())
	}
	created := rbacDecode[rbacRolesData](t, recorder).Role
	if created.Code != "AUDITOR" || len(created.Permissions) != 2 ||
		created.Permissions[0] != auth.PermDashboardView || created.Permissions[1] != auth.PermAuditView {
		t.Fatalf("新建角色结果异常：%+v", created)
	}
	if created.Builtin {
		t.Fatalf("自定义角色不应带内置标记：%+v", created)
	}

	// 重复标识必须 409，未知权限点必须 400。
	if recorder = perform(router, http.MethodPost, "/api/admin/roles",
		`{"code":"AUDITOR","name":"另一个"}`,
		adminCookie); recorder.Code != http.StatusConflict {
		t.Fatalf("重复角色标识应返回 409，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder = perform(router, http.MethodPost, "/api/admin/roles",
		`{"code":"BAD_PERM","name":"坏权限","permissions":["not.a.permission"]}`,
		adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("未知权限点应返回 400，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 更新自定义角色。
	if recorder = perform(router, http.MethodPut, "/api/admin/roles/"+created.ID,
		`{"code":"AUDITOR","name":"审计员","permissions":["audit.view"]}`,
		adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("更新角色失败：%d %s", recorder.Code, recorder.Body.String())
	}

	// 内置角色：不可改标识、不可删除。
	if recorder = perform(router, http.MethodPut, "/api/admin/roles/"+builtinIDs[auth.RoleCodeOperator],
		`{"code":"OPS_REPLACED","name":"运营"}`,
		adminCookie); recorder.Code != http.StatusConflict {
		t.Fatalf("内置角色改标识应返回 409，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder = perform(router, http.MethodDelete, "/api/admin/roles/"+builtinIDs[auth.RoleCodeModerator], "", adminCookie); recorder.Code != http.StatusConflict {
		t.Fatalf("删除内置角色应返回 409，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 账号角色分配：查询 → 分配 → 未知角色被拒 → 被引用时不可删。
	if recorder = perform(router, http.MethodGet, "/api/admin/users/"+victimID+"/roles", "", adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("读取账号角色失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if recorder = perform(router, http.MethodPut, "/api/admin/users/"+victimID+"/roles",
		`{"roleCodes":["AUDITOR"]}`, adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("分配账号角色失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if assigned := rbacDecode[rbacUserRolesData](t, recorder); assigned.UserID != victimID ||
		len(assigned.RoleCodes) != 1 || assigned.RoleCodes[0] != "AUDITOR" {
		t.Fatalf("分配结果异常：%s", recorder.Body.String())
	}
	if recorder = perform(router, http.MethodPut, "/api/admin/users/"+victimID+"/roles",
		`{"roleCodes":["GHOST"]}`, adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("未知角色应返回 400，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder = perform(router, http.MethodDelete, "/api/admin/roles/"+created.ID, "", adminCookie); recorder.Code != http.StatusConflict {
		t.Fatalf("被引用的角色应拒绝删除，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 清空之后即可删除。
	if recorder = perform(router, http.MethodPut, "/api/admin/users/"+victimID+"/roles",
		`{"roleCodes":[]}`, adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("清空账号角色失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if recorder = perform(router, http.MethodDelete, "/api/admin/roles/"+created.ID, "", adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("删除自定义角色失败：%d %s", recorder.Code, recorder.Body.String())
	}

	// 写操作必须全部留痕。
	if recorder = perform(router, http.MethodGet, "/api/admin/audit-events", "", adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("审计列表失败：%d %s", recorder.Code, recorder.Body.String())
	}
	for _, action := range []string{"role.create", "role.update", "role.delete", "user.roles.update"} {
		if !strings.Contains(recorder.Body.String(), action) {
			t.Fatalf("审计缺少动作 %s：%s", action, recorder.Body.String())
		}
	}

	// 非管理员连 RBAC 列表都看不到。
	if recorder = perform(router, http.MethodGet, "/api/admin/roles", "", victimCookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("非管理员访问角色列表应返回 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
}

// TestHostedRbacPermissionGuard 单独验证 requirePermission 的四条分支。
func TestHostedRbacPermissionGuard(t *testing.T) {
	extension, router, authDB := newRbacAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "guard-admin@example.com")
	promoteToAdmin(t, authDB, adminID)
	userCookie, userID := registerAccount(t, router, authDB, "guard-user@example.com")

	// 未登录：会话解析失败，401。
	if recorder := perform(router, http.MethodGet, "/api/rbac-probe/roles", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录访问探针应返回 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	// 管理员旁路：不带任何角色也放行。
	if recorder := perform(router, http.MethodGet, "/api/rbac-probe/roles", "", adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("管理员应绕过细粒度权限，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	// 非管理员且无该权限：403。
	if recorder := perform(router, http.MethodGet, "/api/rbac-probe/roles", "", userCookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("无权限的普通账号应返回 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}

	// 授予一个含 rbac.manage 的自定义角色后，同一个账号必须通过。
	if recorder := perform(router, http.MethodPost, "/api/admin/roles",
		`{"code":"RBAC_OPS","name":"权限运营","permissions":["rbac.manage"]}`, adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("新建授权角色失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodPut, "/api/admin/users/"+userID+"/roles",
		`{"roleCodes":["RBAC_OPS"]}`, adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("分配授权角色失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodGet, "/api/rbac-probe/roles", "", userCookie); recorder.Code != http.StatusOK {
		t.Fatalf("持有该权限的账号应放行，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	// 权限被收回后立刻回到 403，不允许缓存旧判定。
	if recorder := perform(router, http.MethodPut, "/api/admin/users/"+userID+"/roles",
		`{"roleCodes":[]}`, adminCookie); recorder.Code != http.StatusOK {
		t.Fatalf("清空角色失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodGet, "/api/rbac-probe/roles", "", userCookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("收回权限后应返回 403，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
}
