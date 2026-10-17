package repository

import "time"

// ChannelCallStat 是近 N 天某条渠道上某个模型的上游提交计数。
//
// 只统计 request_kind = 'create'：一次生成在 api_call_logs 里还会留下若干条 poll 与
// 一条 download，把它们一起算进去会把"一条任务失败"放大成"十次失败"。
type ChannelCallStat struct {
	ChannelID string
	Model     string
	Total     int64
	Succeeded int64
	Failed    int64
}

// ChannelCallErrorCount 是失败调用按错误码的分布。
type ChannelCallErrorCount struct {
	ChannelID  string
	Model      string
	ErrorCode  string
	StatusCode int
	Count      int64
}

// ChannelCallLastFailure 是某个渠道模型最近一次失败的原话与时间。
type ChannelCallLastFailure struct {
	ChannelID  string
	Model      string
	ErrorCode  string
	StatusCode int
	Error      string
	CreatedAt  time.Time
}

// ChannelCallStats 按渠道与模型汇总提交调用。
func (r *Repository) ChannelCallStats(since time.Time) ([]ChannelCallStat, error) {
	var rows []ChannelCallStat
	err := r.db.Raw(`
		SELECT channel_id,
		       model,
		       COUNT(*) AS total,
		       COALESCE(SUM(CASE WHEN status = 'succeeded' THEN 1 ELSE 0 END), 0) AS succeeded,
		       COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0) AS failed
		FROM api_call_logs
		WHERE request_kind = 'create' AND channel_id <> '' AND created_at >= ?
		GROUP BY channel_id, model
		ORDER BY failed DESC, total DESC
	`, since).Scan(&rows).Error
	if rows == nil {
		rows = []ChannelCallStat{}
	}
	return rows, err
}

// ChannelCallErrorCounts 按渠道模型与错误码汇总失败次数。
func (r *Repository) ChannelCallErrorCounts(since time.Time) ([]ChannelCallErrorCount, error) {
	var rows []ChannelCallErrorCount
	err := r.db.Raw(`
		SELECT channel_id,
		       model,
		       error_code,
		       status_code,
		       COUNT(*) AS count
		FROM api_call_logs
		WHERE request_kind = 'create' AND status = 'failed' AND channel_id <> '' AND created_at >= ?
		GROUP BY channel_id, model, error_code, status_code
		ORDER BY count DESC
	`, since).Scan(&rows).Error
	if rows == nil {
		rows = []ChannelCallErrorCount{}
	}
	return rows, err
}

// ChannelCallLastFailures 取每个渠道模型最近一次的失败原话。
func (r *Repository) ChannelCallLastFailures(since time.Time) ([]ChannelCallLastFailure, error) {
	var rows []ChannelCallLastFailure
	err := r.db.Raw(`
		SELECT channel_id, model, error_code, status_code, error, created_at
		FROM (
			SELECT channel_id, model, error_code, status_code, error, created_at,
			       ROW_NUMBER() OVER (PARTITION BY channel_id, model ORDER BY created_at DESC) AS rn
			FROM api_call_logs
			WHERE request_kind = 'create' AND status = 'failed' AND channel_id <> '' AND created_at >= ?
		) ranked
		WHERE rn = 1
	`, since).Scan(&rows).Error
	if rows == nil {
		rows = []ChannelCallLastFailure{}
	}
	return rows, err
}
