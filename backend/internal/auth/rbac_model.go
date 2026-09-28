package auth

import (
	"encoding/json"
	"strings"
	"time"
)

// 角色与权限（RBAC）。
//
// 账号表里的 role 只有 USER/ADMIN 两个取值，它只回答"是不是管理员"这一件事；后台的
// 功能分区（用户、内容、计费、工单……）需要更细的授权粒度，因此单独建一组表：
//
//   - auth_roles 是角色定义，permissions 存 JSON 数组；
//   - auth_user_roles 是账号到角色的绑定。
//
// 绑定用 role_code 而不是角色 ID 做关联键：内置角色（SUPER_ADMIN 等）需要在 Prisma
// 迁移、代码常量与人工翻库三处保持同一个可读标识，随机 ID 会让这些场景全部退化。
// 账号本身不存角色码——一个人可以同时是运营和审核员，角色是集合而不是单值。

// 权限点目录（冻结）。
//
// code 是对外的稳定标识，会出现在角色配置、审计日志与前端路由里；中文名与分组只用于
// 后台展示，改文案不改变授权判定。新增权限点必须同时更新这里与
// requirePermission 的调用点，否则该权限点会成为"能勾选但没有任何作用"的摆设。
const (
	PermDashboardView   = "dashboard.view"
	PermUsersManage     = "users.manage"
	PermContentModerate = "content.moderate"
	PermBillingManage   = "billing.manage"
	PermRbacManage      = "rbac.manage"
	PermTicketsManage   = "tickets.manage"
	PermTemplatesManage = "templates.manage"
	PermAssetsManage    = "assets.manage"
	PermSettingsManage  = "settings.manage"
	PermAuditView       = "audit.view"
)

// 权限点分组：后台按分组做多选，分组名只影响展示。
const (
	permissionGroupOverview = "概览"
	permissionGroupAccess   = "账号与权限"
	permissionGroupContent  = "内容与素材"
	permissionGroupOps      = "运营"
)

// 内置角色标识。首次读取时幂等播种，builtin=true：不可删除、不可改 code。
//
// 必须能区分"系统预置"和"运营自建"：一次误删 SUPER_ADMIN 会让所有细粒度授权入口
// 同时失效，而这类角色在界面上和普通自定义角色长得一模一样。
const (
	RoleCodeSuperAdmin = "SUPER_ADMIN"
	RoleCodeOperator   = "OPERATOR"
	RoleCodeModerator  = "MODERATOR"
	RoleCodeSupport    = "SUPPORT"
)

// Role 是一个角色定义。
//
// Permissions 存 JSON 数组文本：SQLite 与 MySQL 都能原样保存，读出来的顺序就是配置
// 时的顺序（展示用），而授权判定只关心集合。
type Role struct {
	ID          string `gorm:"column:id;primaryKey;size:36"`
	Code        string `gorm:"column:code;size:64"`
	Name        string `gorm:"column:name;size:64"`
	Description string `gorm:"column:description;size:255"`
	Builtin     bool   `gorm:"column:builtin"`
	Permissions string `gorm:"column:permissions"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (Role) TableName() string { return "auth_roles" }

// UserRoleBinding 是账号到角色的绑定。
//
// 唯一索引落在 (user_id, role_code)：同一个人重复领取同一个角色没有意义，而缺了这条
// 约束，"清空再分配"在并发重放下会悄悄留下重复行，权限并集虽然不变，后台的人数统计
// 却会开始虚高。
type UserRoleBinding struct {
	ID        string `gorm:"column:id;primaryKey;size:36"`
	UserID    string `gorm:"column:user_id;size:36"`
	RoleCode  string `gorm:"column:role_code;size:64"`
	GrantedBy string `gorm:"column:granted_by;size:36"`
	CreatedAt time.Time
}

func (UserRoleBinding) TableName() string { return "auth_user_roles" }

// RbacModels 供 EnsureDevSchema / EnsureRbacSchema 建表使用。
//
// 生产库的结构由 CanvasMind 的 Prisma 迁移管理，这里返回的模型只在 sqlite 开发库上
// 落表（见 EnsureRbacSchema 的驱动白名单）。
func RbacModels() []any {
	return []any{
		&Role{},
		&UserRoleBinding{},
	}
}

// PermissionView 是权限点目录的一项。
type PermissionView struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Group string `json:"group"`
}

// rbacPermissionCatalog 是冻结的权限点目录，顺序即后台展示顺序。
var rbacPermissionCatalog = []PermissionView{
	{Code: PermDashboardView, Name: "仪表盘", Group: permissionGroupOverview},
	{Code: PermUsersManage, Name: "用户管理", Group: permissionGroupAccess},
	{Code: PermRbacManage, Name: "角色与权限", Group: permissionGroupAccess},
	{Code: PermAuditView, Name: "审计日志", Group: permissionGroupAccess},
	{Code: PermContentModerate, Name: "内容审核", Group: permissionGroupContent},
	{Code: PermAssetsManage, Name: "素材管理", Group: permissionGroupContent},
	{Code: PermTemplatesManage, Name: "模板管理", Group: permissionGroupContent},
	{Code: PermBillingManage, Name: "计费与订单", Group: permissionGroupOps},
	{Code: PermTicketsManage, Name: "工单与反馈", Group: permissionGroupOps},
	{Code: PermSettingsManage, Name: "站点设置", Group: permissionGroupOps},
}

// rbacBuiltinRole 描述一个内置角色的初始配置。
type rbacBuiltinRole struct {
	Code        string
	Name        string
	Description string
	Permissions []string
}

// rbacBuiltinRoles 是内置角色的权威定义。
//
// 播种只在缺失时补行，不覆盖已有配置：运营给 OPERATOR 加了"内容审核"之后重启服务，
// 权限不能被这段常量悄悄改回去。
var rbacBuiltinRoles = []rbacBuiltinRole{
	{
		Code:        RoleCodeSuperAdmin,
		Name:        "超级管理员",
		Description: "拥有后台全部权限；账号本身的 ADMIN 角色同样直接放行。",
		Permissions: allPermissionCodes(),
	},
	{
		Code:        RoleCodeOperator,
		Name:        "运营",
		Description: "日常运营：用户、订单、模板、素材与工单。",
		Permissions: []string{
			PermDashboardView, PermUsersManage, PermBillingManage, PermTemplatesManage,
			PermAssetsManage, PermTicketsManage, PermAuditView,
		},
	},
	{
		Code:        RoleCodeModerator,
		Name:        "审核员",
		Description: "内容审核与素材管理。",
		Permissions: []string{PermDashboardView, PermContentModerate, PermAssetsManage},
	},
	{
		Code:        RoleCodeSupport,
		Name:        "客服",
		Description: "工单处理与订单查询，不涉及内容与账号改配。",
		Permissions: []string{PermDashboardView, PermTicketsManage, PermBillingManage},
	},
}

// RoleView 是后台角色管理的读模型。
//
// 只有角色字段本身：这里的每一列都会原样进 JSON，任何凭据类字段（密码、令牌、渠道
// 密钥）都不允许出现在这一层。
type RoleView struct {
	ID          string    `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Builtin     bool      `json:"builtin"`
	Permissions []string  `json:"permissions"`
	UserCount   int64     `json:"userCount"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// UserRoleView 是一个账号当前的角色与权限并集。
type UserRoleView struct {
	UserID      string   `json:"userId"`
	RoleCodes   []string `json:"roleCodes"`
	Permissions []string `json:"permissions"`
}

// RoleInput 是后台保存角色的入参。ID 为空表示新建。
//
// 没有 Builtin 字段：内置标记只能由播种过程写入，不允许通过接口伪造或取消。
type RoleInput struct {
	ID          string   `json:"id"`
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

// allPermissionCodes 返回目录里的全部权限点（按目录顺序）。
func allPermissionCodes() []string {
	codes := make([]string, 0, len(rbacPermissionCatalog))
	for _, permission := range rbacPermissionCatalog {
		codes = append(codes, permission.Code)
	}
	return codes
}

// rbacPermissionIndex 返回权限点的存在性索引。
func rbacPermissionIndex() map[string]bool {
	index := make(map[string]bool, len(rbacPermissionCatalog))
	for _, permission := range rbacPermissionCatalog {
		index[permission.Code] = true
	}
	return index
}

// normalizeRoleCode 归一角色标识。
//
// 统一转大写再比较：内置角色是 SUPER_ADMIN 这种形态，如果允许 "operator" 与 "OPERATOR"
// 并存，唯一索引挡得住完全相同的字符串，却挡不住运营在两种写法之间反复创建"看起来
// 一样"的角色。
func normalizeRoleCode(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

// encodePermissions 把权限点序列化成落库的 JSON 文本。
//
// 空集合序列化成 "[]" 而不是 ""：前者是合法 JSON，读取端不必为"空字符串算不算空数组"
// 单独写一条分支。
func encodePermissions(codes []string) (string, error) {
	encoded, err := json.Marshal(orderedPermissions(codes))
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// decodePermissions 解析落库的权限点。
//
// 解析失败按"没有权限"处理而不是把错误抛给调用方：脏数据应该表现为权限收紧（用户被
// 明确拒绝），而不是让整个角色列表打不开。收紧的方向是安全的，放宽才是事故。
func decodePermissions(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var codes []string
	if err := json.Unmarshal([]byte(trimmed), &codes); err != nil {
		return nil
	}
	return codes
}

// orderedPermissions 去重并按下标目录的顺序重排权限点。
//
// 目录之外的 code 直接丢弃：它们不对应任何可判定的能力（requirePermission 只认目录里
// 的 code），留在列表里只会让后台显示一堆"看起来有权限、实际不生效"的项。
func orderedPermissions(codes []string) []string {
	selected := make(map[string]bool, len(codes))
	for _, raw := range codes {
		if code := strings.TrimSpace(raw); code != "" {
			selected[code] = true
		}
	}
	ordered := make([]string, 0, len(selected))
	for _, permission := range rbacPermissionCatalog {
		if selected[permission.Code] {
			ordered = append(ordered, permission.Code)
		}
	}
	return ordered
}

// nonNilStrings 把 nil 切片归一成空切片：JSON 里 [] 和 null 对前端是两种形状。
func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
