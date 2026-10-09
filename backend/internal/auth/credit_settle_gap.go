package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 文本结算的欠款清单。
//
// 文本任务按真实 token 用量结算，而结算发生在用户拿到结果之后——这时余额可能已经不够。
// 积分账户不允许为负（余额是用户可见的数字，负数既解释不清也挡不住后续消费的歧义），
// 因此收不回来的那一部分不能写进流水，只能单独记账。
//
// 放在账号库而不是画布库：它是一笔钱的读数，与流水同源，后台要能和扣费列并排看。
// 也正因为不写进流水，它不会污染"流水之和 = 余额"这条不变量。

// CreditSettleGap 是一条任务收不回来的结算差额。
type CreditSettleGap struct {
	ID     string `gorm:"primaryKey;size:36"`
	UserID string `gorm:"index;size:36"`
	// TaskID 唯一：一条任务只会有一条欠款记录，重放收尾不会记两遍。
	TaskID      string `gorm:"uniqueIndex;size:64"`
	ModelKey    string `gorm:"size:160"`
	Uncollected int64
	CreatedAt   time.Time
}

func (CreditSettleGap) TableName() string { return "credit_settle_gaps" }

// RecordCreditSettleGap 记下一次收不回来的结算差额。
//
// 同一条任务重放时覆盖而不是追加：欠款是"这条任务还差多少"的现状，不是累计发生额。
// 金额按较大值保留，避免重放时上游回执变少（例如缓存统计被修正）把已经暴露的缺口改小。
func (s *Store) RecordCreditSettleGap(gap CreditSettleGap) error {
	if strings.TrimSpace(gap.TaskID) == "" {
		return errors.New("auth: 欠款记录缺少任务标识")
	}
	if gap.CreatedAt.IsZero() {
		gap.CreatedAt = time.Now()
	}
	if strings.TrimSpace(gap.ID) == "" {
		gap.ID = "gap-" + gap.TaskID
	}
	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "task_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"user_id":     gap.UserID,
			"model_key":   gap.ModelKey,
			"uncollected": gorm.Expr(scalarMaxExpr(s.sqlDialect(), "credit_settle_gaps.uncollected", "?"), gap.Uncollected),
		}),
	}).Create(&gap).Error
}

// ClearCreditSettleGap 在补扣成功后清掉这条任务的欠款记录。
//
// 任务收尾可能重放：第一次因为余额不足记了欠款，用户充值后重放补扣成功，这时欠款必须
// 消失，否则后台会一直挂着一条已经不存在的缺口。
func (s *Store) ClearCreditSettleGap(taskID string) error {
	if strings.TrimSpace(taskID) == "" {
		return nil
	}
	if err := s.db.Where("task_id = ?", strings.TrimSpace(taskID)).Delete(&CreditSettleGap{}).Error; err != nil {
		return fmt.Errorf("auth: 清理欠款记录失败: %w", err)
	}
	return nil
}

// RecentCreditSettleGaps 按时间倒序读最近的欠款记录。
func (s *Store) RecentCreditSettleGaps(limit int) ([]CreditSettleGap, error) {
	if limit <= 0 {
		limit = 100
	}
	gaps := make([]CreditSettleGap, 0, limit)
	if err := s.db.Model(&CreditSettleGap{}).Order("created_at desc").Limit(limit).Find(&gaps).Error; err != nil {
		return nil, fmt.Errorf("auth: 读取文本结算欠款失败: %w", err)
	}
	return gaps, nil
}

// AdminSettleGapRow 是后台看到的一条欠款读数。
type AdminSettleGapRow struct {
	TaskID      string
	UserID      string
	ModelKey    string
	Uncollected int64
	CreatedAt   time.Time
}

// CreditSettleGapTotal 返回所有欠款的合计。
//
// 与列表分开取：列表会截断到几十条，合计必须是全量，否则"少收了多少钱"这个读数会随着
// 列表长度悄悄变小——那正是最容易让人以为账已经平了的错法。
func (s *Store) CreditSettleGapTotal() (int64, error) {
	var total int64
	err := s.db.Model(&CreditSettleGap{}).Select("COALESCE(SUM(uncollected), 0)").Scan(&total).Error
	if err != nil {
		return 0, fmt.Errorf("auth: 统计文本结算欠款失败: %w", err)
	}
	return total, nil
}

// AdminRecentSettleGaps 返回最近的文本结算欠款与全量合计，供后台对账页展示。
func (s *Service) AdminRecentSettleGaps(limit int) ([]AdminSettleGapRow, int64, error) {
	gaps, err := s.store.RecentCreditSettleGaps(limit)
	if err != nil {
		return nil, 0, internalFailure(err)
	}
	total, err := s.store.CreditSettleGapTotal()
	if err != nil {
		return nil, 0, internalFailure(err)
	}
	rows := make([]AdminSettleGapRow, 0, len(gaps))
	for _, gap := range gaps {
		rows = append(rows, AdminSettleGapRow{
			TaskID:      gap.TaskID,
			UserID:      gap.UserID,
			ModelKey:    gap.ModelKey,
			Uncollected: gap.Uncollected,
			CreatedAt:   gap.CreatedAt,
		})
	}
	return rows, total, nil
}

// isInsufficientCreditsError 判断一个错误是不是"余额不够扣"。
//
// 结算要按它分流：余额不足是"钱收不回来"，属于要记账的业务事实；其余错误（账号库抖动、
// 写库失败）是故障，应当原样抛出由调用方留痕。
func isInsufficientCreditsError(err error) bool {
	var authErr *Error
	if !errors.As(err, &authErr) {
		return false
	}
	return authErr.Status == http.StatusPaymentRequired
}
