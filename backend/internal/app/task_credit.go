package app

import (
	"strconv"
	"strings"

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
	Quantity   int64
	Note       string
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
func (s *Service) refundTaskCredits(userID string, taskID string, note string) {
	if s == nil || s.taskCreditLedger == nil {
		return
	}
	credits, refunded, err := s.taskCreditLedger.RefundTask(userID, taskID, note)
	if err != nil {
		// 退费失败不能把任务本身的终态一起吞掉：任务已经失败/取消是既成事实，
		// 这里只留下可查的痕迹，由后台按任务 ID 手工补退。
		_ = s.log(userID, taskID, "error", "积分退回失败："+err.Error(), "")
		return
	}
	if !refunded {
		return
	}
	_ = s.log(userID, taskID, "info", "已退回预扣积分 "+strconv.FormatInt(credits, 10), "")
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
