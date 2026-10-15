package app

import (
	"encoding/json"
	"strings"
	"time"

	"infinite-canvas/backend/internal/model"
)

// 渠道级兜底：同一个对外型号在平台里可能挂在多条上游渠道上。
//
// 前台目录按"显示名 + 能力"把同名渠道合并成一行（见 web/src/lib/model-selection.ts 的
// groupModelsByDisplayName）：用户看到的是一个型号，看不到背后是 fal 还是 Replicate。
// 一条上游渠道出故障时，用户按下生成还是应该出片——只要这次失败已经能证明"上游没有为这次
// 请求建任务"，平台就该在同名渠道里换一条重发，而不是让这次生成直接失败。
//
// 这里只做系统渠道的直连任务。前台逻辑模型模式有自己的路由表和权重，换路由由
// switchTaskToNextRoute 负责，两条路不能互相顶掉对方的选择。

// channelNoJobEvidence 报告这次尝试能不能证明"上游没有为这次请求建任务"。
//
// 换渠道重发不复用幂等键——上游是另一家，键也不通用。所以只有"确定没建任务"的失败才允许
// 换：上游明确拒单，或边缘网关连续两次都没把请求交给源站（见 finishTaskRouteAttempt 里
// 写 rejected_no_job 的两个判据）。5xx、超时和连接中断都不在其中，它们可能已经在上游建了
// 任务，换渠道重发就是重复计费，留给既有的对账与退款路径。
func channelNoJobEvidence(attempt *model.RouteAttempt) bool {
	return attempt != nil && attempt.DispatchState == "rejected_no_job"
}

// nextChannelAttemptAfterFailure 为一次"上游没有建任务"的直连失败换一条同名平台渠道。
//
// 返回 (nil, nil) 表示这次不该换或换不了：调用方保留原始失败，交回既有的失败与退款路径。
// 找不到备用渠道不是错误——平台只有一条渠道时，这里的职责只是不要把它变成一个新的故障。
func (s *Service) nextChannelAttemptAfterFailure(task *model.Task, attempt *model.RouteAttempt) (*model.RouteAttempt, error) {
	if !channelNoJobEvidence(attempt) || task == nil || strings.TrimSpace(task.ID) == "" {
		return nil, nil
	}
	if s.repo == nil {
		return nil, nil
	}
	input, err := s.taskInputForChannelFallback(task)
	if err != nil {
		return nil, err
	}
	if input == nil {
		return nil, nil
	}
	current, err := s.currentChannelModelForDirectTask(task, input)
	if err != nil || current == nil {
		return nil, nil
	}
	attempts, err := s.repo.RouteAttempts(task.ID, task.RouteRun)
	if err != nil {
		return nil, err
	}
	candidates, err := s.fallbackChannelModelCandidates(*current, attempts)
	if err != nil {
		return nil, err
	}
	intent := ModelRequestIntentFromTaskInput(input, task.Type, task.Operation)
	for index := range candidates {
		candidate := candidates[index]
		// 同样的输入和参数，备用渠道必须也接受。否则换过去只是把一个拒绝换成另一个拒绝，
		// 用户看到的是更晚、更含糊的失败。
		spec, specErr := channelModelCapabilitySpec(candidate)
		if specErr != nil || !MatchCapability(spec, intent).Matched {
			continue
		}
		// 售价必须与被扣的那一档一致：预扣是按原渠道的价目表收的，换到更贵的渠道等于平台
		// 自己吃差价，换到更便宜的渠道等于多收用户的钱。同价是这套兜底能自动生效的前提。
		if !s.channelFallbackPriceMatches(task, input, candidate) {
			continue
		}
		variant := channelModelVariantForIntent(candidate, intent)
		next, switchErr := s.switchTaskToChannelModel(task, input, candidate, variant, len(attempts)+1)
		if switchErr != nil {
			return nil, switchErr
		}
		_ = s.log(task.UserID, task.ID, "warn", "上游未受理本次请求，换同名渠道重发", candidate.ChannelID+"::"+candidate.ModelKey)
		return next, nil
	}
	return nil, nil
}

// taskInputForChannelFallback 取出可改写、可复算计费的任务输入快照。
//
// 任务输入在库里是按字段加密的（apiKey 这类），改写前必须先解密，改写后由
// switchTaskToChannelModel 重新加密，不能把明文密钥写回磁盘。
func (s *Service) taskInputForChannelFallback(task *model.Task) (map[string]any, error) {
	raw, err := s.decryptTaskInputJSON(task.InputJSON)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		// 输入快照读不出来时不能改写：那条任务只能按原样失败，由用户自己重试。
		return nil, err
	}
	return input, nil
}

// currentChannelModelForDirectTask 找出这条任务当前真正落点上的渠道模型。
//
// 落点以任务输入快照为准，而不是 attempt 上的记录：直连任务的 attempt 一直是"不记渠道"
// 的写法，历史行没有这个字段，只有输入快照里的 channelId + channelModelKey 能定位它。
func (s *Service) currentChannelModelForDirectTask(task *model.Task, input map[string]any) (*model.ChannelModel, error) {
	config, _ := input["config"].(map[string]any)
	channelID := strings.TrimSpace(stringValue(config["channelId"]))
	modelKey := strings.TrimPrefix(strings.TrimSpace(stringValue(firstNonNilValue(config["channelModelKey"], config["model"]))), "models/")
	if channelID != "" && modelKey != "" {
		item, err := s.repo.ChannelModelByKey(channelID, modelKey)
		if err == nil && item != nil {
			return item, nil
		}
	}
	if strings.TrimSpace(task.ChannelModelID) != "" {
		return s.repo.ChannelModel(task.ChannelModelID)
	}
	return nil, nil
}

// firstNonNilValue 按顺序取第一个非空字符串值：输入快照里渠道模型标识可能写在
// channelModelKey，也可能是回退用的 model。
func firstNonNilValue(values ...any) string {
	for _, value := range values {
		if text := strings.TrimSpace(stringValue(value)); text != "" {
			return text
		}
	}
	return ""
}

// fallbackChannelModelCandidates 找出同一个对外型号挂在其他平台渠道上的模型。
//
// 分组口径与前台目录一致：显示名 + 能力。平台渠道名对用户不可见，同名渠道在目录里就是
// 一行，兜底必须落在同一行里，否则用户会拿到另一个型号的产物。
// 顺序按渠道顺序（channel sort_order）——那也是目录的默认落点顺序。
// 已经试过的渠道模型会被排除：一轮任务里每条渠道只发一次，避免两条渠道之间来回重发。
func (s *Service) fallbackChannelModelCandidates(current model.ChannelModel, attempts []model.RouteAttempt) ([]model.ChannelModel, error) {
	name, capability := channelModelGroupKey(current)
	if name == "" {
		// 没有显示名的渠道模型无法安全分组：同名判断失去依据，宁可不兜底。
		return nil, nil
	}
	tried := make(map[string]bool, len(attempts))
	for _, attempt := range attempts {
		if strings.TrimSpace(attempt.ChannelModelID) != "" {
			tried[attempt.ChannelModelID] = true
		}
	}
	tried[current.ID] = true
	channels, err := s.repo.SystemChannels(false)
	if err != nil {
		return nil, err
	}
	candidates := make([]model.ChannelModel, 0, len(channels))
	for _, channel := range channels {
		if channel.ID == current.ChannelID {
			continue
		}
		items, listErr := s.repo.ChannelModels(channel.ID, false)
		if listErr != nil {
			// 一条渠道读不出来不能拖住整次兜底：其余渠道仍然可用。
			continue
		}
		for index := range items {
			item := items[index]
			if tried[item.ID] || !item.Enabled {
				continue
			}
			itemName, itemCapability := channelModelGroupKey(item)
			if itemName != name || itemCapability != capability {
				continue
			}
			candidates = append(candidates, item)
		}
	}
	return candidates, nil
}

// channelModelGroupKey 是前台目录把同名模型并成一行用的键：显示名 + 能力。
//
// 显示名来自渠道配置（fal 的 openai/gpt-image-2 与 Replicate 的 openai/gpt-image-2 都叫
// "GPT Image 2"），大小写与空白差异不该把同一行拆成两行。
func channelModelGroupKey(item model.ChannelModel) (string, string) {
	return strings.ToLower(strings.TrimSpace(item.DisplayName)), normalizeCapability(item.Capability)
}

// channelFallbackPriceMatches 校验备用渠道在本次用量上与被扣的那一档同价。
//
// 预扣用的是原渠道的价目行（"渠道::模型" + 档位 + 用量），换渠道不重新计费。价格只有相等
// 才允许自动换：更贵是平台吃差价，更便宜是多收用户的钱。任一渠道未定价（试算报错）都不换——
// 那种情况下的"相等"无从判断。
func (s *Service) channelFallbackPriceMatches(task *model.Task, input map[string]any, candidate model.ChannelModel) bool {
	if s.taskCreditLedger == nil {
		// 不计费的形态没有价目可比，兜底只看能力。
		return true
	}
	request := s.taskChargeRequest(task, input)
	charged, err := s.taskCreditLedger.QuoteTask(request)
	if err != nil {
		return false
	}
	request.ModelKey = candidate.ChannelID + "::" + candidate.ModelKey
	candidateQuote, err := s.taskCreditLedger.QuoteTask(request)
	if err != nil {
		return false
	}
	return candidateQuote.Credits == charged.Credits
}

// switchTaskToChannelModel 把这条任务的落点改写到备用渠道模型上，并落一个新的尝试。
//
// 改写的是任务输入快照里的渠道选择（channelId / channelModelKey / model / 规格变体），
// 与前台逻辑模型换路由的写法保持一致：先解密、再改写、再加密回去，最后原子地换掉
// channel_model_id 与 input_json。换不掉（并发改写）时如实报错，由调用方保留原始失败。
func (s *Service) switchTaskToChannelModel(task *model.Task, input map[string]any, candidate model.ChannelModel, variant *model.ChannelModelVariant, attemptNumber int) (*model.RouteAttempt, error) {
	routed := &RoutedModel{ChannelModel: candidate, Variant: variant}
	nextInput := applyRoutedProviderSelection(input, routed)
	if err := s.protectTaskSecrets(nextInput); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(nextInput)
	if err != nil {
		return nil, err
	}
	previousChannelModelID := task.ChannelModelID
	if err := s.repo.SwitchTaskChannelModelSelection(task.ID, previousChannelModelID, candidate.ID, string(encoded)); err != nil {
		return nil, err
	}
	task.ChannelModelID = candidate.ID
	task.InputJSON = string(encoded)
	// 这条新尝试是另一个上游、另一个幂等键，不可能继承上一个请求号。
	task.ProviderRequestID = ""
	return s.createChannelFallbackAttempt(task, candidate, attemptNumber)
}

func (s *Service) createChannelFallbackAttempt(task *model.Task, candidate model.ChannelModel, attemptNumber int) (*model.RouteAttempt, error) {
	id, err := s.repo.NextPrefixedID("ATTEMPT")
	if err != nil {
		return nil, err
	}
	attempt := &model.RouteAttempt{
		ID: id, TaskID: task.ID, RouteRun: task.RouteRun, AttemptNumber: attemptNumber,
		ChannelModelID: candidate.ID, ChannelID: candidate.ChannelID,
		Status: "selected", DispatchState: "not_sent", StartedAt: time.Now(),
	}
	if err := s.repo.CreateRouteAttempt(attempt); err != nil {
		return nil, err
	}
	return attempt, nil
}
