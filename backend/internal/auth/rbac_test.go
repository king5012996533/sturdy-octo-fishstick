package auth

import (
	"slices"
	"strings"
	"testing"
)

// newRbacTestService 复用计费用例的 SQLite 装配，再补建 RBAC 表。
//
// 建表在这里显式调用 EnsureRbacSchema，而不是指望 EnsureDevSchema 已经带上 RBAC
// 模型：建表注册由合并方负责，本模块的用例必须能在"还没接线"的状态下独立跑绿。
func newRbacTestService(t *testing.T) *billingTestEnv {
	t.Helper()
	env := newBillingTestService(t)
	if err := EnsureRbacSchema(env.store.db); err != nil {
		t.Fatalf("初始化 RBAC 表失败: %v", err)
	}
	return env
}

func seedRbacUser(t *testing.T, env *billingTestEnv, id string) {
	t.Helper()
	email := id + "@example.com"
	name := "测试账号" + id
	if err := env.store.CreateUser(&User{ID: id, Email: &email, Name: &name}); err != nil {
		t.Fatalf("创建测试账号失败: %v", err)
	}
}

// rbacRoleView 从后台列表里取指定角色，取不到直接失败。
func rbacRoleView(t *testing.T, env *billingTestEnv, code string) RoleView {
	t.Helper()
	roles, err := env.service.AdminRoles()
	if err != nil {
		t.Fatalf("读取角色列表失败: %v", err)
	}
	for _, role := range roles {
		if role.Code == code {
			return role
		}
	}
	t.Fatalf("角色列表缺少 %s", code)
	return RoleView{}
}

// TestRbacBuiltinRolesSeeded 覆盖内置角色的幂等播种与权限并集。
func TestRbacBuiltinRolesSeeded(t *testing.T) {
	env := newRbacTestService(t)

	roles, err := env.service.AdminRoles()
	if err != nil {
		t.Fatalf("读取角色列表失败: %v", err)
	}
	if len(roles) != 4 {
		t.Fatalf("首次读取应播种 4 个内置角色，实际 %d 个", len(roles))
	}
	for _, role := range roles {
		if !role.Builtin {
			t.Fatalf("内置角色 %s 的 builtin 标记丢失", role.Code)
		}
	}

	// SUPER_ADMIN 必须覆盖目录里的全部权限点。
	super := rbacRoleView(t, env, RoleCodeSuperAdmin)
	if !slices.Equal(super.Permissions, allPermissionCodes()) {
		t.Fatalf("超级管理员权限不完整：%v", super.Permissions)
	}

	// 目录冻结：code 与中文名都不得漂移。
	catalog := env.service.PermissionCatalog()
	if len(catalog) != 10 {
		t.Fatalf("权限点目录应为 10 项，实际 %d 项", len(catalog))
	}
	expectedNames := map[string]string{
		PermDashboardView:   "仪表盘",
		PermUsersManage:     "用户管理",
		PermContentModerate: "内容审核",
		PermBillingManage:   "计费与订单",
		PermRbacManage:      "角色与权限",
		PermTicketsManage:   "工单与反馈",
		PermTemplatesManage: "模板管理",
		PermAssetsManage:    "素材管理",
		PermSettingsManage:  "站点设置",
		PermAuditView:       "审计日志",
	}
	for _, permission := range catalog {
		if expectedNames[permission.Code] != permission.Name {
			t.Fatalf("权限点 %s 的中文名漂移：%q", permission.Code, permission.Name)
		}
		if strings.TrimSpace(permission.Group) == "" {
			t.Fatalf("权限点 %s 缺少分组，前端无法分组展示", permission.Code)
		}
	}

	// 再读一次不得重复播种。
	if roles, err = env.service.AdminRoles(); err != nil || len(roles) != 4 {
		t.Fatalf("重复读取应保持 4 个角色，实际 %d 个（err=%v）", len(roles), err)
	}
}

// TestSaveRoleRejectsUnknownPermission 确认未知权限点被挡在写库之前。
func TestSaveRoleRejectsUnknownPermission(t *testing.T) {
	env := newRbacTestService(t)

	_, err := env.service.SaveRole(RoleInput{
		Code:        "CUSTOM",
		Name:        "自定义角色",
		Permissions: []string{PermDashboardView, "not.a.permission"},
	})
	assertBillingError(t, err, 400, "")

	// 非法标识同样要在写库前被拒。
	_, err = env.service.SaveRole(RoleInput{Code: "bad code", Name: "非法标识"})
	assertBillingError(t, err, 400, "")

	// 拒绝之后不能留下半条记录。
	roles, err := env.service.AdminRoles()
	if err != nil {
		t.Fatalf("读取角色列表失败: %v", err)
	}
	for _, role := range roles {
		if role.Code == "CUSTOM" {
			t.Fatalf("非法权限点被部分写入：%+v", role)
		}
	}
}

// TestSaveRoleCodeConflict 覆盖标识归一、权限重排与重复标识的冲突。
func TestSaveRoleCodeConflict(t *testing.T) {
	env := newRbacTestService(t)

	created, err := env.service.SaveRole(RoleInput{
		Code:        "auditor",
		Name:        "审计",
		Permissions: []string{PermAuditView, PermDashboardView},
	})
	if err != nil {
		t.Fatalf("新建角色失败: %v", err)
	}
	if created.Code != "AUDITOR" {
		t.Fatalf("角色标识应归一成大写，实际 %q", created.Code)
	}
	// 权限按目录顺序重排：展示顺序稳定，前端不必再排一次。
	if !slices.Equal(created.Permissions, []string{PermDashboardView, PermAuditView}) {
		t.Fatalf("权限未按目录顺序重排：%v", created.Permissions)
	}

	// 大小写不同但归一后相同的标识必须判为冲突。
	_, err = env.service.SaveRole(RoleInput{Code: "Auditor", Name: "另一个"})
	assertBillingError(t, err, 409, "角色标识已被占用")

	// 更新自己沿用原标识必须放行。
	updated, err := env.service.SaveRole(RoleInput{
		ID:          created.ID,
		Code:        "AUDITOR",
		Name:        "审计员",
		Permissions: []string{PermAuditView},
	})
	if err != nil {
		t.Fatalf("更新角色失败: %v", err)
	}
	if updated.ID != created.ID || updated.Name != "审计员" {
		t.Fatalf("更新结果异常：%+v", updated)
	}
	if updated.Builtin {
		t.Fatalf("自定义角色不应带内置标记：%+v", updated)
	}

	// 改成别的角色已占用的标识同样冲突。
	_, err = env.service.SaveRole(RoleInput{ID: created.ID, Code: RoleCodeOperator, Name: "审计员"})
	assertBillingError(t, err, 409, "角色标识已被占用")
}

// TestBuiltinRoleCannotBeDeletedOrRecoded 覆盖内置角色的两条硬边界。
func TestBuiltinRoleCannotBeDeletedOrRecoded(t *testing.T) {
	env := newRbacTestService(t)
	operator := rbacRoleView(t, env, RoleCodeOperator)

	assertBillingError(t, env.service.DeleteRole(operator.ID), 409, "内置角色不可删除")

	_, err := env.service.SaveRole(RoleInput{
		ID:          operator.ID,
		Code:        "OPS",
		Name:        operator.Name,
		Permissions: operator.Permissions,
	})
	assertBillingError(t, err, 409, "内置角色不可修改标识")

	// 契约只冻结 code：改名与调整权限必须仍然可行，否则内置角色无法随业务演进。
	updated, err := env.service.SaveRole(RoleInput{
		ID:          operator.ID,
		Code:        RoleCodeOperator,
		Name:        "运营组",
		Permissions: []string{PermDashboardView},
	})
	if err != nil {
		t.Fatalf("内置角色改名失败: %v", err)
	}
	if updated.Name != "运营组" || !updated.Builtin {
		t.Fatalf("内置角色更新结果异常：%+v", updated)
	}
}

// TestDeleteRoleBlockedWhileAssigned 确认被引用的角色不能直接删除。
func TestDeleteRoleBlockedWhileAssigned(t *testing.T) {
	env := newRbacTestService(t)
	seedRbacUser(t, env, "user-delete")

	created, err := env.service.SaveRole(RoleInput{Code: "TEMP", Name: "临时角色"})
	if err != nil {
		t.Fatalf("新建角色失败: %v", err)
	}
	if _, err := env.service.AssignUserRoles("user-delete", []string{"TEMP"}, "admin-1"); err != nil {
		t.Fatalf("分配角色失败: %v", err)
	}
	assertBillingError(t, env.service.DeleteRole(created.ID), 409, "该角色仍分配给账号，请先解除")

	// 解除引用之后即可删除。
	if _, err := env.service.AssignUserRoles("user-delete", []string{}, "admin-1"); err != nil {
		t.Fatalf("清空角色失败: %v", err)
	}
	if err := env.service.DeleteRole(created.ID); err != nil {
		t.Fatalf("解除引用后删除应成功: %v", err)
	}
	// 删除不存在的角色应给 404 而不是 500。
	assertBillingError(t, env.service.DeleteRole("missing-role"), 404, "角色不存在")
}

// TestAssignUserRolesPermissionUnion 覆盖分配、并集与清空。
func TestAssignUserRolesPermissionUnion(t *testing.T) {
	env := newRbacTestService(t)
	seedRbacUser(t, env, "user-assign")

	// 未知角色必须在写库前被拒。
	_, err := env.service.AssignUserRoles("user-assign", []string{"OPERATOR", "GHOST"}, "admin-1")
	assertBillingError(t, err, 400, "存在未知的角色标识")
	// 未知账号不能留下孤儿绑定。
	_, err = env.service.AssignUserRoles("ghost-user", []string{"OPERATOR"}, "admin-1")
	assertBillingError(t, err, 404, "账号不存在")

	view, err := env.service.AssignUserRoles("user-assign", []string{"operator", "MODERATOR"}, "admin-1")
	if err != nil {
		t.Fatalf("分配角色失败: %v", err)
	}
	// 归一成大写，并按 (分配时间, 角色标识) 排序：同一事务写入的两条绑定时间相同，
	// 必须退到 role_code 才有确定次序（MODERATOR < OPERATOR）。
	if !slices.Equal(view.RoleCodes, []string{RoleCodeModerator, RoleCodeOperator}) {
		t.Fatalf("角色码归一或排序异常：%v", view.RoleCodes)
	}
	// 运营 ∪ 审核员，按目录顺序去重。
	expected := []string{
		PermDashboardView, PermUsersManage, PermAuditView, PermContentModerate,
		PermAssetsManage, PermTemplatesManage, PermBillingManage, PermTicketsManage,
	}
	if !slices.Equal(view.Permissions, expected) {
		t.Fatalf("权限并集计算错误：%v", view.Permissions)
	}

	permissions, err := env.service.EffectivePermissions("user-assign")
	if err != nil {
		t.Fatalf("读取有效权限失败: %v", err)
	}
	if !slices.Equal(permissions, expected) {
		t.Fatalf("EffectivePermissions 与分配结果不一致：%v", permissions)
	}

	// 空数组表示清空。
	view, err = env.service.AssignUserRoles("user-assign", []string{}, "admin-1")
	if err != nil {
		t.Fatalf("清空角色失败: %v", err)
	}
	if view.RoleCodes == nil || len(view.RoleCodes) != 0 || len(view.Permissions) != 0 {
		t.Fatalf("清空后应为非 nil 空集合：%+v", view)
	}
	permissions, err = env.service.EffectivePermissions("user-assign")
	if err != nil {
		t.Fatalf("读取有效权限失败: %v", err)
	}
	if len(permissions) != 0 {
		t.Fatalf("清空后不应残留权限：%v", permissions)
	}
}

// TestUserRoleViewForUnknownAccount 覆盖账号不存在与未分配两种状态。
func TestUserRoleViewForUnknownAccount(t *testing.T) {
	env := newRbacTestService(t)

	_, err := env.service.UserRoleViewFor("nobody")
	assertBillingError(t, err, 404, "账号不存在")

	seedRbacUser(t, env, "user-view")
	view, err := env.service.UserRoleViewFor("user-view")
	if err != nil {
		t.Fatalf("读取账号角色失败: %v", err)
	}
	// 未分配角色是正常状态：必须返回可序列化成 [] 的空集合，而不是 null 或报错。
	if view.RoleCodes == nil || view.Permissions == nil || len(view.RoleCodes) != 0 || len(view.Permissions) != 0 {
		t.Fatalf("未分配角色的账号应返回空集合：%+v", view)
	}
}
