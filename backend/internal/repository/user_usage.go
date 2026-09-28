package repository

import (
	"time"

	"infinite-canvas/backend/internal/model"
)

// UserUsageTotals 是单个账号在窗口内的用量读数。
type UserUsageTotals struct {
	Canvases       int64
	ActiveCanvases int64
	Assets         int64
	StoredBytes    int64
	Calls          int64
	FailedCalls    int64
	InputTokens    int64
	OutputTokens   int64
}

// UserUsageTotals 汇总一个账号的画布、素材、存储与调用量。
//
// 存储量走与配额校验同一份口径：同一份物理对象被多次引用只算一次，否则用户会
// 看到"账户页说我用了 3GB，配额校验说我只用了 1GB"。
func (r *Repository) UserUsageTotals(userID string, activeSince time.Time, callSince time.Time) (UserUsageTotals, error) {
	var totals UserUsageTotals
	if err := r.db.Model(&model.CanvasProject{}).Where("user_id = ?", userID).Count(&totals.Canvases).Error; err != nil {
		return totals, err
	}
	if err := r.db.Model(&model.CanvasProject{}).Where("user_id = ? AND updated_at >= ?", userID, activeSince).Count(&totals.ActiveCanvases).Error; err != nil {
		return totals, err
	}
	if err := r.db.Model(&model.Asset{}).Where("user_id = ?", userID).Count(&totals.Assets).Error; err != nil {
		return totals, err
	}
	storedBytes, err := r.UserStoredFileBytes(userID)
	if err != nil {
		return totals, err
	}
	totals.StoredBytes = storedBytes

	var aggregate struct {
		Calls        int64
		FailedCalls  int64
		InputTokens  int64
		OutputTokens int64
	}
	if err := r.db.Model(&model.ApiCallLog{}).
		Where("user_id = ? AND created_at >= ?", userID, callSince).
		Select(`COUNT(*) AS calls,
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) AS failed_calls,
			COALESCE(SUM(input_tokens), 0) AS input_tokens,
			COALESCE(SUM(output_tokens), 0) AS output_tokens`, model.ApiCallStatusFailed).
		Scan(&aggregate).Error; err != nil {
		return totals, err
	}
	totals.Calls = aggregate.Calls
	totals.FailedCalls = aggregate.FailedCalls
	totals.InputTokens = aggregate.InputTokens
	totals.OutputTokens = aggregate.OutputTokens
	return totals, nil
}
