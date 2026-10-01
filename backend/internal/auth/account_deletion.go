package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"infinite-canvas/backend/internal/kernel"

	"gorm.io/gorm"
)

// 账号注销域：用户自助申请 → 冷静期内可撤销 → 到期匿名化。
//
// 为什么不是「点一下就删」：注销不可逆，而账号背后可能还挂着没跑完的生成任务、
// 未结算的订单和没导出的作品。给一段冷静期，用户后悔来得及；平台也才有窗口把
// 账务留痕跑完。
//
// 到期执行的语义是「匿名化」而不是物理删除：账本、订单、审计记录必须留存，
// 但能直接识别到个人的字段（邮箱、手机号、用户名、外部身份绑定）一律清空。
const (
	AccountDeletionPending   = "PENDING"
	AccountDeletionExecuted  = "EXECUTED"
	AccountDeletionCancelled = "CANCELLED"
)

// AccountDeletionGraceDays 是注销冷静期。到期前用户随时可以自己撤销。
const AccountDeletionGraceDays = 7

// accountDeletedDisplayName 是匿名化后的展示名，本身不含任何个人信息。
const accountDeletedDisplayName = "已注销用户"

// AccountDeletion 是一次注销申请。
//
// 只保留「谁、什么时候申请、计划什么时候执行、执行了没有」，不保存验证码与
// 会话凭据：这张表在冷静期内是活的，泄露它不能等价于拿到账号。
type AccountDeletion struct {
	ID          string     `gorm:"column:id;primaryKey;size:36"`
	UserID      string     `gorm:"column:user_id;size:36"`
	Status      string     `gorm:"column:status;size:24"`
	MethodType  string     `gorm:"column:method_type;size:24"`
	Reason      *string    `gorm:"column:reason;size:500"`
	RequesterIP *string    `gorm:"column:requester_ip;size:64"`
	RequestedAt time.Time  `gorm:"column:requested_at"`
	ScheduledAt time.Time  `gorm:"column:scheduled_at"`
	ExecutedAt  *time.Time `gorm:"column:executed_at"`
	CancelledAt *time.Time `gorm:"column:cancelled_at"`
}

func (AccountDeletion) TableName() string { return "auth_account_deletions" }

// AccountDeletionModels 供 EnsureDevSchema / EnsureAccountDeletionSchema 建表使用。
func AccountDeletionModels() []any { return []any{&AccountDeletion{}} }

// EnsureAccountDeletionSchema 建注销申请表，仅用于本地 SQLite。
//
// 与积分、定价域同样的驱动白名单：生产库结构归 CanvasMind 的 Prisma 迁移，
// 这里建表是为了让 SQLite 形态（含当前这台独立部署）能跑通整条链路。
func EnsureAccountDeletionSchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("auth: 数据库连接为空")
	}
	if name := db.Dialector.Name(); name != "sqlite" {
		return errors.New("auth: 拒绝为 " + name + " 驱动创建注销表；该库的表结构由 Prisma 迁移管理")
	}
	if err := db.AutoMigrate(AccountDeletionModels()...); err != nil {
		return err
	}
	// 到期扫描按 (status, scheduled_at) 取批次，撤销与查询按 user_id 定位：
	// 走全表扫描时注销申请会随用户量线性变慢，而这条路径是后台协程周期性跑的。
	statements := []string{
		`CREATE INDEX IF NOT EXISTS idx_auth_account_deletions_user ON auth_account_deletions (user_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_auth_account_deletions_due ON auth_account_deletions (status, scheduled_at)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

// CreateAccountDeletion 落一条待执行申请，并把该账号此前的待执行申请作废。
//
// 同一账号同时只能有一条待执行申请：重复申请按「重新计时」处理。若允许并存，
// 到期扫描会为同一个人跑两遍匿名化，第二遍还会因为找不到可识别的字段而报错。
func (s *Store) CreateAccountDeletion(record *AccountDeletion) error {
	if record == nil {
		return errors.New("auth: 注销申请为空")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&AccountDeletion{}).
			Where("user_id = ? AND status = ?", record.UserID, AccountDeletionPending).
			Updates(map[string]any{
				"status":       AccountDeletionCancelled,
				"cancelled_at": record.RequestedAt,
			}).Error; err != nil {
			return err
		}
		return tx.Create(record).Error
	})
}

// ActiveAccountDeletion 读取该账号当前待执行的申请，没有时返回 ErrNotFound。
func (s *Store) ActiveAccountDeletion(userID string) (*AccountDeletion, error) {
	var record AccountDeletion
	err := s.db.
		Where("user_id = ? AND status = ?", userID, AccountDeletionPending).
		Order("requested_at desc").
		First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// CancelAccountDeletion 撤销待执行申请，返回是否真的撤销掉了一条。
func (s *Store) CancelAccountDeletion(userID string, at time.Time) (bool, error) {
	result := s.db.Model(&AccountDeletion{}).
		Where("user_id = ? AND status = ?", userID, AccountDeletionPending).
		Updates(map[string]any{"status": AccountDeletionCancelled, "cancelled_at": at})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// DueAccountDeletions 取出已过冷静期、等待执行的申请。
func (s *Store) DueAccountDeletions(now time.Time, limit int) ([]AccountDeletion, error) {
	if limit <= 0 {
		limit = 50
	}
	var records []AccountDeletion
	err := s.db.
		Where("status = ? AND scheduled_at <= ?", AccountDeletionPending, now).
		Order("scheduled_at asc").
		Limit(limit).
		Find(&records).Error
	if err != nil {
		return nil, err
	}
	return records, nil
}

// ExecuteAccountDeletion 匿名化账号并标记申请已执行。
//
// 标记与匿名化必须同事务：先标记后匿名会留下「已注销但还能查到邮箱」的窗口，
// 反过来则会留下一条永远扫不到、也永远删不掉的申请。
func (s *Store) ExecuteAccountDeletion(id string, userID string, at time.Time) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		// 只认还处于待执行的行：用户中途撤销、或两个实例同时扫描，都不能把
		// 已撤销的申请执行掉。
		result := tx.Model(&AccountDeletion{}).
			Where("id = ? AND status = ?", id, AccountDeletionPending).
			Updates(map[string]any{"status": AccountDeletionExecuted, "executed_at": at})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}
		return anonymizeUser(tx, userID, at)
	})
}

// anonymizeUser 清空账号上可直接识别个人的字段。
//
// username / email / phone 都带唯一索引，因此必须置 NULL 而不是空串：空串会让
// 第二个注销的账号撞上唯一约束而失败，NULL 在 SQLite 与 MySQL 下都互不冲突。
// 账号行本身保留，因为账本、订单和审计都按 user_id 外键指向它。
func anonymizeUser(tx *gorm.DB, userID string, at time.Time) error {
	if tx == nil || strings.TrimSpace(userID) == "" {
		return errors.New("auth: 注销账号缺少用户标识")
	}
	result := tx.Model(&User{}).
		Where("id = ?", userID).
		Updates(map[string]any{
			"username":      nil,
			"password_hash": nil,
			"avatar_url":    nil,
			"email":         nil,
			"phone":         nil,
			"name":          accountDeletedDisplayName,
			"status":        string(StatusDisabled),
			"updated_at":    at,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	// 第三方身份绑定（provider_user_id / identifier）本身就是可识别信息，
	// 注销后必须整行删掉，留着等于用别人的微信/Google 身份标记这个人。
	if err := tx.Where("user_id = ?", userID).Delete(&AuthIdentity{}).Error; err != nil {
		return err
	}
	// 会话一并吊销：不吊销的话，已经被注销的账号手里的 cookie 还能继续下单。
	return tx.Model(&Session{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", at).Error
}

// AccountDeletionRequest 是一次自助注销申请。
type AccountDeletionRequest struct {
	UserID      string
	MethodType  MethodType
	Code        string
	Reason      string
	RequesterIP string
}

// AccountDeletionView 是前台看到的注销状态。
type AccountDeletionView struct {
	Status      string     `json:"status"`
	Reason      string     `json:"reason,omitempty"`
	RequestedAt *time.Time `json:"requestedAt,omitempty"`
	ScheduledAt *time.Time `json:"scheduledAt,omitempty"`
	GraceDays   int        `json:"graceDays"`
}

// RequestAccountDeletion 校验验证码后登记一次注销申请。
//
// 注销是强校验写路径：必须用账号自己绑定的邮箱或手机号收到的验证码确认身份，
// 且验证码走的仍是登录场景的下发通道（同一 scene），因此不存在「注销专用验证码
// 可以被别的场景复用」这种旁路。
func (s *Service) RequestAccountDeletion(ctx context.Context, in AccountDeletionRequest) (*AccountDeletionView, error) {
	if s == nil || s.store == nil {
		return nil, internalFailure(errors.New("账号库未装配"))
	}
	user, err := s.store.UserByID(strings.TrimSpace(in.UserID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, unauthorized("当前未登录或登录已失效")
		}
		return nil, internalFailure(err)
	}
	if user.Status != StatusActive {
		return nil, forbidden("账号当前状态不允许自助注销，请联系客服处理")
	}
	target, err := accountDeletionTarget(user, in.MethodType)
	if err != nil {
		return nil, err
	}
	code := strings.TrimSpace(in.Code)
	if code == "" {
		return nil, invalidArgument("请输入验证码")
	}
	if _, err := s.store.ConsumeCode(in.MethodType, target, code, codeScene); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, invalidArgument("验证码不正确或已过期")
		}
		return nil, internalFailure(err)
	}

	now := s.now()
	record := &AccountDeletion{
		ID:          kernel.NewID(),
		UserID:      user.ID,
		Status:      AccountDeletionPending,
		MethodType:  string(in.MethodType),
		Reason:      optionalString(truncate(strings.TrimSpace(in.Reason), 500)),
		RequesterIP: optionalString(strings.TrimSpace(in.RequesterIP)),
		RequestedAt: now,
		ScheduledAt: now.AddDate(0, 0, AccountDeletionGraceDays),
	}
	if err := s.store.CreateAccountDeletion(record); err != nil {
		return nil, internalFailure(err)
	}
	return accountDeletionViewOf(record), nil
}

// CancelAccountDeletion 撤销待执行申请。没有待执行申请时返回 notFound，
// 因为「撤销一个不存在的注销」在界面上只可能来自过期页面。
func (s *Service) CancelAccountDeletion(userID string) (*AccountDeletionView, error) {
	if s == nil || s.store == nil {
		return nil, internalFailure(errors.New("账号库未装配"))
	}
	cancelled, err := s.store.CancelAccountDeletion(strings.TrimSpace(userID), s.now())
	if err != nil {
		return nil, internalFailure(err)
	}
	if !cancelled {
		return nil, notFound("当前没有待执行的注销申请")
	}
	return &AccountDeletionView{Status: AccountDeletionCancelled, GraceDays: AccountDeletionGraceDays}, nil
}

// AccountDeletionView 返回当前账号的注销状态，没有申请时状态为 NONE。
func (s *Service) AccountDeletionStatus(userID string) (*AccountDeletionView, error) {
	if s == nil || s.store == nil {
		return nil, internalFailure(errors.New("账号库未装配"))
	}
	record, err := s.store.ActiveAccountDeletion(strings.TrimSpace(userID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return &AccountDeletionView{Status: "NONE", GraceDays: AccountDeletionGraceDays}, nil
		}
		return nil, internalFailure(err)
	}
	return accountDeletionViewOf(record), nil
}

// ExecuteDueAccountDeletions 执行已过冷静期的注销申请，返回实际执行条数。
//
// 单条失败不终止整批：一条坏数据不该让后面所有人的注销卡在队列里。失败原因由
// 调用方记日志，下一轮扫描还会重试同一条。
func (s *Service) ExecuteDueAccountDeletions(limit int) (int, error) {
	if s == nil || s.store == nil {
		return 0, internalFailure(errors.New("账号库未装配"))
	}
	due, err := s.store.DueAccountDeletions(s.now(), limit)
	if err != nil {
		return 0, internalFailure(err)
	}
	executed := 0
	for _, item := range due {
		if err := s.store.ExecuteAccountDeletion(item.ID, item.UserID, s.now()); err != nil {
			// 已被撤销或已被并行执行：不是错误，跳过即可。
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return executed, internalFailure(err)
		}
		executed++
	}
	return executed, nil
}

// accountDeletionTarget 取该账号在指定验证渠道上的标识。
//
// 只认账号自己绑定的邮箱/手机号：注销请求里带什么目标都不参与校验，否则用一个
// 自己收得到的号码就能注销别人的账号。
func accountDeletionTarget(user *User, methodType MethodType) (string, error) {
	if user == nil {
		return "", unauthorized("当前未登录或登录已失效")
	}
	switch methodType {
	case MethodEmailCode:
		if user.Email == nil || strings.TrimSpace(*user.Email) == "" {
			return "", invalidArgument("该账号未绑定邮箱，无法用邮箱验证码确认身份")
		}
		return strings.ToLower(strings.TrimSpace(*user.Email)), nil
	case MethodPhoneCode:
		if user.Phone == nil || strings.TrimSpace(*user.Phone) == "" {
			return "", invalidArgument("该账号未绑定手机号，无法用短信验证码确认身份")
		}
		return strings.TrimSpace(*user.Phone), nil
	default:
		return "", invalidArgument("请选择邮箱或手机号接收验证码")
	}
}

func accountDeletionViewOf(record *AccountDeletion) *AccountDeletionView {
	if record == nil {
		return &AccountDeletionView{Status: "NONE", GraceDays: AccountDeletionGraceDays}
	}
	view := &AccountDeletionView{
		Status:      record.Status,
		RequestedAt: &record.RequestedAt,
		ScheduledAt: &record.ScheduledAt,
		GraceDays:   AccountDeletionGraceDays,
	}
	if record.Reason != nil {
		view.Reason = *record.Reason
	}
	return view
}
