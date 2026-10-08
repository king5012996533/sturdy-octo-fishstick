package auth

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// 后台对账用的积分只读查询。
//
// 与积分管理页的区别：那边按账号看流水，这边按任务看扣费。对账要回答的是
// "这笔扣费对应哪个产物"，所以入口是任务 ID，而任务 ID 恰恰不在账号库里。

// AdminTaskChargeRow 是一个任务的净扣费读数。
type AdminTaskChargeRow struct {
	TaskID    string
	UserID    string
	Net       int64
	ChargedAt time.Time
}

// creditReconciliationChunkSize 限制 IN 查询的元数。
//
// SQLite 的变量上限是 999，一次把几千个任务 ID 塞进 IN 会直接报错；分片后单条 SQL
// 的规模可控，代价只是多几次往返。
const creditReconciliationChunkSize = 256

// TaskChargeTotalsByIDs 批量读任务的净扣费，键为任务 ID。
//
// 净额而不是"扣了多少"：退过的任务净额会变小甚至归零，这样后台不会把一笔已经退回的
// 失败任务当成漏单继续挂着。
//
// 结算补扣也算进来：文本任务提交时只扣了起步价，真实费用在收尾时才补上。漏掉这一类
// 会让后台把一笔已经扣费的文本任务显示成只值 1 积分——对账时正是它会引出"是不是漏扣了"
// 的假警报。
func (s *Store) TaskChargeTotalsByIDs(taskIDs []string) (map[string]int64, error) {
	totals := make(map[string]int64, len(taskIDs))
	for start := 0; start < len(taskIDs); start += creditReconciliationChunkSize {
		end := start + creditReconciliationChunkSize
		if end > len(taskIDs) {
			end = len(taskIDs)
		}
		type row struct {
			RefID string
			Net   int64
		}
		rows := make([]row, 0, end-start)
		if err := s.db.Model(&CreditLedgerEntry{}).
			Select("ref_id AS ref_id, SUM(-amount) AS net").
			Where("ref_type = ? AND kind IN ?", CreditRefTask, []string{CreditKindCharge, CreditKindRefund, CreditKindSettle}).
			Where("ref_id IN ?", taskIDs[start:end]).
			Group("ref_id").
			Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("auth: 统计任务扣费失败: %w", err)
		}
		for _, item := range rows {
			totals[item.RefID] = item.Net
		}
	}
	return totals, nil
}

// TaskChargeBillingStart 返回第一条任务扣费的时间。
//
// 它是"计费上线前不算漏单"的分界线：上线前跑出来的任务本来就不扣费，把它们算成
// 漏单只会让真正要看的几笔淹没在噪音里。没有扣费记录时 ok 为 false，调用方应当
// 关掉漏单判定而不是拿零值当分界。
func (s *Store) TaskChargeBillingStart() (time.Time, bool, error) {
	var entry CreditLedgerEntry
	err := s.db.Model(&CreditLedgerEntry{}).
		Where("kind = ?", CreditKindCharge).
		Order("created_at asc, id asc").
		Limit(1).
		Take(&entry).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("auth: 读取计费起始时间失败: %w", err)
	}
	return entry.CreatedAt, true, nil
}

// RecentTaskCharges 返回最近的任务扣费流水，按时间倒序，最多 limit 条。
//
// 只取扣费方向的流水：反向漏单要找的是"扣了钱却没有产物"，退回记录不产生这个疑问。
func (s *Store) RecentTaskCharges(limit int) ([]CreditLedgerEntry, error) {
	if limit <= 0 {
		limit = 200
	}
	entries := make([]CreditLedgerEntry, 0, limit)
	if err := s.db.Model(&CreditLedgerEntry{}).
		Where("kind = ? AND ref_type = ?", CreditKindCharge, CreditRefTask).
		Order("created_at desc, id desc").
		Limit(limit).
		Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("auth: 读取最近任务扣费失败: %w", err)
	}
	return entries, nil
}

// ---------- 服务层 ----------

// AdminTaskChargeTotals 批量返回任务的净扣费，供后台把产物与扣费对上。
func (s *Service) AdminTaskChargeTotals(taskIDs []string) (map[string]int64, error) {
	totals, err := s.store.TaskChargeTotalsByIDs(taskIDs)
	if err != nil {
		return nil, internalFailure(err)
	}
	return totals, nil
}

// AdminTaskChargeBillingStart 返回计费起始时间，供后台区分"上线前没扣费"与"漏扣"。
func (s *Service) AdminTaskChargeBillingStart() (time.Time, bool, error) {
	start, ok, err := s.store.TaskChargeBillingStart()
	if err != nil {
		return time.Time{}, false, internalFailure(err)
	}
	return start, ok, nil
}

// AdminRecentTaskCharges 返回最近的扣费记录，用于反向对账（扣费无产物）。
func (s *Service) AdminRecentTaskCharges(limit int) ([]AdminTaskChargeRow, error) {
	entries, err := s.store.RecentTaskCharges(limit)
	if err != nil {
		return nil, internalFailure(err)
	}
	rows := make([]AdminTaskChargeRow, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, AdminTaskChargeRow{
			TaskID: entry.RefID, UserID: entry.UserID, Net: -entry.Amount, ChargedAt: entry.CreatedAt,
		})
	}
	return rows, nil
}
