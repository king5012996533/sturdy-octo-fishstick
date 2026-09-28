package app

import (
	"time"

	"infinite-canvas/backend/internal/model"
)

// AccountUsageSummary 是用户在账户页看到的用量读数（自己的数据，不需要管理员权限）。
type AccountUsageSummary struct {
	Canvases       int64     `json:"canvases"`
	ActiveCanvases int64     `json:"activeCanvases"`
	Assets         int64     `json:"assets"`
	StoredBytes    int64     `json:"storedBytes"`
	Calls          int64     `json:"calls"`
	FailedCalls    int64     `json:"failedCalls"`
	InputTokens    int64     `json:"inputTokens"`
	OutputTokens   int64     `json:"outputTokens"`
	Days           int       `json:"days"`
	Since          time.Time `json:"since"`
}

// AccountUsage 汇总当前账号在窗口内的用量。
//
// 与仪表盘共用同一套口径：用户看到的数字必须和管理员在后台看到的一致，否则
// "平台计费说不清"就从这里开始。
func (s *Service) AccountUsage(actor *model.User, days int) (*AccountUsageSummary, error) {
	if actor == nil || actor.ID == "" {
		return nil, Unauthorized("请先登录")
	}
	days = normalizeOverviewDays(days)
	windowStart := dayStart(time.Now()).AddDate(0, 0, -(days - 1))
	totals, err := s.repo.UserUsageTotals(actor.ID, windowStart, windowStart)
	if err != nil {
		return nil, err
	}
	return &AccountUsageSummary{
		Canvases:       totals.Canvases,
		ActiveCanvases: totals.ActiveCanvases,
		Assets:         totals.Assets,
		StoredBytes:    totals.StoredBytes,
		Calls:          totals.Calls,
		FailedCalls:    totals.FailedCalls,
		InputTokens:    totals.InputTokens,
		OutputTokens:   totals.OutputTokens,
		Days:           days,
		Since:          windowStart,
	}, nil
}
