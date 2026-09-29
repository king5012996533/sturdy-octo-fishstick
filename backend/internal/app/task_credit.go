package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"infinite-canvas/backend/internal/generation"
	"infinite-canvas/backend/internal/model"
)

// 任务计费的编排层。
//
// 端口定义在这里、实现由托管装配注入，而不是让 app 直接引用认证模块：计费口径属于
// 账号域，但"什么时候该收钱、什么时候该退"属于任务域，两边只通过这个窄接口见面。
// 为 nil 表示当前形态不计费——桌面与纯本地装载没有账号库，也就没有积分账户。

// TaskChargeRequest 是一次需要计费的调用。
//
// ModelKey 是平台模型标识，Quantity 的计量单位由该模型在定价表里配置的 Unit 决定。
type TaskChargeRequest struct {
	UserID   string
	TaskID   string
	ModelKey string
	// Capability 取 text / image / video / audio，与路由意图同源。
	Capability string
	// Tier 是价格档位（见 taskChargeTier）；空表示这次调用不区分档位。
	Tier     string
	Quantity int64
	Note     string
}

// TaskChargeOutcome 是计费结果。
type TaskChargeOutcome struct {
	Credits int64
}

// TaskCreditLedger 是任务计费端口。
//
// RefundTask 不接收金额：退多少由流水决定，调用方只负责说"这个任务没跑成"。
// 让失败路径自己算金额，上游一调价就会退成一个不再等于当初扣款的值。
type TaskCreditLedger interface {
	ChargeTask(request TaskChargeRequest) (TaskChargeOutcome, error)
	RefundTask(userID string, taskID string, note string) (int64, bool, error)
}

// UseTaskCreditLedger 注入计费端口，只在装配期调用一次。
func (s *Service) UseTaskCreditLedger(ledger TaskCreditLedger) {
	if s == nil {
		return
	}
	s.taskCreditLedger = ledger
}

// TaskBillingEnabled 报告当前形态是否接了计费端口。
//
// 暴露成只读诊断而不是内部字段，是因为"这个实例到底扣不扣费"必须能被外部回答：
// 一个忘了注入计费端口的托管实例会在无人察觉的情况下白送算力，而这类缺失不会报错，
// 只会表现为收入凭空少了一块。启动日志与后台读数都靠它。
func (s *Service) TaskBillingEnabled() bool {
	return s != nil && s.taskCreditLedger != nil
}

// chargeTaskCredits 在任务落库之前预扣费用。
//
// 预扣发生在持久化之前而不是之后：先建任务再扣费，扣费失败就得把已经入库的任务改成
// 失败，而这条任务可能已经被 worker 捡走开始调上游了。反过来，扣费成功但落库失败，
// 由调用方立刻退回，窗口只有一次写库的时间。
func (s *Service) chargeTaskCredits(task *model.Task, normalizedInput map[string]any) error {
	if s == nil || s.taskCreditLedger == nil || task == nil {
		return nil
	}
	intent := ModelRequestIntentFromTaskInput(normalizedInput, task.Type, task.Operation)
	outcome, err := s.taskCreditLedger.ChargeTask(TaskChargeRequest{
		UserID:     task.UserID,
		TaskID:     task.ID,
		ModelKey:   taskChargeModelKey(normalizedInput, task),
		Capability: intent.Capability,
		Tier:       taskChargeTier(intent),
		Quantity:   taskChargeQuantity(intent),
		Note:       taskChargeNoteText(normalizedInput),
	})
	if err != nil {
		return err
	}
	// 金额不落在任务行上：流水是唯一的账，任务行再存一份就有两个真相，退款也就失去了
	// "退的就是当初扣的那笔"这个保证。这里只留一条可查的日志。
	if outcome.Credits > 0 {
		_ = s.log(task.UserID, task.ID, "info", "已预扣积分 "+strconv.FormatInt(outcome.Credits, 10), "")
	}
	return nil
}

// refundTaskCredits 退回一次任务预扣。
//
// 失败与取消都会走到这里，两条路径都可能被重放，因此必须允许"没有可退的东西"：
// 端口侧据此返回 refunded=false，不是错误。
//
// 是否该退由 taskRefundVerdict 判定，不看"任务成没成"。taskErr 是这次失败的原始错误，
// 取消与本地失败路径没有上游错误可传，传 nil。
func (s *Service) refundTaskCredits(task *model.Task, taskErr error, note string) {
	if s == nil || s.taskCreditLedger == nil || task == nil {
		return
	}
	refundable, reason := s.taskRefundVerdict(task, taskErr)
	if !refundable {
		// 不退款必须留痕：用户会问"为什么这次没退"，运维也要能看到拒退发生在哪些任务上。
		_ = s.log(task.UserID, task.ID, "warn", "预扣未退回："+note+"。原因："+reason, "")
		return
	}
	credits, refunded, err := s.taskCreditLedger.RefundTask(task.UserID, task.ID, note)
	if err != nil {
		// 退费失败不能把任务本身的终态一起吞掉：任务已经失败/取消是既成事实，
		// 这里只留下可查的痕迹，由后台按任务 ID 手工补退。
		_ = s.log(task.UserID, task.ID, "error", "积分退回失败："+err.Error(), "")
		return
	}
	if !refunded {
		return
	}
	_ = s.log(task.UserID, task.ID, "info", "已退回预扣积分 "+strconv.FormatInt(credits, 10), "")
}

// taskProviderRefundableFailures 是"上游明确回执这次生成没有产出、因而不计费"的失败类别。
//
// 白名单收得很紧是刻意的。只有上游用响应正文明确否掉这次生成，才能确认它没建任务、也没
// 扣钱。超时、连接中断、网关 5xx 一律不在名单里——它们恰恰是"上游可能已经受理并开始计费"
// 的情形，按它们退款就是把成本从用户身上挪到平台身上。
var taskProviderRefundableFailures = map[generation.FailureCategory]bool{
	generation.CategoryModerationInput:     true,
	generation.CategoryModerationReference: true,
	generation.CategoryModerationOutput:    true,
	generation.CategoryInvalidParams:       true,
	generation.CategoryContextTooLong:      true,
	generation.CategoryInputInaccessible:   true,
	generation.CategoryInputTooLarge:       true,
	generation.CategoryModelMissing:        true,
	generation.CategoryAsyncFailed:         true,
}

// taskRefundVerdict 判定一次任务预扣能不能退，以及不能退时给得出结论。
//
// 判据不是"任务是不是失败了"，而是"能不能证明上游没有受理这次请求"。聚合上游普遍按任务
// 创建即计费：请求一旦被受理，后面无论我们超时、放弃轮询还是用户取消，那笔钱都已经花出去
// 了，全额退回等于平台自己承担。
//
// 只有两种情形能证明钱没花：
//   - 请求从未离开平台（预检、路由、并发槽位、落库阶段失败，或上游明确拒绝未建任务）；
//   - 上游用响应正文明确否掉了这次生成（审核驳回、参数非法、模型缺失、异步任务失败）。
//
// 其余一律不退，包括最危险的"请求发出去了但没拿到回执"：它恰恰是最可能已经计费、又最查不
// 出结果的状态。
func (s *Service) taskRefundVerdict(task *model.Task, taskErr error) (bool, string) {
	if task == nil {
		return false, "任务标识缺失，需人工核对"
	}
	s.hydrateTaskProviderRequestID(task)
	dispatched, evidenceErr := s.taskDispatchedToProvider(task)
	if evidenceErr != nil {
		// 读不到提交记录就无法证明上游没受理。这里必须偏向平台：退错一笔是净资损，
		// 不退最多是一次申诉，后台按任务 ID 仍然能手工补退。
		return false, "读取上游提交记录失败（" + evidenceErr.Error() + "），需人工核对"
	}
	providerRequestID := strings.TrimSpace(task.ProviderRequestID)
	if providerRequestID == "" && !dispatched {
		return true, ""
	}
	if taskErr != nil && taskProviderRefundableFailures[classifyTaskFailure(taskErr).Category] {
		return true, ""
	}
	if providerRequestID == "" {
		return false, "请求已发出但提交结果不明，上游可能已受理并计费"
	}
	return false, "上游已受理（任务 " + providerRequestID + "）并已产生费用"
}

// taskDispatchedToProvider 报告这次任务是否有过"请求已经离开平台"的提交记录。
//
// 只看 task.ProviderRequestID 不够：请求发出去但回执丢失时它是空的，而那种状态恰恰最可能
// 已经在上游计费。RouteAttempt 是唯一在发请求之前就落库的提交状态，因此以它为准：
// dispatching / submission_unknown / accepted 都表示请求已经出发，只有 not_sent 和
// rejected_no_job 能证明上游没有建任务。
//
// 只看当前 route_run：任务重试会把 route_run +1，上一轮的尝试记录与这一轮无关。
func (s *Service) taskDispatchedToProvider(task *model.Task) (bool, error) {
	if task == nil || strings.TrimSpace(task.ID) == "" {
		return false, nil
	}
	if s.repo == nil {
		return false, errors.New("任务仓储未装配")
	}
	attempts, err := s.repo.RouteAttempts(task.ID, task.RouteRun)
	if err != nil {
		return false, err
	}
	for _, attempt := range attempts {
		if strings.TrimSpace(attempt.ProviderRequestID) != "" {
			return true, nil
		}
		switch attempt.DispatchState {
		case "dispatching", "submission_unknown", "accepted":
			return true, nil
		}
	}
	return false, nil
}

// taskChargeModelKey 取计费用的平台模型标识。
//
// 系统渠道用「渠道::模型」而不是裸模型名：不同渠道可能上架同名的上游 SKU，裸名会让
// 两家的价目表串在一起。前台模型模式没有渠道，此时退回逻辑模型 code。
func taskChargeModelKey(input map[string]any, task *model.Task) string {
	config, _ := input["config"].(map[string]any)
	channelID := strings.TrimSpace(stringValue(config["channelId"]))
	modelKey := strings.TrimSpace(stringValue(config["channelModelKey"]))
	if modelKey == "" {
		modelKey = strings.TrimSpace(stringValue(config["model"]))
	}
	if channelID != "" && modelKey != "" {
		return channelID + "::" + modelKey
	}
	if code := strings.TrimSpace(task.Model); code != "" {
		return code
	}
	return modelKey
}

// taskChargeQuantity 从路由意图里取本次用量。
//
// 图片按张、视频与音频按秒、文本无法在提交时得知 token 数——返回 0 交给计费端口回退成
// 一个单位（按次预扣）。这里刻意不猜 token：编一个数字出来只会让账单看起来精确，
// 实际上对不上。真正的 token 级结算需要等任务侧记录用量之后再做。
func taskChargeQuantity(intent ModelRequestIntent) int64 {
	switch normalizeCapability(intent.Capability) {
	case "image":
		return optionQuantity(intent.Options, "count")
	case "video", "audio":
		return optionQuantity(intent.Options, "videoSeconds")
	default:
		return 0
	}
}

// taskChargeTier 取本次调用落在哪个价格档位。
//
// 只有图片在提交时就能确定档位：上游按 quality 的 low / medium / high 分别定价，价差在
// 十倍量级，不按档取价就必然有一头算错。这里只认上游真实存在的三个档位：
//
//   - 面板选 auto（或压根没带 quality）时返回空档，由定价侧去要"不区分质量"那一行；
//     绝不回落到某个具体档位——那等于用一个自己没验过的成本出货，正是"按最低价卖 4K"
//     这类资损的来源；
//   - 认不出的取值同样回空档。模型能力白名单会在更前面挡住这类参数，走到这里说明
//     渠道配置本身有问题，宁可要一个"未定价"的可见错误，也不要猜一个档位。
//
// 文本的 token 档位（缓存命中 / 未命中 / 输出）在提交时还不知道，要等用量回执才能结算，
// 因此这里不返回档位；视频与音频目前只有一个价。
func taskChargeTier(intent ModelRequestIntent) string {
	if normalizeCapability(intent.Capability) != "image" {
		return ""
	}
	raw, ok := intent.Options["quality"]
	if !ok || raw == nil {
		return ""
	}
	switch tier := strings.ToUpper(strings.TrimSpace(fmt.Sprint(raw))); tier {
	case "LOW", "MEDIUM", "HIGH":
		return tier
	default:
		return ""
	}
}

// optionQuantity 把能力参数转成正整数用量，解析不出来一律回 0。
//
// 面板传上来的数值可能是字符串："10" 与 10 都是合法输入，而把解析失败当成 0 而不是
// 报错，是因为计费端口对 0 的定义是"用量未知按一个单位计"，不是"免费"。
func optionQuantity(options map[string]any, key string) int64 {
	raw, ok := options[key]
	if !ok || raw == nil {
		return 0
	}
	switch typed := raw.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case string:
		value, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err != nil || value < 0 {
			return 0
		}
		return value
	default:
		return 0
	}
}

// taskChargeNoteText 取一句能贴进流水备注的说明。
//
// 只摘模型展示名，不摘完整 prompt：流水备注是给用户对账用的，把创作内容复制进去
// 等于把作品片段写进账单。
func taskChargeNoteText(input map[string]any) string {
	config, _ := input["config"].(map[string]any)
	if name := strings.TrimSpace(stringValue(config["model"])); name != "" {
		return name
	}
	return ""
}
