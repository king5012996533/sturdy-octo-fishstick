package auth

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

// 角色与权限的服务层。
//
// 这里只做"校验 + 编排"：表的读写与事务在 rbac_store.go 里，细粒度守卫在托管层的
// requirePermission 里。把校验集中在这一层，是为了让后台接口、内置播种与账号分配走
// 同一条路径——否则"后台存得进去、守卫却认不出来"这类不一致会反复出现。

// roleCodePattern 是角色标识的白名单。
//
// 归一成大写后只允许字母、数字、下划线与连字符：code 会进入唯一索引、审计日志与前端
// 展示，空格、冒号这类字符一旦放进来，同一个角色在不同系统里就会被写成多个"看起来
// 一样"的标识，而唯一索引只挡得住完全相同的字符串。
var roleCodePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{0,31}$`)

// PermissionCatalog 返回冻结的权限点目录。
//
// 返回副本：调用方（HTTP 层）不该改到包级常量，否则一次误写会影响同一进程里所有请求。
func (s *Service) PermissionCatalog() []PermissionView {
	catalog := make([]PermissionView, len(rbacPermissionCatalog))
	copy(catalog, rbacPermissionCatalog)
	return catalog
}

// AdminRoles 返回后台的完整角色列表（内置在前），带被引用账号数。
func (s *Service) AdminRoles() ([]RoleView, error) {
	if err := s.ensureRbacRoles(); err != nil {
		return nil, err
	}
	roles, err := s.store.Roles()
	if err != nil {
		return nil, internalFailure(err)
	}
	counts, err := s.store.CountUserRolesByCode()
	if err != nil {
		return nil, internalFailure(err)
	}
	views := make([]RoleView, 0, len(roles))
	for _, role := range roles {
		views = append(views, roleView(role, counts[role.Code]))
	}
	return views, nil
}

// SaveRole 新建或更新一个角色。
//
// 入参 ID 为空表示新建：后台只要传了 ID 就一律按"更新"处理，不会因为 ID 写错而悄悄
// 多出一个重复角色。
func (s *Service) SaveRole(input RoleInput) (*RoleView, error) {
	if err := s.ensureRbacRoles(); err != nil {
		return nil, err
	}
	code := normalizeRoleCode(input.Code)
	if !roleCodePattern.MatchString(code) {
		return nil, invalidArgument("角色标识只能由字母、数字、下划线和连字符组成，需以字母或数字开头且不超过 32 位")
	}
	name := strings.TrimSpace(input.Name)
	if count := utf8.RuneCountInString(name); count < 1 || count > 40 {
		return nil, invalidArgument("角色名称需为 1 到 40 个字符")
	}
	description := strings.TrimSpace(input.Description)
	if utf8.RuneCountInString(description) > 255 {
		return nil, invalidArgument("角色说明不能超过 255 个字符")
	}
	permissions, err := normalizeRolePermissions(input.Permissions)
	if err != nil {
		return nil, err
	}

	targetID := strings.TrimSpace(input.ID)
	existing := Role{}
	if targetID != "" {
		record, err := s.store.RoleByID(targetID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				// 包成 404 而不是裸 ErrNotFound：HTTP 层只认 *Error，裸错误会落到 500，
				// "角色被删掉了"在后台会显示成"系统处理失败"。
				return nil, notFound("角色不存在")
			}
			return nil, internalFailure(err)
		}
		existing = *record
		// 内置角色的 code 是它在迁移、审计与守卫之间的关联键，改了之后历史留痕就对不上。
		if existing.Builtin && existing.Code != code {
			return nil, conflict("内置角色不可修改标识")
		}
	}

	// code 冲突单独查一次并给出明确提示，而不是把唯一索引的报错翻译成 500。
	conflicting, err := s.store.RoleByCode(code)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, internalFailure(err)
	}
	if err == nil && conflicting.ID != targetID {
		return nil, conflict("角色标识已被占用")
	}

	encoded, err := encodePermissions(permissions)
	if err != nil {
		return nil, internalFailure(err)
	}
	role := Role{
		ID:          targetID,
		Code:        code,
		Name:        name,
		Description: description,
		Builtin:     existing.Builtin,
		Permissions: encoded,
		CreatedAt:   existing.CreatedAt,
	}
	if err := s.store.SaveRole(&role); err != nil {
		return nil, internalFailure(err)
	}
	assigned, err := s.store.CountUserRolesByRoleCode(role.Code)
	if err != nil {
		return nil, internalFailure(err)
	}
	view := roleView(role, assigned)
	return &view, nil
}

// DeleteRole 删除一个自定义角色。
//
// 内置角色永远不可删除；仍被账号引用的角色也要先解除分配——直接删掉会让那些账号在
// 下一次权限判定时静默失去权限，而"谁少了哪一项"在界面上看不出任何线索。
func (s *Service) DeleteRole(id string) error {
	if err := s.ensureRbacRoles(); err != nil {
		return err
	}
	role, err := s.store.RoleByID(strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return notFound("角色不存在")
		}
		return internalFailure(err)
	}
	if role.Builtin {
		return conflict("内置角色不可删除")
	}
	assigned, err := s.store.CountUserRolesByRoleCode(role.Code)
	if err != nil {
		return internalFailure(err)
	}
	if assigned > 0 {
		return conflict("该角色仍分配给账号，请先解除")
	}
	if err := s.store.DeleteRole(role.ID); err != nil {
		return internalFailure(err)
	}
	return nil
}

// UserRoleViewFor 返回一个账号的角色与权限并集。
func (s *Service) UserRoleViewFor(userID string) (*UserRoleView, error) {
	target := strings.TrimSpace(userID)
	if target == "" {
		return nil, invalidArgument("缺少账号标识")
	}
	if err := s.ensureRbacRoles(); err != nil {
		return nil, err
	}
	if _, err := s.store.UserByID(target); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, notFound("账号不存在")
		}
		return nil, internalFailure(err)
	}
	return s.userRoleView(target)
}

// AssignUserRoles 覆盖式分配账号角色。
//
// 入参是完整集合而不是增量：后台的勾选框提交的就是"最终应该有哪些角色"，用增量语义
// 会让"取消勾选"变成一次额外请求，界面与数据之间多一处可以对不上的状态。空数组表示
// 清空全部角色。
func (s *Service) AssignUserRoles(userID string, codes []string, actorID string) (*UserRoleView, error) {
	target := strings.TrimSpace(userID)
	if target == "" {
		return nil, invalidArgument("缺少账号标识")
	}
	if err := s.ensureRbacRoles(); err != nil {
		return nil, err
	}
	if _, err := s.store.UserByID(target); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, notFound("账号不存在")
		}
		return nil, internalFailure(err)
	}

	normalized := make([]string, 0, len(codes))
	seen := make(map[string]bool, len(codes))
	for _, raw := range codes {
		code := normalizeRoleCode(raw)
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		normalized = append(normalized, code)
	}
	if len(normalized) > 0 {
		// 逐条确认角色存在，而不是把未知 code 写进绑定表：绑到不存在的角色不会报错，
		// 只会让这个账号的权限并集长期少一块，而且从后台看不出来。
		roles, err := s.store.RolesByCodes(normalized)
		if err != nil {
			return nil, internalFailure(err)
		}
		known := make(map[string]bool, len(roles))
		for _, role := range roles {
			known[role.Code] = true
		}
		for _, code := range normalized {
			if !known[code] {
				return nil, invalidArgument("存在未知的角色标识")
			}
		}
	}
	if err := s.store.ReplaceUserRoles(target, normalized, actorID); err != nil {
		return nil, internalFailure(err)
	}
	return s.userRoleView(target)
}

// EffectivePermissions 返回一个账号的权限并集，供 requirePermission 判定使用。
//
// 没有分配任何角色不是错误，而是"这个账号没有后台权限"的正常状态：返回空集合让守卫
// 直接 403，把它当成故障会让一次误配置表现为 500，更难排查。
func (s *Service) EffectivePermissions(userID string) ([]string, error) {
	target := strings.TrimSpace(userID)
	if target == "" {
		return []string{}, nil
	}
	if err := s.ensureRbacRoles(); err != nil {
		return nil, err
	}
	view, err := s.userRoleView(target)
	if err != nil {
		return nil, err
	}
	return view.Permissions, nil
}

// ensureRbacRoles 幂等播种内置角色：只在缺失时补行，不覆盖已有配置。
func (s *Service) ensureRbacRoles() error {
	roles := make([]Role, 0, len(rbacBuiltinRoles))
	for _, definition := range rbacBuiltinRoles {
		encoded, err := encodePermissions(definition.Permissions)
		if err != nil {
			return internalFailure(err)
		}
		roles = append(roles, Role{
			Code:        definition.Code,
			Name:        definition.Name,
			Description: definition.Description,
			Builtin:     true,
			Permissions: encoded,
		})
	}
	if err := s.store.EnsureRoles(roles); err != nil {
		return internalFailure(err)
	}
	return nil
}

// userRoleView 组装账号的角色码与权限并集，调用方负责确认账号存在。
func (s *Service) userRoleView(userID string) (*UserRoleView, error) {
	records, err := s.store.UserRolesByUserID(userID)
	if err != nil {
		return nil, internalFailure(err)
	}
	codes := make([]string, 0, len(records))
	for _, record := range records {
		codes = append(codes, record.RoleCode)
	}
	roles, err := s.store.RolesByCodes(codes)
	if err != nil {
		return nil, internalFailure(err)
	}
	collected := make([]string, 0, len(roles)*2)
	for _, role := range roles {
		collected = append(collected, decodePermissions(role.Permissions)...)
	}
	return &UserRoleView{
		UserID:      userID,
		RoleCodes:   nonNilStrings(codes),
		Permissions: nonNilStrings(orderedPermissions(collected)),
	}, nil
}

// normalizeRolePermissions 校验并归一权限点集合。
func normalizeRolePermissions(codes []string) ([]string, error) {
	known := rbacPermissionIndex()
	for _, raw := range codes {
		code := strings.TrimSpace(raw)
		if code == "" {
			continue
		}
		if !known[code] {
			return nil, invalidArgument("存在未知的权限点：" + code)
		}
	}
	return orderedPermissions(codes), nil
}

// roleView 把存储模型投影成读模型。
func roleView(role Role, userCount int64) RoleView {
	return RoleView{
		ID:          role.ID,
		Code:        role.Code,
		Name:        role.Name,
		Description: role.Description,
		Builtin:     role.Builtin,
		Permissions: nonNilStrings(orderedPermissions(decodePermissions(role.Permissions))),
		UserCount:   userCount,
		CreatedAt:   role.CreatedAt,
		UpdatedAt:   role.UpdatedAt,
	}
}
