package auth

import (
	"errors"
	"fmt"
	"strings"

	"infinite-canvas/backend/internal/kernel"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RBAC 的读写层。
//
// 只做"查/写 + 事务边界"，不含业务策略：哪些角色不可删、权限点是否合法、清空与并集
// 怎么算，全部在服务层。写在这里会让后台接口与 requirePermission 各绕开一遍校验。

// EnsureRbacSchema 仅在本地 SQLite 上创建 RBAC 表。
//
// 与 EnsureDevSchema 相同的驱动白名单：生产库的表结构由 CanvasMind 的 Prisma 迁移
// 管理，让 GORM 按自己的类型推断碰到共享库，可能改写真实 DDL。
func EnsureRbacSchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("auth: 数据库连接为空")
	}
	if name := db.Dialector.Name(); name != "sqlite" {
		return fmt.Errorf("auth: 拒绝为 %s 驱动创建 RBAC 表；该库的表结构由 Prisma 迁移管理", name)
	}
	if err := db.AutoMigrate(RbacModels()...); err != nil {
		return err
	}
	// 索引用幂等 SQL 显式声明，理由与 EnsureDevSchema 一致：这些表归 Prisma 迁移所有，
	// 结构体标签一旦和真实 DDL 漂移，宿主就会误以为结构由自己控制。
	//
	// code 的唯一索引决定"角色标识是否可能重复"，是新增与改名时的并发兜底；
	// (user_id, role_code) 的唯一索引决定重复分配不会留下重复行。
	statements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_auth_roles_code ON auth_roles (code)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_auth_user_roles_user_role ON auth_user_roles (user_id, role_code)`,
		`CREATE INDEX IF NOT EXISTS idx_auth_user_roles_role_code ON auth_user_roles (role_code)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("auth: 创建 RBAC 索引失败（%s）: %w", statement, err)
		}
	}
	return nil
}

// Roles 返回全部角色：内置在前，自定义在后，同组内按建立时间排。
func (s *Store) Roles() ([]Role, error) {
	var roles []Role
	if err := s.db.Order("builtin DESC, created_at ASC, code ASC").Find(&roles).Error; err != nil {
		return nil, err
	}
	return roles, nil
}

// RoleByID 按主键读取角色。
func (s *Store) RoleByID(id string) (*Role, error) {
	var role Role
	err := s.db.Where("id = ?", strings.TrimSpace(id)).First(&role).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &role, nil
}

// RoleByCode 按标识读取角色。
func (s *Store) RoleByCode(code string) (*Role, error) {
	var role Role
	err := s.db.Where("code = ?", normalizeRoleCode(code)).First(&role).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &role, nil
}

// RolesByCodes 批量按标识读取角色，空入参直接返回空切片。
func (s *Store) RolesByCodes(codes []string) ([]Role, error) {
	normalized := make([]string, 0, len(codes))
	for _, code := range codes {
		if code := normalizeRoleCode(code); code != "" {
			normalized = append(normalized, code)
		}
	}
	if len(normalized) == 0 {
		return []Role{}, nil
	}
	var roles []Role
	if err := s.db.Where("code IN ?", normalized).Find(&roles).Error; err != nil {
		return nil, err
	}
	return roles, nil
}

// SaveRole 新建或更新一个角色。
//
// builtin 不参与 OnConflict 更新：内置标记由播种写入，一旦允许被保存覆盖，任何一次
// 编辑都能把 SUPER_ADMIN 降级成可删除的普通角色。
func (s *Store) SaveRole(role *Role) error {
	if role == nil {
		return errors.New("auth: 角色为空")
	}
	now := s.clock()
	if role.ID == "" {
		role.ID = kernel.NewID()
	}
	if role.CreatedAt.IsZero() {
		role.CreatedAt = now
	}
	role.UpdatedAt = now
	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"code", "name", "description", "permissions", "updated_at",
		}),
	}).Create(role).Error
}

// DeleteRole 删除角色。是否允许删除由服务层判定。
func (s *Store) DeleteRole(id string) error {
	return s.db.Where("id = ?", strings.TrimSpace(id)).Delete(&Role{}).Error
}

// EnsureRoles 幂等写入内置角色：只补缺失的行，不覆盖已有配置。
//
// 冲突时 DoNothing 而不是报错：两个请求同时冷启动会并发播种，唯一索引会把其中一个
// 挡下来；那是"角色已经在了"的正常结果，不该升级成 500。
func (s *Store) EnsureRoles(roles []Role) error {
	if len(roles) == 0 {
		return nil
	}
	now := s.clock()
	for _, role := range roles {
		record := role
		if normalizeRoleCode(record.Code) == "" {
			continue
		}
		record.Code = normalizeRoleCode(record.Code)
		if record.ID == "" {
			record.ID = kernel.NewID()
		}
		if record.CreatedAt.IsZero() {
			record.CreatedAt = now
		}
		record.UpdatedAt = now
		if err := s.db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "code"}},
			DoNothing: true,
		}).Create(&record).Error; err != nil {
			return err
		}
	}
	return nil
}

// UserRolesByUserID 返回某账号的角色绑定，按分配时间排序。
//
// 同一时间写入的多条绑定（一次分配多个角色就在同一个事务里）必须有确定的次序，
// 否则并列的 created_at 会退化成按随机主键排，同一个账号两次读到的角色顺序都可能
// 不同。这里用 role_code 兜底，读出来的顺序因此是可断言的。
func (s *Store) UserRolesByUserID(userID string) ([]UserRoleBinding, error) {
	var records []UserRoleBinding
	if err := s.db.Where("user_id = ?", strings.TrimSpace(userID)).
		Order("created_at ASC, role_code ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

// CountUserRolesByCode 统计每个角色被多少个账号引用，供列表与删除前提示使用。
//
// 一次分组查询取回全部计数：后台列表要显示"被引用数"，逐行 count 会让角色列表变成
// N+1 次查询。
func (s *Store) CountUserRolesByCode() (map[string]int64, error) {
	type row struct {
		RoleCode string
		Total    int64
	}
	var rows []row
	if err := s.db.Model(&UserRoleBinding{}).
		Select("role_code AS role_code, COUNT(*) AS total").
		Group("role_code").Scan(&rows).Error; err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(rows))
	for _, record := range rows {
		counts[record.RoleCode] = record.Total
	}
	return counts, nil
}

// CountUserRolesByRoleCode 统计单个角色被多少个账号引用。
func (s *Store) CountUserRolesByRoleCode(code string) (int64, error) {
	var total int64
	if err := s.db.Model(&UserRoleBinding{}).
		Where("role_code = ?", normalizeRoleCode(code)).Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

// ReplaceUserRoles 把某账号的角色集合替换成 codes。
//
// 逐条增删而不是"全删再全插"：保留未变化的绑定，等于保留"这个角色是从什么时候开始
// 给的"这条信息；全删再插会让每次分配都刷新 created_at，事后追查授权时间就失去依据。
// 整个替换在一个事务里完成：中途失败留下"删了旧的、新的没建上"的空档，会让账号在
// 几毫秒内失去全部后台权限。
func (s *Store) ReplaceUserRoles(userID string, codes []string, actorID string) error {
	target := strings.TrimSpace(userID)
	desired := make(map[string]bool, len(codes))
	for _, code := range codes {
		if normalized := normalizeRoleCode(code); normalized != "" {
			desired[normalized] = true
		}
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var existing []UserRoleBinding
		if err := tx.Where("user_id = ?", target).Find(&existing).Error; err != nil {
			return err
		}
		keep := make(map[string]bool, len(existing))
		for _, record := range existing {
			if desired[record.RoleCode] {
				keep[record.RoleCode] = true
				continue
			}
			if err := tx.Where("id = ?", record.ID).Delete(&UserRoleBinding{}).Error; err != nil {
				return err
			}
		}
		now := s.clock()
		for code := range desired {
			if keep[code] {
				continue
			}
			record := UserRoleBinding{
				ID:        kernel.NewID(),
				UserID:    target,
				RoleCode:  code,
				GrantedBy: strings.TrimSpace(actorID),
				CreatedAt: now,
			}
			if err := tx.Create(&record).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
