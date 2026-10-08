package repository

import (
	"fmt"
	"strings"

	"infinite-canvas/backend/internal/model"
)

// TextTaskTokenUsage 是一条文本任务在所有上游调用上累计的 token 用量。
//
// 按任务聚合而不是按调用：预扣与结算都以任务为单位，而且一次 Agent 会话本来就会连打
// 好几轮（每轮一次调用），逐次结算会让账单上多出一串同名的小额扣费，用户对不上账。
type TextTaskTokenUsage struct {
	// Input 是输入总量（含命中缓存的部分），Cached 是其中命中提示词缓存的部分，
	// Output 是模型生成的 token。
	Input  int64
	Cached int64
	Output int64
	// Calls 是参与统计的调用条数；0 表示这条任务没有任何上游调用，调用方据此跳过结算。
	Calls int64
	// UsageCalls 是上游回了用量回执的调用条数。小于 Calls 说明有一部分用量取不到，
	// 结算会少收钱，调用方必须把这件事记进日志而不是当成算准了。
	UsageCalls int64
}

// TextTaskTokenUsage 汇总一条文本任务的上游用量。
//
// 只统计能力为 text 的调用，并排除 poll / download：这两类是一次生成内部的取件动作，
// 不产生 token，算进来只会让某些渠道的用量凭空翻倍。
//
// 成功与失败的调用都计入：失败调用通常不带用量（计 0），但流式响应可能先回了 usage
// 再中断，那部分 token 上游照样收费，漏掉就等于平台替用户垫。
func (r *Repository) TextTaskTokenUsage(taskID string) (TextTaskTokenUsage, error) {
	usage := TextTaskTokenUsage{}
	if strings.TrimSpace(taskID) == "" {
		return usage, nil
	}
	err := r.db.Model(&model.ApiCallLog{}).
		Select("COUNT(*) AS calls, COALESCE(SUM(input_tokens), 0) AS input, COALESCE(SUM(cached_tokens), 0) AS cached, COALESCE(SUM(output_tokens), 0) AS output, COALESCE(SUM(CASE WHEN usage_available THEN 1 ELSE 0 END), 0) AS usage_calls").
		Where("task_id = ? AND capability = ?", taskID, "text").
		Where("COALESCE(request_kind, '') NOT IN ?", []string{"poll", "download"}).
		Scan(&usage).Error
	if err != nil {
		return usage, fmt.Errorf("repository: 汇总文本任务用量失败: %w", err)
	}
	return usage, nil
}
