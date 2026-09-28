package auth

import (
	"strings"
	"time"

	"gorm.io/gorm"
)

// AdminUserFilter 描述管理端用户列表的筛选条件。
type AdminUserFilter struct {
	Keyword string
	Status  string
	Role    string
	Page    int
	Limit   int
}

// adminUserQuery 构造带筛选的查询。
//
// 列表要跑两次（count 与 find）：把 where 挂在上一个 *gorm.DB 上复用，第二次查询
// 会带上第一次的执行状态，所以每次重新构造。
func (s *Store) adminUserQuery(filter AdminUserFilter) *gorm.DB {
	query := s.db.Model(&User{})
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("username LIKE ? OR name LIKE ? OR email LIKE ? OR phone LIKE ? OR id = ?", like, like, like, like, keyword)
	}
	if status := strings.TrimSpace(filter.Status); status != "" {
		query = query.Where("status = ?", status)
	}
	if role := strings.TrimSpace(filter.Role); role != "" {
		query = query.Where("role = ?", role)
	}
	return query
}

// AdminUserPage 返回分页后的账号列表与总数。
func (s *Store) AdminUserPage(filter AdminUserFilter) ([]User, int64, error) {
	var total int64
	if err := s.adminUserQuery(filter).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var users []User
	query := s.adminUserQuery(filter).Order("created_at DESC, id DESC")
	if filter.Limit > 0 {
		query = query.Limit(filter.Limit).Offset((filter.Page - 1) * filter.Limit)
	}
	if err := query.Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// AdminUserCounts 按口径汇总账号数，供仪表盘使用。
func (s *Store) AdminUserCounts(since time.Time) (AdminUserCounts, error) {
	var counts AdminUserCounts
	if err := s.db.Model(&User{}).Count(&counts.Total).Error; err != nil {
		return counts, err
	}
	if err := s.db.Model(&User{}).Where("status = ?", StatusActive).Count(&counts.Active).Error; err != nil {
		return counts, err
	}
	if err := s.db.Model(&User{}).Where("status = ?", StatusDisabled).Count(&counts.Disabled).Error; err != nil {
		return counts, err
	}
	if err := s.db.Model(&User{}).Where("role = ?", RoleAdmin).Count(&counts.Admins).Error; err != nil {
		return counts, err
	}
	if err := s.db.Model(&User{}).Where("created_at >= ?", since).Count(&counts.NewUsers).Error; err != nil {
		return counts, err
	}
	return counts, nil
}

// UpdateUserStatus 改写账号状态（封禁/解封）。
func (s *Store) UpdateUserStatus(userID string, status UserStatus, now time.Time) error {
	result := s.db.Model(&User{}).Where("id = ?", userID).Updates(map[string]any{"status": status, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateUserRole 改写账号角色（管理员标记）。
func (s *Store) UpdateUserRole(userID string, role UserRole, now time.Time) error {
	result := s.db.Model(&User{}).Where("id = ?", userID).Updates(map[string]any{"role": role, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateUserPassword 覆盖密码哈希。
//
// 只写 password_hash：账号库的其余列由 CanvasMind 的 Prisma 迁移维护，
// 这里改结构会撞上两侧的迁移归属。
func (s *Store) UpdateUserPassword(userID string, passwordHash string, now time.Time) error {
	result := s.db.Model(&User{}).Where("id = ?", userID).Updates(map[string]any{"password_hash": passwordHash, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ActiveSessionCount 统计账号当前未过期的会话数。
func (s *Store) ActiveSessionCount(userID string, now time.Time) (int64, error) {
	var count int64
	err := s.db.Model(&Session{}).Where("user_id = ? AND revoked_at IS NULL AND expires_at > ?", userID, now).Count(&count).Error
	return count, err
}

// LastActiveAtByUser 返回每个账号最近一次会话活动时间。
//
// 不用 MAX(last_active_at) 聚合：SQLite 会把聚合结果当字符串返回，扫进
// time.Time 直接报错（MySQL 则返回 DATETIME），一条语句在两种驱动下语义不同。
// 这里按行取回后在 Go 侧收敛，代价是每页最多"账号数 × 会话数"行，量级可控。
func (s *Store) LastActiveAtByUser(userIDs []string) (map[string]time.Time, error) {
	result := make(map[string]time.Time, len(userIDs))
	if len(userIDs) == 0 {
		return result, nil
	}
	type row struct {
		UserID       string
		LastActiveAt *time.Time
	}
	var rows []row
	err := s.db.Model(&Session{}).
		Select("user_id, last_active_at").
		Where("user_id IN ? AND revoked_at IS NULL", userIDs).
		Order("last_active_at DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, item := range rows {
		if item.LastActiveAt == nil {
			continue
		}
		if existing, ok := result[item.UserID]; ok && !item.LastActiveAt.After(existing) {
			continue
		}
		result[item.UserID] = *item.LastActiveAt
	}
	return result, nil
}

// RevokeAllSessions 吊销账号的全部有效会话并返回被吊销的数量。
//
// 封禁与"强制登出"都要回显"踢掉了几台设备"，所以这里返回行数；原有的
// RevokeUserSessions 保持 error-only，继续服务改密流程。
func (s *Store) RevokeAllSessions(userID string, at time.Time) (int64, error) {
	result := s.db.Model(&Session{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", at)
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

// AdminMethodConfigs 返回全部登录方式配置，含被禁用与不可见的行。
//
// 与 EnabledMethods 的区别是"管理视角"：后台要能看到并打开一条当前禁用的通道，
// 因此不能复用只返回启用项的查询。
func (s *Store) AdminMethodConfigs() ([]MethodConfig, error) {
	var configs []MethodConfig
	if err := s.db.Order("sort_order asc, method_type asc").Find(&configs).Error; err != nil {
		return nil, err
	}
	return configs, nil
}

// UpdateMethodConfig 按登录方式改写配置列。
//
// 只允许调用方传入白名单列（由 service 组装），避免把管理接口变成"任意列写入"。
func (s *Store) UpdateMethodConfig(methodType MethodType, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	fields["updated_at"] = time.Now()
	result := s.db.Model(&MethodConfig{}).Where("method_type = ?", string(methodType)).Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// CountEnabledMethodConfigs 统计启用中的登录方式数量。
//
// 关闭最后一条通道等于把所有人都挡在门外（包括管理员自己下次登录），因此这里
// 提供一个不算太贵的兜底检查。
func (s *Store) CountEnabledMethodConfigs(exclude MethodType) (int64, error) {
	var count int64
	query := s.db.Model(&MethodConfig{}).Where("is_enabled = ?", true)
	if exclude != "" {
		query = query.Where("method_type <> ?", string(exclude))
	}
	err := query.Count(&count).Error
	return count, err
}

// UserAgreements 返回一个账号签过的协议留痕，按时间倒序。
//
// 只读：留痕一旦写入就不可修改，"用户在哪个版本上点的同意"必须能被原样举证。
func (s *Store) UserAgreements(userID string, limit int) ([]UserAgreement, error) {
	var records []UserAgreement
	query := s.db.Where("user_id = ?", userID).Order("accepted_at DESC, id DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

// UsersByIDs 批量读取账号，用于把画布/调用记录上的 owner id 换成人能读的标识。
func (s *Store) UsersByIDs(ids []string) ([]User, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var users []User
	if err := s.db.Where("id IN ?", ids).Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}
