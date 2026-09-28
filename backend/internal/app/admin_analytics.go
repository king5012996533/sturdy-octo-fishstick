package app

import (
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// 仪表盘只开放两档窗口：趋势图是"近 7/30 天"，允许任意天数等于让管理员
// 一次次触发全表扫描。
const (
	AdminOverviewDefaultDays = 7
	AdminOverviewMaxDays     = 30
	adminAuditDefaultLimit   = 50
	adminAuditMaxLimit       = 200
)

// AdminTrendPoint 是趋势图上的一天。
type AdminTrendPoint struct {
	Day          string `json:"day"`
	Calls        int64  `json:"calls"`
	FailedCalls  int64  `json:"failedCalls"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
}

// AdminCanvasOverview 是仪表盘的画布侧读数。
//
// 账号侧计数（总用户、新增用户）来自另一套库，由托管层合并后返回，因此这里
// 只回报画布库能自证的部分。
type AdminCanvasOverview struct {
	Canvases        int64             `json:"canvases"`
	ActiveCanvases  int64             `json:"activeCanvases"`
	Assets          int64             `json:"assets"`
	StoredBytes     int64             `json:"storedBytes"`
	Calls           int64             `json:"calls"`
	FailedCalls     int64             `json:"failedCalls"`
	InputTokens     int64             `json:"inputTokens"`
	OutputTokens    int64             `json:"outputTokens"`
	Channels        int64             `json:"channels"`
	EnabledChannels int64             `json:"enabledChannels"`
	Models          int64             `json:"models"`
	EnabledModels   int64             `json:"enabledModels"`
	Trend           []AdminTrendPoint `json:"trend"`
	Days            int               `json:"days"`
	GeneratedAt     time.Time         `json:"generatedAt"`
}

// AdminCanvasOverview 汇总画布、素材、存储、调用量与渠道模型规模。
//
// days 决定调用量窗口与趋势区间；"活跃画布"沿用同一个窗口，避免出现"近 7 天
// 调用量"配"近 30 天活跃画布"这种读不懂的组合。
func (s *Service) AdminCanvasOverview(actor *model.User, days int) (*AdminCanvasOverview, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	days = normalizeOverviewDays(days)
	now := time.Now()
	windowStart := dayStart(now).AddDate(0, 0, -(days - 1))

	totals, err := s.repo.AdminCanvasTotals(windowStart, windowStart)
	if err != nil {
		return nil, err
	}
	channelTotals, err := s.repo.AdminChannelTotals()
	if err != nil {
		return nil, err
	}
	points, err := s.repo.AdminDailyCallTrend(windowStart, dayStart(now).AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	return &AdminCanvasOverview{
		Canvases:        totals.Canvases,
		ActiveCanvases:  totals.ActiveCanvases,
		Assets:          totals.Assets,
		StoredBytes:     totals.StoredBytes,
		Calls:           totals.Calls,
		FailedCalls:     totals.FailedCalls,
		InputTokens:     totals.InputTokens,
		OutputTokens:    totals.OutputTokens,
		Channels:        channelTotals.Channels,
		EnabledChannels: channelTotals.EnabledChannels,
		Models:          channelTotals.Models,
		EnabledModels:   channelTotals.EnabledModels,
		Trend:           fillTrendGaps(points, windowStart, days),
		Days:            days,
		GeneratedAt:     now,
	}, nil
}

// AdminCanvasCountsByOwner 返回每个账号名下的画布数，供用户列表展示"作品数"。
func (s *Service) AdminCanvasCountsByOwner(actor *model.User, userIDs []string) (map[string]int64, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	counts, err := s.repo.CanvasCountsByUser(userIDs)
	if err != nil {
		return nil, err
	}
	return counts, nil
}

// AdminAuditEventPage 是审计日志的一页。
type AdminAuditEventPage struct {
	Events []model.AdminAuditEvent `json:"events"`
	Total  int64                   `json:"total"`
	Page   int                     `json:"page"`
	Limit  int                     `json:"pageSize"`
}

// RecordAdminAudit 由托管层在账号侧写操作成功后调用。
//
// 账号库与画布库归属不同，但审计必须落在一处：管理员的"封禁/改角色/重置密码"
// 和"改渠道/改开关"是同一条责任链，分散到两个库就无法按人回放。
func (s *Service) RecordAdminAudit(actor *model.User, action string, targetType string, targetID string, summary string, metadata any) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	return s.appendAdminAudit(actor, action, targetType, targetID, summary, metadata)
}

// AdminAuditEvents 返回审计日志分页。
func (s *Service) AdminAuditEvents(actor *model.User, page int, limit int) (*AdminAuditEventPage, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = adminAuditDefaultLimit
	}
	if limit > adminAuditMaxLimit {
		limit = adminAuditMaxLimit
	}
	events, total, err := s.repo.AdminAuditEvents(limit, (page-1)*limit)
	if err != nil {
		return nil, err
	}
	if events == nil {
		events = []model.AdminAuditEvent{}
	}
	return &AdminAuditEventPage{Events: events, Total: total, Page: page, Limit: limit}, nil
}

func normalizeOverviewDays(days int) int {
	if days <= 0 {
		return AdminOverviewDefaultDays
	}
	if days > AdminOverviewMaxDays {
		return AdminOverviewMaxDays
	}
	return days
}

func dayStart(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, value.Location())
}

// fillTrendGaps 把"只有有数据的日期"补齐成连续区间。
//
// 前端按数组下标画折线，缺日会让曲线把两个不相邻的日期连成一条直线，看起来像
// 那天有量。补齐零值比让前端自己猜区间更稳。
func fillTrendGaps(points []repository.AdminDailyPoint, from time.Time, days int) []AdminTrendPoint {
	byDay := make(map[string]repository.AdminDailyPoint, len(points))
	for _, point := range points {
		byDay[point.Day] = point
	}
	trend := make([]AdminTrendPoint, 0, days)
	for offset := 0; offset < days; offset++ {
		day := from.AddDate(0, 0, offset).Format("2006-01-02")
		point := byDay[day]
		trend = append(trend, AdminTrendPoint{
			Day:          day,
			Calls:        point.Calls,
			FailedCalls:  point.FailedCalls,
			InputTokens:  point.InputTokens,
			OutputTokens: point.OutputTokens,
		})
	}
	return trend
}
