package app

import (
	"encoding/json"
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
	// SurchargeCredits 是与用量无关的附加费（积分），加在"单价 × 用量"之外。
	// 目前唯一的来源是参考图超出免费额度后的按张加收，见 task_credit_reference_image.go。
	SurchargeCredits int64
	// SurchargeNote 说明这笔附加费是什么：流水要能独立回答"这笔钱是怎么来的"。
	SurchargeNote string
	Note          string
}

// TaskChargeOutcome 是一次计费的金额，以及这笔钱是怎么算出来的。
//
// 单价与用量不是为了在任务域里复算金额——金额只有账号域算得对——而是为了让生成前的
// 试算能写出"450 积分 = 15 秒 × 30 积分/秒"这种用户能自己复核的算式。ChargeTask 只
// 用得到 Credits，其余字段由试算接口原样透出。
type TaskChargeOutcome struct {
	Credits int64 `json:"credits"`
	// Unit 是计价单位（IMAGE / SECOND / TOKEN_1M / REQUEST），Quantity 是本次用量。
	Unit     string `json:"unit"`
	Quantity int64  `json:"quantity"`
	// SurchargeCredits 是 Credits 里"单价 × 用量"之外的那部分（如参考图超量加收）。
	SurchargeCredits int64 `json:"surchargeCredits"`
	// SellUnitPrice 为 nil 表示没有可展示的单价（未定价）。
	SellUnitPrice    *int64 `json:"sellUnitPrice"`
	MultiplierBp     int    `json:"multiplierBp"`
	MultiplierSource string `json:"multiplierSource"`
	// Priced 为 false 表示这个模型（或这个档位）还没定价。
	Priced bool `json:"priced"`
	// MinimumBalance 是这次提交需要保留的最低余额（积分），0 表示没有这条要求。
	// 与 Credits 是两件事：Credits 是"这次扣多少"，MinimumBalance 是"余额得有这么多才
	// 允许开始"。只有文本会给非零值，原因见 TaskCreditLedger.EnsureTextTaskBalance。
	MinimumBalance int64 `json:"minimumBalance"`
}

// TaskTextSettleRequest 是一次文本任务按实际用量的结算入参。
//
// 三档用量分开传而不是只传一个总数：缓存命中价与未命中价差一个量级，压成一个数就
// 会把大半输入按最贵的那档收。InputTokens 含 CachedTokens。
type TaskTextSettleRequest struct {
	UserID       string
	TaskID       string
	ModelKey     string
	InputTokens  int64
	CachedTokens int64
	OutputTokens int64
}

// TaskTextSettleOutcome 是一次文本结算的金额与结论。
//
// Credits 是按实际用量算出的总价，Delta 是本次补扣额。两者都回传是为了让调用方写得出
// "起步价 1 分 + 补扣 13 分 = 14 分"这种能自己复核的日志。
type TaskTextSettleOutcome struct {
	Credits int64 `json:"credits"`
	Charged int64 `json:"charged"`
	Delta   int64 `json:"delta"`
	// Priced 为 false 表示这个模型没有任何文本价目，本次没有结算。
	Priced bool `json:"priced"`
	// MissingTiers 非空表示有档位没取到价，这次结算少收了钱，调用方必须落日志。
	MissingTiers []string `json:"missingTiers"`
	// Uncollected 是余额不够、这次收不回来的差额（积分）。
	Uncollected int64  `json:"uncollected"`
	Note        string `json:"note"`
}

// TaskCreditLedger 是任务计费端口。
//
// RefundTask 不接收金额：退多少由流水决定，调用方只负责说"这个任务没跑成"。
// 让失败路径自己算金额，上游一调价就会退成一个不再等于当初扣款的值。
//
// QuoteTask 与 ChargeTask 收同一份入参、走同一条取价路径，区别只有一个写不写账：
// 试算要是另走一套算价逻辑，"面板上的价"和"实扣的价"迟早会对不上。
//
// SettleTextTask 是提交时无法预扣足额的唯一补偿口：文本的 token 用量要等上游回执，
// 预扣只能是起步价，成功收尾时按真实用量补差额。它必须在端口上而不是由 app 自己算，
// 理由与 ChargeTask 相同——金额只有账号域算得对。
type TaskCreditLedger interface {
	ChargeTask(request TaskChargeRequest) (TaskChargeOutcome, error)
	QuoteTask(request TaskChargeRequest) (TaskChargeOutcome, error)
	RefundTask(userID string, taskID string, note string) (int64, bool, error)
	SettleTextTask(request TaskTextSettleRequest) (TaskTextSettleOutcome, error)
	// EnsureTextTaskBalance 校验文本任务的最低余额水位，余额不足返回 402。
	//
	// 单列一个方法而不是并进 ChargeTask，是因为"什么时候要过水位"是任务域的判断：
	// 用户主动发起的那一次提交要过，会话进行中的续跑步骤不过（见 taskChargesTextMinimumBalance）。
	// 计费域只回答"这个模型要多少余额"。
	EnsureTextTaskBalance(userID string, modelKey string) error
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
	request := s.taskChargeRequest(task, normalizedInput)
	// 水位先于预扣：文本的预扣只有一个起步价，挡不住"余额 1 积分也能发起一轮长会话"。
	// 放在这里而不是各个入口，是因为所有计费都走这一条路，漏一处就是一个可以绕开的洞。
	if taskChargesTextMinimumBalance(*task, request.Capability) {
		if err := s.taskCreditLedger.EnsureTextTaskBalance(request.UserID, request.ModelKey); err != nil {
			return err
		}
	}
	outcome, err := s.taskCreditLedger.ChargeTask(request)
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

// taskChargesTextMinimumBalance 判断这次提交要不要先满足文本的最低余额水位。
//
// 只有用户主动发起的那一轮要过水位。会话进行中的续跑步骤（cloud_agent_step）与后台的
// 记忆压缩（agent_memory_compact）不过：它们不是用户此刻按下发送产生的消费，而是已经
// 放行的会话在继续。把水位压到它们头上，会让一条正常跑到一半的会话因为余额降到水位
// 以下而中途断掉——那种"钱花完了所以把你的对话截断"的体验，比收不回最后一轮的钱更糟，
// 而收不回的那部分现在有欠款清单兜底。
func taskChargesTextMinimumBalance(task model.Task, capability string) bool {
	if normalizeCapability(capability) != "text" {
		return false
	}
	switch strings.TrimSpace(task.Operation) {
	case "cloud_agent_step", cloudAgentMemoryCompactOp:
		return false
	default:
		return true
	}
}

// quoteTaskCredits 试算一次任务消耗，不写流水也不改余额。
//
// 走的是与 chargeTaskCredits 完全相同的取价入参：模型标识、能力、档位与用量四项都从
// taskChargeRequest 出。少传其中任何一项，试算都会按另一个价目行取价，而那种偏差不会报错，
// 只会让用户看到两个不一样的数。
func (s *Service) quoteTaskCredits(task *model.Task, normalizedInput map[string]any) (*TaskChargeOutcome, error) {
	if s == nil || s.taskCreditLedger == nil || task == nil {
		// 只有托管形态会注入计费端口；走到这里说明装配漏了，不能回一个 0 让前端
		// 显示"本次免费"。
		return nil, &AppError{Status: 503, Code: 503, Message: "当前实例未启用计费，无法试算消耗"}
	}
	outcome, err := s.taskCreditLedger.QuoteTask(s.taskChargeRequest(task, normalizedInput))
	if err != nil {
		return nil, err
	}
	return &outcome, nil
}

// taskChargeRequest 从任务本身与它的输入推导出计费入参，提交与试算共用。
func (s *Service) taskChargeRequest(task *model.Task, normalizedInput map[string]any) TaskChargeRequest {
	intent := ModelRequestIntentFromTaskInput(normalizedInput, task.Type, task.Operation)
	surcharge, surchargeNote := s.referenceImageSurcharge(normalizedInput)
	return TaskChargeRequest{
		UserID:           task.UserID,
		TaskID:           task.ID,
		ModelKey:         taskChargeModelKey(normalizedInput, task),
		Capability:       intent.Capability,
		Tier:             taskChargeTier(intent),
		Quantity:         s.taskChargeQuantityFor(task, normalizedInput, intent),
		SurchargeCredits: surcharge,
		SurchargeNote:    surchargeNote,
		Note:             taskChargeNoteText(normalizedInput),
	}
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

// settleTextTaskCredits 在文本任务成功收尾时按上游回执的 token 用量补扣差额。
//
// 文本的 token 用量提交时定不了，预扣只能是一个起步价。少了这一步，一次带着几万 token
// 上下文的 Agent 会话会按起步价成交——平台每跑一轮亏一轮，而且账面上那一行与正常扣费
// 长得一模一样，只能靠对账发现问题。所以这里不是"优化"，是计费闭环里缺的那一半。
//
// 只处理成功收尾：失败与取消走退款路径，两条路径都动账会出现"退了一笔又补扣一笔"。
// 结算本身必须幂等（任务收尾可能被重放），幂等由账号域按 (任务, 结算) 的唯一键保证。
func (s *Service) settleTextTaskCredits(task *model.Task) {
	if s == nil || s.taskCreditLedger == nil || s.repo == nil || task == nil {
		return
	}
	inputJSON, err := s.decryptTaskInputJSON(task.InputJSON)
	if err != nil {
		_ = s.log(task.UserID, task.ID, "error", "文本结算无法读取任务输入："+err.Error(), "")
		return
	}
	var normalizedInput map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &normalizedInput); err != nil {
		_ = s.log(task.UserID, task.ID, "error", "文本结算无法解析任务输入："+err.Error(), "")
		return
	}
	// 能力从与预扣同源的那份解析里取：预扣按 text 收的，结算就按 text 补，两边不会分叉。
	intent := ModelRequestIntentFromTaskInput(normalizedInput, task.Type, task.Operation)
	if normalizeCapability(intent.Capability) != "text" {
		return
	}
	usage, err := s.repo.TextTaskTokenUsage(task.ID)
	if err != nil {
		_ = s.log(task.UserID, task.ID, "error", "文本结算读取用量失败："+err.Error(), "")
		return
	}
	if usage.Calls == 0 {
		// 没有上游调用就没有可结算的用量：文本回放草稿、未触达上游的任务都走这条路。
		return
	}
	if usage.UsageCalls == 0 {
		// 有调用却一条用量回执都没有：按 0 结算等于这次白送，必须留痕由人工核对上游账单。
		_ = s.log(task.UserID, task.ID, "warn", "文本任务有 "+strconv.FormatInt(usage.Calls, 10)+" 次上游调用但没有任何用量回执，本次未结算", "")
		return
	}
	outcome, err := s.taskCreditLedger.SettleTextTask(TaskTextSettleRequest{
		UserID:       task.UserID,
		TaskID:       task.ID,
		ModelKey:     taskChargeModelKey(normalizedInput, task),
		InputTokens:  usage.Input,
		CachedTokens: usage.Cached,
		OutputTokens: usage.Output,
	})
	if err != nil {
		// 结算失败不影响任务终态：结果已经落库、用户已经拿到东西，这里只留可查的痕迹，
		// 由后台按任务 ID 手工补扣。
		_ = s.log(task.UserID, task.ID, "error", "文本积分结算失败："+err.Error(), "")
		return
	}
	if !outcome.Priced {
		_ = s.log(task.UserID, task.ID, "warn", "文本结算未取到任何价目，本次未结算", "")
		return
	}
	if len(outcome.MissingTiers) > 0 {
		_ = s.log(task.UserID, task.ID, "warn", "文本结算缺少价目档位："+strings.Join(outcome.MissingTiers, "、")+"，这部分未计费", "")
	}
	if outcome.Delta <= 0 {
		// 起步价已经盖住实际用量，没有差额可补。仍然记一条 info：它回答了"为什么这次
		// 只有 1 积分"，用户拿着短对话来问时不用去猜。
		_ = s.log(task.UserID, task.ID, "info", "文本用量未超出起步价，按起步价成交", outcome.Note)
		return
	}
	if usage.UsageCalls < usage.Calls {
		_ = s.log(task.UserID, task.ID, "warn", "文本结算有 "+strconv.FormatInt(usage.Calls-usage.UsageCalls, 10)+" 次调用没有用量回执，本次只结算已回执部分", "")
	}
	if outcome.Uncollected > 0 {
		// 余额不够，这一轮的钱收不回来。任务已经成功、结果已经给到用户，不能在日志里
		// 含糊过去：后台要能按"未收金额"找到它，用户充值后由运维决定是补收还是核销。
		_ = s.log(task.UserID, task.ID, "warn", "文本结算未收金额 "+strconv.FormatInt(outcome.Uncollected, 10)+" 积分（余额不足），已记入后台欠款清单", outcome.Note)
		return
	}
	_ = s.log(task.UserID, task.ID, "info", "已按用量补扣积分 "+strconv.FormatInt(outcome.Delta, 10), outcome.Note)
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
// 一个单位（按次起步价预扣）。这里刻意不猜 token：编一个数字出来只会让账单看起来精确，
// 实际上对不上。真实用量由成功收尾时的结算补上，见 settleTextTaskCredits。
func taskChargeQuantity(intent ModelRequestIntent) int64 {
	switch normalizeCapability(intent.Capability) {
	case "image":
		return optionQuantity(intent.Options, "count")
	case "video":
		return optionQuantity(intent.Options, "videoSeconds")
	case "audio":
		// 音频按次：这里返回 1 而不是去读 videoSeconds。配音时长由文本决定、配乐长度由上游
		// 决定，提交时都拿不到，读出来只会恒为 0 再被计费域兜底成 1——写死 1 是为了让下一个
		// 读这段代码的人不会以为音频量得出时长（见 docs/credits-billing.md 的用量口径）。
		return 1
	default:
		return 0
	}
}

// taskChargeTier 取本次调用落在哪个价格档位。
//
// 只有图片在提交时就能确定档位：上游按 quality 的 low / medium / high / xhigh / max
// 分别定价，价差到四十倍（low $0.012 对 max $0.50），不按档取价就必然有一头算错。
// 这里只认上游真实存在的档位：
//
//   - 面板选 auto（或压根没带 quality）时返回空档，由定价侧去要"不区分质量"那一行；
//     绝不回落到某个具体档位——那等于用一个自己没验过的成本出货，正是"按最低价卖 4K"
//     这类资损的来源；
//   - 认不出的取值同样回空档。模型能力白名单会在更前面挡住这类参数，走到这里说明
//     渠道配置本身有问题，宁可要一个"未定价"的可见错误，也不要猜一个档位。
//
// 视频按分辨率分档：480p 与 720p 在上游是两个价，用户在面板上选哪一档就按哪一档取价。
//
// 文本的 token 档位（缓存命中 / 未命中 / 输出）在提交时还不知道，要等用量回执才能结算，
// 因此这里不返回档位；音频只有一个价。
// 价格档位名，与 auth.PriceTier 的取值一一对应。
//
// app 不 import auth——两者只在计费端口（TaskCreditLedger）上相接，档位名是这条边界上
// 唯一要共享的词表。分成两份写是刻意的，代价由 TestAudioPriceTierMatchesPricingDomain
// 兜底：哪边改了名而另一边没跟，测试会立刻失败，而不是让一批调用静默落到"未定价"。
const (
	tierLow    = "LOW"
	tierMedium = "MEDIUM"
	tierHigh   = "HIGH"
	tierXHigh  = "XHIGH"
	tierMax    = "MAX"
	tierShort  = "SHORT"
	tierLong   = "LONG"
	// 图片尺寸档，与音频那三档同源：档位名写在这里，边界写在各档的映射文件里。
	tierSize1K = "SIZE_1K"
	tierSize2K = "SIZE_2K"
	tierSize4K = "SIZE_4K"
)

func taskChargeTier(intent ModelRequestIntent) string {
	switch normalizeCapability(intent.Capability) {
	case "image":
		// 质量优先：有 quality 参数的模型（openai 系）按上游质量档取价，与上游的
		// 价目一一对应。没有质量维度的模型（imagen、混元生图这类）落到尺寸档上，
		// 两条轴不会同时命中——能力合同不会让同一个模型既有质量档又走尺寸档。
		if raw, ok := intent.Options["quality"]; ok && raw != nil {
			if tier := imageQualityPriceTier(raw); tier != "" {
				return tier
			}
		}
		return imageSizePriceTier(intent.Options["size"])
	case "audio":
		return audioPriceTier(intent.Options["audioDuration"])
	case "video":
		return videoResolutionPriceTier(intent.Options["vquality"])
	default:
		return ""
	}
}

// videoResolutionPriceTier 把用户选的分辨率折成价格档位名（480p → 480P，768 → 768P）。
//
// 认形状而不是查固定清单：视频分辨率有 360p…2160p，还有 768p、960p 这类非标准写法，
// 写死清单会在接入新模型时漏档。
//
// 裸数字要补回尾部的 P：面板统一把分辨率写成 480 / 720 / 768 这种数字形式（见
// web/src/lib/video-generation-options.ts）。标准档位由 normalizeModelRequestOption
// 的固定表补 P，768、960 这类非标准档漏在表外，档位于是算成空档，落到「不区分」那一行，
// 表现为"照常配了 768P 的价却报未定价"。补 P 只是同一分辨率的另一种写法，不是把认不出
// 的取值猜成某一档。
//
// 面板没选（auto / 空）或取值不像分辨率时仍回空档，由「不区分」那一行兜底——回落到某个
// 具体档位等于用一个自己没验过的成本出货。
func videoResolutionPriceTier(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	resolution := strings.TrimSpace(text)
	if isAutomaticVideoResolution(resolution) {
		return ""
	}
	tier := strings.ToUpper(resolution)
	if isVideoResolutionTierShape(tier) {
		return tier
	}
	if !isBareResolutionNumber(tier) {
		return ""
	}
	// 补完仍要再判一次形状：拼出来的档位必须自己合法，否则一位两位的数字会被拼成
	// 一个不存在的档位，把本该回落到「不区分」的一次调用变成"未定价"。
	candidate := tier + "P"
	if !isVideoResolutionTierShape(candidate) {
		return ""
	}
	return candidate
}

// isBareResolutionNumber 报告一个取值是不是"只有数字的分辨率"（768、960、2160）。
//
// 下限三位：360p…2160p 都是三位以上，一位两位的数字更可能是别的参数被误传进 vquality，
// 那种情况应该留在空档，而不是拼出一个不存在的档位。
func isBareResolutionNumber(value string) bool {
	if len(value) < 3 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// isVideoResolutionTierShape 与 auth.IsVideoResolutionPriceTier 是同一条形状规则。
//
// app 不 import auth，两份写法是刻意的，一致性由 TestVideoResolutionTierMatchesPricingDomain
// 兜底：两边跑偏时取价会静默落到"未定价"，表现为"用户点生成被拒"，不会有编译错误。
func isVideoResolutionTierShape(tier string) bool {
	if len(tier) < 2 || len(tier) > 5 {
		return false
	}
	switch tier[len(tier)-1] {
	case 'P', 'K':
	default:
		return false
	}
	for _, char := range tier[:len(tier)-1] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// imageQualityPriceTier 认上游真实存在的质量档，认不出来返回空档。
//
// 绝不回落到某个具体档位——那等于用一个自己没验过的成本出货，正是"按最低价卖 4K"
// 这类资损的来源。认不出的取值说明渠道配置本身有问题，宁可要一个"未定价"的可见错误。
func imageQualityPriceTier(value any) string {
	switch tier := strings.ToUpper(strings.TrimSpace(fmt.Sprint(value))); tier {
	case tierLow, tierMedium, tierHigh, tierXHigh, tierMax:
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
