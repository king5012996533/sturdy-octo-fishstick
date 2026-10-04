package app

import (
	"strings"

	"infinite-canvas/backend/internal/model"
)

// 参考视频的输入秒数也要计入扣费。
//
// 上游对 MiniMax 原生视频协议（秘塔 H3 走的是这条）按「输出 + 参考视频输入」的总秒数
// 结算：实测一条「输出 4 秒 + 参考视频 5.04 秒」的任务，用量回执是 input_seconds=6、
// output_seconds=4，上游按 10 秒收钱。只按输出秒数扣费，输入那部分就是平台垫的——
// 4 秒 768P 收 0.60 元、上游要收 0.90 元，每单倒亏。
//
// 别的视频协议不补这一段：Seedance 的上游按条结算（¥5/条，与秒数无关，见
// scripts/seed-video-model-prices.py 的文件头），把输入秒数加进去等于在没有成本依据的
// 情况下改价。要扩到别的协议，先把那条协议的用量回执拿到手。
func billsReferenceVideoSeconds(interfaceType string) bool {
	return strings.TrimSpace(interfaceType) == string(model.ChannelInterfaceMiniMaxVideo)
}

// referenceVideoSeconds 汇总本次提交的参考视频总时长（整秒，向上取整）。
//
// 时长只认服务端资源表：任务输入里的参考素材是 {storageKey: "resource:<id>"}，客户端
// 刻意不带 durationMs（见 web/src/services/api/generation-task.ts 的 backendMediaReference），
// 那条路走不通，也不该信客户端自报的秒数——那是个能直接改小账单的字段。
//
// 向上取整是跟着上游的算法走：5.04 秒的输入，回执里记的是 input_seconds=6。
//
// 取不到时长（素材是外链、资源不是视频、或时长还没探测出来）时按 0 计，不拿模型上限去猜：
// 多扣是用户当场就能看见的错账，少扣是一个有上限的漏损。
func (s *Service) referenceVideoSeconds(userID string, input map[string]any) int64 {
	if s == nil || s.repo == nil {
		return 0
	}
	list, ok := input["referenceVideos"].([]any)
	if !ok || len(list) == 0 {
		return 0
	}
	var total int64
	for _, raw := range list {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		key := strings.TrimSpace(stringValue(entry["storageKey"]))
		if !strings.HasPrefix(key, "resource:") {
			continue
		}
		id := strings.TrimPrefix(key, "resource:")
		if id == "" {
			continue
		}
		resource, err := s.repo.ResourceForUser(userID, id)
		if err != nil || resource == nil || resource.DurationMs <= 0 {
			continue
		}
		total += (resource.DurationMs + 999) / 1000
	}
	return total
}

// taskChargeQuantityFor 在按「输入 + 输出」总秒数结算的视频协议上，把参考视频的输入秒数
// 加进用量。提交预扣与试算共用这一个入口，所以页面上显示的估算和实际扣的是同一个数。
func (s *Service) taskChargeQuantityFor(task *model.Task, input map[string]any, intent ModelRequestIntent) int64 {
	quantity := taskChargeQuantity(intent)
	if intent.Capability != "video" || !billsReferenceVideoSeconds(inputInterfaceType(input)) {
		return quantity
	}
	return quantity + s.referenceVideoSeconds(task.UserID, input)
}

// inputInterfaceType 读任务输入里声明的协议。任务输入是提交时的原始载荷，config 一定在，
// 读不到只说明这份输入不是生成任务，返回空串让调用方按"不补秒数"处理。
func inputInterfaceType(input map[string]any) string {
	config, ok := input["config"].(map[string]any)
	if !ok {
		return ""
	}
	return stringValue(config["interfaceType"])
}
