package auth

import (
	"errors"
	"time"

	"infinite-canvas/backend/internal/kernel"

	"gorm.io/gorm"
)

// ErrNotFound 表示记录不存在。上层据此区分「未登录」和「系统故障」。
var ErrNotFound = errors.New("记录不存在")

// Store 是账号体系表的读写投影。
//
// 它只做查询和持久化，不含业务策略；策略在 Service 与 strategies 中。
type Store struct {
	db  *gorm.DB
	now func() time.Time
}

// NewStore 包装一个已打开的数据库连接。
//
// db 必须指向由 Prisma 迁移管理的库；本包不会创建或修改表结构。
func NewStore(db *gorm.DB) *Store {
	return &Store{db: db, now: time.Now}
}

// UseClock 覆盖时间源。
//
// 存储在过期判断里直接比较时间，若不与服务共用同一个时钟，测试注入的固定时间
// 只影响服务层，写库的 expires_at 与查询用的 now 就会错位；线上两者都退化成
// time.Now，因此这里只需要一个可替换的单一来源。
func (s *Store) UseClock(now func() time.Time) {
	if s == nil || now == nil {
		return
	}
	s.now = now
}

func (s *Store) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

// MethodConfig 读取某种登录方式的配置。
func (s *Store) MethodConfig(methodType MethodType) (*MethodConfig, error) {
	var config MethodConfig
	err := s.db.Where("method_type = ?", string(methodType)).First(&config).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &config, nil
}

// EnabledMethods 返回启用且可见的登录方式，按展示顺序排列。
func (s *Store) EnabledMethods() ([]MethodConfig, error) {
	var configs []MethodConfig
	err := s.db.
		Where("is_enabled = ? AND is_visible = ?", true, true).
		Order("sort_order asc").
		Find(&configs).Error
	return configs, err
}

// UserByID 按主键读取用户。
func (s *Store) UserByID(id string) (*User, error) {
	var user User
	err := s.db.Where("id = ?", id).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// UserByEmail 按邮箱读取用户。
func (s *Store) UserByEmail(email string) (*User, error) {
	var user User
	err := s.db.Where("email = ?", email).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Store) UserByPhone(phone string) (*User, error) {
	var user User
	err := s.db.Where("phone = ?", phone).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// UserByUsername 按账号读取用户。
func (s *Store) UserByUsername(username string) (*User, error) {
	var user User
	err := s.db.Where("username = ?", username).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// CreateUser 写入新用户。
func (s *Store) CreateUser(user *User) error {
	now := s.clock()
	if user.ID == "" {
		user.ID = kernel.NewID()
	}
	user.CreatedAt = now
	user.UpdatedAt = now
	if user.Role == "" {
		user.Role = RoleUser
	}
	if user.Status == "" {
		user.Status = StatusActive
	}
	return s.db.Create(user).Error
}

// CreateSession 写入会话记录。
func (s *Store) CreateSession(session *Session) error {
	now := s.clock()
	if session.ID == "" {
		session.ID = kernel.NewID()
	}
	// CreatedAt 参与会话绝对有效期判断，允许调用方注入时钟以便测试。
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	session.UpdatedAt = now
	return s.db.Create(session).Error
}

// SessionByTokenHash 按令牌摘要读取未吊销且未过期的会话。
func (s *Store) SessionByTokenHash(tokenHash string) (*Session, error) {
	var session Session
	err := s.db.
		Where("token_hash = ? AND revoked_at IS NULL AND expires_at > ?", tokenHash, s.clock()).
		First(&session).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// TouchSession 刷新会话活跃时间。调用方负责节流，避免每请求一次写。
func (s *Store) TouchSession(id string, at time.Time) error {
	return s.db.Model(&Session{}).
		Where("id = ?", id).
		UpdateColumn("last_active_at", at).Error
}

// RefreshSession 一次性写入活跃时间与续期后的过期时间。
//
// 两列必须同一条 UPDATE：分开写会在崩溃窗口里留下「已续期但活跃时间没更新」
// 的中间态，下次请求又会重复续期。
func (s *Store) RefreshSession(id string, lastActiveAt time.Time, expiresAt time.Time) error {
	return s.db.Model(&Session{}).
		Where("id = ?", id).
		Updates(map[string]any{"last_active_at": lastActiveAt, "expires_at": expiresAt}).Error
}

// RevokeSession 吊销会话。已吊销的会话保持幂等。
func (s *Store) RevokeSession(tokenHash string, at time.Time) error {
	return s.db.Model(&Session{}).
		Where("token_hash = ? AND revoked_at IS NULL", tokenHash).
		Update("revoked_at", at).Error
}

// RevokeUserSessions 吊销某用户的全部会话，用于封禁或改密。
func (s *Store) RevokeUserSessions(userID string, at time.Time) error {
	return s.db.Model(&Session{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", at).Error
}

// Identity 按第三方方法 + 上游用户 ID 读取身份绑定。
func (s *Store) Identity(methodType MethodType, providerUserID string) (*AuthIdentity, error) {
	var identity AuthIdentity
	err := s.db.
		Where("method_type = ? AND provider_user_id = ?", string(methodType), providerUserID).
		First(&identity).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &identity, nil
}

// IdentityByIdentifier 按第三方方法 + 标识读取身份绑定。
func (s *Store) IdentityByIdentifier(methodType MethodType, identifier string) (*AuthIdentity, error) {
	var identity AuthIdentity
	err := s.db.
		Where("method_type = ? AND identifier = ?", string(methodType), identifier).
		First(&identity).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &identity, nil
}

// CreateIdentity 建立第三方身份绑定。
func (s *Store) CreateIdentity(identity *AuthIdentity) error {
	now := s.clock()
	if identity.ID == "" {
		identity.ID = kernel.NewID()
	}
	identity.CreatedAt = now
	identity.UpdatedAt = now
	return s.db.Create(identity).Error
}

// InvalidateActiveCodes 作废目标对象上尚未使用的验证码，确保同一时刻只有一个有效码。
func (s *Store) InvalidateActiveCodes(methodType MethodType, target string, scene string) error {
	return s.db.Model(&VerificationCode{}).
		Where("method_type = ? AND target = ? AND scene = ? AND used_at IS NULL AND expires_at > ?",
			string(methodType), target, scene, s.clock()).
		Update("used_at", s.clock()).Error
}

// CreateVerificationCode 写入验证码记录。
func (s *Store) CreateVerificationCode(record *VerificationCode) error {
	now := s.clock()
	if record.ID == "" {
		record.ID = kernel.NewID()
	}
	// CreatedAt 参与验证码冷却判断，允许调用方注入时钟以便测试。
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	return s.db.Create(record).Error
}

// LatestCodeIssuedAt 返回目标对象最近一次验证码的下发时间，用于冷却判断。
func (s *Store) LatestCodeIssuedAt(methodType MethodType, target string, scene string) (*time.Time, error) {
	var record VerificationCode
	err := s.db.
		Where("method_type = ? AND target = ? AND scene = ?", string(methodType), target, scene).
		Order("created_at desc").
		First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &record.CreatedAt, nil
}

// ConsumeCode 原子消费一条验证码：命中即标记 used_at，重复使用会失败。
func (s *Store) ConsumeCode(methodType MethodType, target string, code string, scene string) (*VerificationCode, error) {
	var record VerificationCode
	err := s.db.
		Where("method_type = ? AND target = ? AND code = ? AND scene = ? AND used_at IS NULL AND expires_at > ?",
			string(methodType), target, code, scene, s.clock()).
		Order("created_at desc").
		First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	now := s.clock()
	result := s.db.Model(&VerificationCode{}).
		Where("id = ? AND used_at IS NULL", record.ID).
		Update("used_at", now)
	if result.Error != nil {
		return nil, result.Error
	}
	// 影响行数为 0 说明另一个并发请求先消费了同一验证码。
	if result.RowsAffected == 0 {
		return nil, ErrNotFound
	}
	record.UsedAt = &now
	return &record, nil
}

// AttachCodeUser 把验证码记录关联到已解析用户，便于审计。
func (s *Store) AttachCodeUser(codeID string, userID string) error {
	return s.db.Model(&VerificationCode{}).
		Where("id = ?", codeID).
		Update("user_id", userID).Error
}

// CreateAgreements 在同一事务里落下一组协议接受记录。
//
// 用户勾选是一次动作，但协议是多份：拆成多次写入会让「同意了用户协议、还没同意
// 隐私政策」这种中间状态被读到，因此必须原子写入。
func (s *Store) CreateAgreements(records []UserAgreement) error {
	if len(records) == 0 {
		return nil
	}
	now := s.clock()
	for index := range records {
		if records[index].ID == "" {
			records[index].ID = kernel.NewID()
		}
		if records[index].AcceptedAt.IsZero() {
			records[index].AcceptedAt = now
		}
		records[index].CreatedAt = now
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		return tx.Create(&records).Error
	})
}
