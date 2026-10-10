package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/protocol"
	"infinite-canvas/backend/internal/repository"
)

const providerTaskRecoveryLeaseDuration = 10 * time.Minute

// providerRecoveryLeaseOwnerPrefix 是"正在向上游查这条任务"的租约前缀。
//
// 人工恢复和自动对账共用它：两条路读的是同一份上游任务，并发跑会把同一份产物登记两遍。
// repository.ClaimFailedTaskProviderRecovery 的互斥条件就是按这个前缀写的。
const providerRecoveryLeaseOwnerPrefix = "manual-recovery:"

type ProviderTaskQueryResult struct {
	Task           *model.Task `json:"task"`
	ProviderStatus string      `json:"providerStatus"`
	Recovered      bool        `json:"recovered"`
}

func (s *Service) QueryFailedVideoTask(ctx context.Context, userID string, taskID string) (*ProviderTaskQueryResult, error) {
	task, err := s.repo.TaskForUser(strings.TrimSpace(userID), strings.TrimSpace(taskID))
	if err != nil {
		return nil, err
	}
	return s.queryFailedVideoTask(ctx, task, strings.TrimSpace(userID))
}

// AdminRecoverFailedVideoTask 用人工提供的上游任务号取回失败视频任务的结果。
//
// 存在的理由：聚合上游的创建接口是阻塞式的，偶发"已经建了任务却回了 5xx"。这时平台
// 手里没有上游任务号，PollStage 停在 submission_unknown，用户既拿不到产物、又不能重试
// （重试等于重复计费）。上游不提供任务列表接口，所以唯一的补救入口是人工从上游工作台
// 抄下任务号。这条路只对管理员开放：平台所有账号共用同一个上游账号，普通用户能随意
// 指定任务号就等于能把别人的产物拉进自己的画布。
func (s *Service) AdminRecoverFailedVideoTask(ctx context.Context, actor *model.User, taskID string, providerRequestID string) (*ProviderTaskQueryResult, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	taskID = strings.TrimSpace(taskID)
	providerRequestID = strings.TrimSpace(providerRequestID)
	if taskID == "" {
		return nil, BadAuthRequest("缺少任务号")
	}
	if providerRequestID == "" {
		return nil, BadAuthRequest("请填写上游任务号")
	}
	task, err := s.repo.Task(taskID)
	if err != nil {
		return nil, err
	}
	// 先落到任务上再查询：查询路径读的就是这个字段，成功后的产物登记也依赖它。
	task.ProviderRequestID = providerRequestID
	result, err := s.queryFailedVideoTask(ctx, task, "")
	if err != nil {
		return nil, err
	}
	if err := s.appendAdminAudit(actor, "task.retrieve_provider_result", "task", task.ID, "人工凭上游任务号取回失败视频", map[string]any{
		"providerRequestId": providerRequestID, "providerStatus": result.ProviderStatus, "recovered": result.Recovered,
	}); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) AdminQueryFailedVideoTask(ctx context.Context, actor *model.User, logID string) (*ProviderTaskQueryResult, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	log, err := s.repo.APICallLog(strings.TrimSpace(logID))
	if err != nil {
		return nil, err
	}
	if log.Capability != "video" || strings.TrimSpace(log.TaskID) == "" {
		return nil, BadAuthRequest("该请求没有可查询的视频任务")
	}
	task, err := s.repo.Task(log.TaskID)
	if err != nil {
		return nil, err
	}
	if task.UserID != log.UserID {
		return nil, BadAuthRequest("请求与任务归属不一致")
	}
	if task.ProviderRequestID == "" {
		task.ProviderRequestID = strings.TrimSpace(log.ProviderRequestID)
	}
	result, err := s.queryFailedVideoTask(ctx, task, "")
	if err != nil {
		return nil, err
	}
	if err := s.appendAdminAudit(actor, "api_log.query_provider_task", "task", task.ID, "人工查询失败视频任务", map[string]any{
		"apiCallLogId": log.ID, "providerRequestId": task.ProviderRequestID, "providerStatus": result.ProviderStatus, "recovered": result.Recovered,
	}); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) queryFailedVideoTask(ctx context.Context, task *model.Task, claimUserID string) (*ProviderTaskQueryResult, error) {
	ctx = withProtocolRegistry(ctx, s.protocolRegistry())
	if task == nil || task.ID == "" {
		return nil, BadAuthRequest("任务不存在")
	}
	if task.Status != model.TaskStatusFailed {
		return nil, BadAuthRequest("只能人工查询状态为失败的任务")
	}
	if !isProviderVideoTaskType(task.Type) {
		return nil, BadAuthRequest("该任务不是视频生成任务")
	}
	recovery, err := s.prepareProviderVideoRecovery(ctx, task)
	if err != nil {
		var unavailable providerRecoveryUnavailableError
		if errors.As(err, &unavailable) {
			return nil, BadAuthRequest(unavailable.Error())
		}
		return nil, err
	}

	owner := providerRecoveryLeaseOwnerPrefix + newID()
	if err := s.repo.ClaimFailedTaskProviderRecovery(task.ID, claimUserID, owner, providerTaskRecoveryLeaseDuration); err != nil {
		if errors.Is(err, repository.ErrTaskProviderRecoveryConflict) {
			return nil, &AuthError{Status: 409, Message: "该任务正在查询上游状态，请稍后再试"}
		}
		return nil, err
	}
	task.LeaseOwner = owner
	defer func() {
		if releaseErr := s.repo.ReleaseTaskProviderRecovery(task.ID, owner); releaseErr != nil {
			_ = s.log(task.UserID, task.ID, "error", "人工查询租约释放失败", releaseErr.Error())
		}
	}()
	return s.runProviderVideoRecovery(ctx, task, recovery, "人工查询")
}

// providerRecoveryUnavailableError 表示这次恢复是"问不了上游"，而不是"问出了错"：
// 平台手里没有上游任务号，或当前协议不支持安全查询。两者都不是临时故障，重试不会变好，
// 调用方要据此走各自的兜底（人工填任务号、或按无产出结清），而不是重排一次。
type providerRecoveryUnavailableError struct{ message string }

func (e providerRecoveryUnavailableError) Error() string { return e.message }

func isProviderVideoTaskType(taskType string) bool {
	taskType = strings.TrimSpace(taskType)
	return strings.HasPrefix(taskType, "canvas_video") || strings.HasPrefix(taskType, "video_")
}

// providerVideoRecovery 是一次上游恢复查询已经解析好的执行条件。
type providerVideoRecovery struct {
	input     canvasGenerationInput
	adapter   protocol.Adapter
	beefVideo bool
}

// prepareProviderVideoRecovery 校验任务可查、解析上游配置，并把上游任务号落回任务。
//
// 它刻意不碰租约：调用方可能已经持有对账租约，重复领取会把自己挡在门外。
func (s *Service) prepareProviderVideoRecovery(ctx context.Context, task *model.Task) (providerVideoRecovery, error) {
	s.hydrateTaskProviderRequestID(task)
	providerRequestID := strings.TrimSpace(task.ProviderRequestID)
	if providerRequestID == "" {
		return providerVideoRecovery{}, providerRecoveryUnavailableError{message: "该任务没有可恢复的上游任务 ID"}
	}
	decryptedInput, err := s.decryptTaskInputJSON(task.InputJSON)
	if err != nil {
		return providerVideoRecovery{}, fmt.Errorf("读取任务配置失败：%w", err)
	}
	var input canvasGenerationInput
	if err := json.Unmarshal([]byte(decryptedInput), &input); err != nil {
		return providerVideoRecovery{}, fmt.Errorf("任务输入解析失败：%w", err)
	}
	config, err := s.resolveProviderConfig(input.Config)
	if err != nil {
		return providerVideoRecovery{}, err
	}
	lookupCtx := ensureOfficialProtocolAdapter(ctx, config.InterfaceType)
	adapter, declarative := declarativeProtocolAdapterForContext(lookupCtx, config.InterfaceType)
	beefVideo := isBeefAPIVideoConfig(config) && isSeedanceVideoConfig(config)
	if !declarative && !beefVideo {
		return providerVideoRecovery{}, providerRecoveryUnavailableError{message: "该任务的请求协议不支持安全查询上游状态"}
	}
	input.Config = config
	task.InputJSON = decryptedInput
	task.ProviderRequestID = providerRequestID
	if err := s.repo.UpdateTaskProviderState(task.ID, providerRequestID, task.PollStage, task.NextPollAt); err != nil {
		return providerVideoRecovery{}, err
	}
	return providerVideoRecovery{input: input, adapter: adapter, beefVideo: beefVideo}, nil
}

// runProviderVideoRecovery 向已确认可查的上游问一次结果，出片时下载、入库并登记项目产物。
//
// 返回 Recovered=false 表示上游仍在处理：任务保持失败态，等下一轮再问。上游已成功后的
// 媒体下载可能持续数十秒，浏览器关闭抽屉、页面刷新或代理断开都不应中断它，因此这里用
// 恢复租约控制的上下文，而不是发起这次查询的请求上下文。
func (s *Service) runProviderVideoRecovery(ctx context.Context, task *model.Task, recovery providerVideoRecovery, action string) (*ProviderTaskQueryResult, error) {
	ctx = ensureOfficialProtocolAdapter(ctx, recovery.input.Config.InterfaceType)
	recoveryCtx, cancelRecovery := providerTaskRecoveryContext(ctx)
	defer cancelRecovery()
	queryCtx := withProviderAnalytics(recoveryCtx, s, *task)
	var result map[string]interface{}
	var providerStatus string
	var err error
	if recovery.beefVideo {
		result, providerStatus, err = queryBeefAPIVideoResult(queryCtx, recovery.input, task.ProviderRequestID)
	} else {
		result, providerStatus, err = queryProtocolAdapterVideoTask(queryCtx, recovery.input, recovery.adapter, task.ProviderRequestID)
	}
	if err != nil {
		_ = s.log(task.UserID, task.ID, "error", action+"上游视频任务失败", err.Error())
		return nil, err
	}
	if result == nil {
		_ = s.log(task.UserID, task.ID, "info", action+"完成，上游任务仍在处理", providerStatus)
		return &ProviderTaskQueryResult{Task: taskForOutput(*task), ProviderStatus: providerStatus, Recovered: false}, nil
	}

	result, err = s.persistGeneratedMediaResultForTask(task, result)
	if err != nil {
		_ = s.log(task.UserID, task.ID, "error", action+"已取得视频，但结果保存失败", err.Error())
		return nil, err
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	task.Error = ""
	task.PollStage = strings.ToLower(providerStatus)
	task.NextPollAt = nil
	if err := s.saveTaskCompletionWithinStorageQuota(task, resultJSON, nil, false); err != nil {
		_ = s.log(task.UserID, task.ID, "error", action+"已取得视频，但任务恢复失败", err.Error())
		return nil, err
	}
	if err := s.RegisterTaskOutputFromTask(*task); err != nil {
		_ = s.log(task.UserID, task.ID, "error", "任务恢复成功但项目产物登记失败", err.Error())
		return nil, fmt.Errorf("任务已恢复，但项目素材登记失败：%w", err)
	}
	_ = s.log(task.UserID, task.ID, "info", action+"确认生成成功，任务已恢复并登记项目产物", providerStatus)
	return &ProviderTaskQueryResult{Task: taskForOutput(*task), ProviderStatus: providerStatus, Recovered: true}, nil
}

// Query-only: never route recovery through create, even when the original
// persisted channel metadata names the legacy generations protocol.
func queryBeefAPIVideoResult(ctx context.Context, input canvasGenerationInput, id string) (map[string]interface{}, string, error) {
	var state map[string]interface{}
	if err := getJSON(ctx, input.Config, "/videos/"+id, &state); err != nil {
		return nil, "", err
	}
	if nested, ok := state["data"].(map[string]interface{}); ok {
		state = nested
	}
	status, videoURL := seedancePollStatusAndURL(state)
	if status == "failed" || status == "cancelled" || status == "expired" {
		// 用 providerTaskFailedError 而不是裸 errors.New：上游明确终止的结论要能被
		// 归类成"异步任务失败"，否则退款判据看不出"上游没有产出"，会把一次确定的
		// 失败当成临时错误反复重查。
		return nil, status, protocolResultError(defaultString(seedanceErrorMessage(state), "视频生成失败"), id)
	}
	if status != "completed" && status != "succeeded" {
		return nil, status, nil
	}
	data, mimeType, err := runVideoDownload(ctx, id, defaultVideoPollPolicy(), func(ctx context.Context) ([]byte, string, error) {
		if videoURL != "" {
			return getProviderExternalBinary(withProviderRequestKind(ctx, "download"), input.Config, videoURL)
		}
		return getBinary(withProviderRequestKind(ctx, "download"), input.Config, "/videos/"+id+"/content")
	})
	if err != nil {
		return nil, status, err
	}
	return map[string]interface{}{"mode": "video", "video": map[string]interface{}{"dataUrl": dataURL(mimeType, data), "mimeType": mimeType}}, status, nil
}

func providerTaskRecoveryContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), providerTaskRecoveryLeaseDuration)
}
