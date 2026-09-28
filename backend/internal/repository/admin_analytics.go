package repository

import (
	"sort"
	"time"

	"infinite-canvas/backend/internal/model"
)

// AdminCanvasTotals 是仪表盘的画布侧计数。
type AdminCanvasTotals struct {
	Canvases       int64
	ActiveCanvases int64
	Assets         int64
	StoredBytes    int64
	Calls          int64
	FailedCalls    int64
	InputTokens    int64
	OutputTokens   int64
}

// AdminDailyPoint 是趋势图上的一天。
type AdminDailyPoint struct {
	Day          string `json:"day"`
	Calls        int64  `json:"calls"`
	FailedCalls  int64  `json:"failedCalls"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
}

// AdminCanvasTotals 汇总画布、素材、存储与调用量。
//
// activeSince 决定"活跃画布"的口径（近期被写过的画布），callSince 决定调用量窗口。
func (r *Repository) AdminCanvasTotals(activeSince time.Time, callSince time.Time) (AdminCanvasTotals, error) {
	var totals AdminCanvasTotals
	if err := r.db.Model(&model.CanvasProject{}).Count(&totals.Canvases).Error; err != nil {
		return totals, err
	}
	if err := r.db.Model(&model.CanvasProject{}).Where("updated_at >= ?", activeSince).Count(&totals.ActiveCanvases).Error; err != nil {
		return totals, err
	}
	if err := r.db.Model(&model.Asset{}).Count(&totals.Assets).Error; err != nil {
		return totals, err
	}
	// 与账号配额同口径：同一份物理对象被多次引用只算一次。
	var storedBytes int64
	if err := r.db.Raw(`
		SELECT COALESCE(SUM(size), 0) FROM (
			SELECT MAX(size) AS size
			FROM resources
			WHERE status = ?
			GROUP BY COALESCE(NULLIF(provider, ''), 'local'), endpoint, bucket, object_key
		) AS physical_resources
	`, model.ResourceStatusReady).Scan(&storedBytes).Error; err != nil {
		return totals, err
	}
	totals.StoredBytes = storedBytes

	var aggregate struct {
		Calls        int64
		FailedCalls  int64
		InputTokens  int64
		OutputTokens int64
	}
	// CASE WHEN 在 SQLite 与 MySQL 上语义一致，不引入方言分支。
	if err := r.db.Model(&model.ApiCallLog{}).
		Where("created_at >= ?", callSince).
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

// AdminDailyCallTrend 返回 [from, to) 内按天归并的调用量与 token 消耗。
//
// 按天分组刻意放在 Go 侧：SQLite 用 date()、MySQL 用 DATE()，为一个仪表盘曲线
// 引入方言分支不划算；这里只取四列，量级由调用量本身决定。
func (r *Repository) AdminDailyCallTrend(from time.Time, to time.Time) ([]AdminDailyPoint, error) {
	type callRow struct {
		CreatedAt    time.Time
		Status       model.ApiCallStatus
		InputTokens  int64
		OutputTokens int64
	}
	var rows []callRow
	if err := r.db.Model(&model.ApiCallLog{}).
		Select("created_at, status, input_tokens, output_tokens").
		Where("created_at >= ? AND created_at < ?", from, to).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	byDay := make(map[string]*AdminDailyPoint)
	for _, row := range rows {
		day := row.CreatedAt.Format("2006-01-02")
		point, ok := byDay[day]
		if !ok {
			point = &AdminDailyPoint{Day: day}
			byDay[day] = point
		}
		point.Calls++
		if row.Status == model.ApiCallStatusFailed {
			point.FailedCalls++
		}
		point.InputTokens += row.InputTokens
		point.OutputTokens += row.OutputTokens
	}
	points := make([]AdminDailyPoint, 0, len(byDay))
	for _, point := range byDay {
		points = append(points, *point)
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Day < points[j].Day })
	return points, nil
}

// AdminChannelTotals 是仪表盘的渠道与模型计数。
type AdminChannelTotals struct {
	Channels        int64
	EnabledChannels int64
	Models          int64
	EnabledModels   int64
}

// AdminChannelTotals 统计系统渠道及其下挂模型。
//
// 模型数走子查询而不是逐渠道 ChannelModels：仪表盘只需要计数，逐个渠道取全量
// 模型列表既慢又浪费内存。
func (r *Repository) AdminChannelTotals() (AdminChannelTotals, error) {
	var totals AdminChannelTotals
	systemChannels := r.db.Model(&model.ModelChannel{}).Select("id").Where("scope = ?", model.ChannelScopeSystem)
	if err := r.db.Model(&model.ModelChannel{}).Where("scope = ?", model.ChannelScopeSystem).Count(&totals.Channels).Error; err != nil {
		return totals, err
	}
	if err := r.db.Model(&model.ModelChannel{}).Where("scope = ? AND enabled = ?", model.ChannelScopeSystem, true).Count(&totals.EnabledChannels).Error; err != nil {
		return totals, err
	}
	if err := r.db.Model(&model.ChannelModel{}).Where("channel_id IN (?)", systemChannels).Count(&totals.Models).Error; err != nil {
		return totals, err
	}
	if err := r.db.Model(&model.ChannelModel{}).Where("enabled = ? AND channel_id IN (?)", true, systemChannels).Count(&totals.EnabledModels).Error; err != nil {
		return totals, err
	}
	return totals, nil
}

// CanvasCountsByUser 返回每个账号名下的画布数量。
//
// 用户管理列表要展示"作品数"，逐个账号查一次会退化成 N+1；这里一次分组取回。
func (r *Repository) CanvasCountsByUser(userIDs []string) (map[string]int64, error) {
	counts := make(map[string]int64, len(userIDs))
	if len(userIDs) == 0 {
		return counts, nil
	}
	type row struct {
		UserID string
		Total  int64
	}
	var rows []row
	if err := r.db.Model(&model.CanvasProject{}).
		Select("user_id, COUNT(*) AS total").
		Where("user_id IN ?", userIDs).
		Group("user_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, item := range rows {
		counts[item.UserID] = item.Total
	}
	return counts, nil
}
