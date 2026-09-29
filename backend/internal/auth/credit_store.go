package auth

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 积分域的存储层。
//
// 这一层的全部写操作都只走 AppendCreditEntry 一个入口：余额与流水必须在同一事务里
// 落地，任何"先扣钱再补流水"的写法都会在进程被杀死时留下一笔无法解释的余额。
// 读操作（余额、流水列表）另行暴露，不参与一致性约束。

// errCreditInsufficient 表示余额不够扣。由服务层翻译成 402，存储层不决定 HTTP 语义。
var errCreditInsufficient = errors.New("auth: 积分余额不足")

// EnsureCreditSchema 建积分表并声明唯一索引，仅用于本地 SQLite。
//
// 唯一索引必须用幂等 SQL 再声明一次，不能只靠结构体标签：AutoMigrate 对已存在的表
// 只在它自己确认缺索引时才补，而扣费路径靠这个索引保证幂等——索引一旦漏建，重复扣费
// 会在生产上静默发生，这是"本地跑通"和"线上安全"之间最不该有落差的一处。
func EnsureCreditSchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("auth: 数据库连接为空")
	}
	if name := db.Dialector.Name(); name != "sqlite" {
		return fmt.Errorf("auth: 拒绝为 %s 驱动创建积分表；该库的表结构由 Prisma 迁移管理", name)
	}
	if err := db.AutoMigrate(CreditModels()...); err != nil {
		return err
	}
	statements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_credit_ledger_idempotency ON credit_ledger_entries (user_id, kind, ref_type, ref_id)`,
		`CREATE INDEX IF NOT EXISTS idx_credit_ledger_user_created ON credit_ledger_entries (user_id, created_at)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("auth: 创建积分索引失败（%s）: %w", statement, err)
		}
	}
	return nil
}

// creditInflow 取一次变动的入账额，出账时为 0。
func creditInflow(amount int64) int64 {
	if amount > 0 {
		return amount
	}
	return 0
}

// creditOutflow 取一次变动的出账额（正数），入账时为 0。
func creditOutflow(amount int64) int64 {
	if amount < 0 {
		return -amount
	}
	return 0
}

// CreditAccountFor 读一个账号的积分账户，不存在时返回 (nil, nil)。
//
// 返回 nil 而不是自动开户：查余额是只读路径，不该因为一次 GET 就写库。
func (s *Store) CreditAccountFor(userID string) (*CreditAccount, error) {
	var account CreditAccount
	err := s.db.Where("user_id = ?", userID).First(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth: 读取积分账户失败: %w", err)
	}
	return &account, nil
}

// EnsureCreditAccount 幂等地建出一个零余额账户。
//
// 冲突时不做任何更新：重复调用不该把 balance 冲回零，也不该刷新 updated_at——
// 后者会让"账户最后变动时间"变成"最后一次有人读它"。
func (s *Store) EnsureCreditAccount(userID string) error {
	return s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&CreditAccount{UserID: userID}).Error
}

// CreditLedgerEntries 分页读流水，返回当页记录与总数。
func (s *Store) CreditLedgerEntries(filter CreditLedgerFilter) ([]CreditLedgerEntry, int64, error) {
	query := s.db.Model(&CreditLedgerEntry{}).Where("user_id = ?", filter.UserID)
	if kind := strings.TrimSpace(filter.Kind); kind != "" {
		query = query.Where("kind = ?", kind)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("auth: 统计积分流水失败: %w", err)
	}
	page, pageSize := normalizeCreditPage(filter.Page, filter.PageSize)
	var entries []CreditLedgerEntry
	if err := query.Order("created_at desc, id desc").Limit(pageSize).Offset((page - 1) * pageSize).Find(&entries).Error; err != nil {
		return nil, 0, fmt.Errorf("auth: 读取积分流水失败: %w", err)
	}
	return entries, total, nil
}

// CreditEntryByRef 按业务引用取一条流水，不存在时返回 (nil, nil)。
//
// 退回路径靠它拿"当初扣了多少"：金额从流水里读而不是由调用方回传，调用方就没有机会
// 退错数目——上游价格随时会调，让失败路径重新算一遍价，退的未必是当初扣的那笔。
func (s *Store) CreditEntryByRef(userID string, kind string, refType string, refID string) (*CreditLedgerEntry, error) {
	var entry CreditLedgerEntry
	err := s.db.Where(
		"user_id = ? AND kind = ? AND ref_type = ? AND ref_id = ?",
		userID, kind, refType, refID,
	).First(&entry).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth: 按业务引用查询积分流水失败: %w", err)
	}
	return &entry, nil
}

// AppendCreditEntries 在同一个事务里落一串积分变动。
//
// 做成批量而不是"单条 + 调用方循环"，是因为一次充值的本金与赠送必须同生共死：
// 分两次事务写入时，进程在中间退出就会留下一笔有本金没赠送的充值，而用户手里的
// 支付凭证与流水对不上——这类账只能靠人工倒推，代价远高于一个批量入口。
//
// created 与 entries 一一对应：false 表示这一笔早就记过（幂等命中），调用方应把它
// 当作成功并沿用返回的同一条流水，而不是重复执行后续副作用。
//
// 余额校验放在 UPDATE 的 WHERE 里（balance + amount >= 0）而不是先读后写：读改写
// 之间会插进另一笔并发扣费，两个请求各自看到"够扣"，结果一起扣成负数。带条件的
// 单条 UPDATE 由数据库保证原子，命中 0 行就是余额不足，不需要额外加锁。
func (s *Store) AppendCreditEntries(entries []CreditLedgerEntry) ([]CreditLedgerEntry, []bool, error) {
	stored := make([]CreditLedgerEntry, len(entries))
	created := make([]bool, len(entries))
	if len(entries) == 0 {
		return stored, created, nil
	}

	err := s.db.Transaction(func(tx *gorm.DB) error {
		for index, entry := range entries {
			existing, err := existingCreditEntry(tx, entry)
			if err != nil {
				return err
			}
			if existing != nil {
				stored[index] = *existing
				continue
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&CreditAccount{UserID: entry.UserID}).Error; err != nil {
				return fmt.Errorf("auth: 初始化积分账户失败: %w", err)
			}
			result := tx.Model(&CreditAccount{}).
				Where("user_id = ? AND balance + ? >= 0", entry.UserID, entry.Amount).
				Updates(map[string]any{
					"balance":      gorm.Expr("balance + ?", entry.Amount),
					"lifetime_in":  gorm.Expr("lifetime_in + ?", creditInflow(entry.Amount)),
					"lifetime_out": gorm.Expr("lifetime_out + ?", creditOutflow(entry.Amount)),
				})
			if result.Error != nil {
				return fmt.Errorf("auth: 更新积分余额失败: %w", result.Error)
			}
			if result.RowsAffected == 0 {
				return errCreditInsufficient
			}

			var account CreditAccount
			if err := tx.Where("user_id = ?", entry.UserID).First(&account).Error; err != nil {
				return fmt.Errorf("auth: 回读积分余额失败: %w", err)
			}
			record := entry
			record.BalanceAfter = account.Balance
			if err := tx.Create(&record).Error; err != nil {
				return fmt.Errorf("auth: 写入积分流水失败: %w", err)
			}
			stored[index] = record
			created[index] = true
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return stored, created, nil
}

// existingCreditEntry 按幂等键查一条已存在的流水。
//
// 先查再写是为了让重复调用能拿到"当初那条流水"而不是一个唯一键冲突：调用方需要把
// 同一条流水的 ID 回给前端，冲突错误给不出这个信息。查与写之间的竞态由唯一索引兜底，
// 真的撞上时事务整体回滚，余额不会被扣两次。
func existingCreditEntry(tx *gorm.DB, entry CreditLedgerEntry) (*CreditLedgerEntry, error) {
	var existing CreditLedgerEntry
	err := tx.Where(
		"user_id = ? AND kind = ? AND ref_type = ? AND ref_id = ?",
		entry.UserID, entry.Kind, entry.RefType, entry.RefID,
	).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth: 查询积分流水失败: %w", err)
	}
	return &existing, nil
}

// normalizeCreditPage 与其余托管域一致地钳住分页参数：
// 没有上限时一次请求就能把整张流水表读进内存。
func normalizeCreditPage(page int, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 20
	}
	return page, pageSize
}
