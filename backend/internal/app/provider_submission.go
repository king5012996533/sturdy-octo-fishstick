package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"infinite-canvas/backend/internal/model"
)

type providerSubmissionKeyContext struct{}

// A lost receipt is not a rejected generation. Only gateways with a verified
// idempotency contract may replay creation with the SAME durable attempt key.
type providerSubmissionUnknownError struct{ Cause error }

// providerAttemptNotCreatedError 表示这次创建卡在边缘网关上，而且每一次尝试都如此。
//
// 502/503 且正文不是供应商 JSON，说明网关自己没把请求交给源站；重放的第二次同样停在网关，
// 就能断定上游从来没有为这次请求建立任务。这是"确定没有上游任务"的证据，比提交结果未知
// 更强的结论：它同时是退款判据（上游没建任务就没计费）和渠道级兜底换发的前提——换渠道
// 不复用幂等键，一旦上游其实建了任务就是重复计费。
type providerAttemptNotCreatedError struct{ Cause error }

func (e providerAttemptNotCreatedError) Error() string {
	return fmt.Sprintf("请求没有进入上游源站：%v", e.Cause)
}
func (e providerAttemptNotCreatedError) Unwrap() error { return e.Cause }

// providerAttemptNotCreated 报告这次失败是不是"每次尝试都停在边缘网关"。
func providerAttemptNotCreated(err error) bool {
	var target providerAttemptNotCreatedError
	return errors.As(err, &target)
}

func (e providerSubmissionUnknownError) Error() string {
	return fmt.Sprintf("提交结果尚未确认：%v", e.Cause)
}
func (e providerSubmissionUnknownError) Unwrap() error { return e.Cause }

// uncertainProviderSubmission 判断一次创建请求的失败能不能定性。
//
// 上游明确拒绝（401/402/403/404/429、熔断）说明这次生成没有被受理，错误可以原样向上；
// 其余瞬时失败（5xx、超时、连接中断、响应体解析不出来）都属于"请求可能已经出去、但我们
// 没拿到回执"，必须打上提交未确认的标记，才能进入事后对账而不是被当成一次普通失败丢掉。
//
// 这条判据最早只用在视频上，因为只有视频有回查入口；现在图片、音频同样走后台对账，因此
// 对这三种会建立上游任务的模式统一生效。文本任务一次性返回、没有可回查的任务号，不在其列。
func uncertainProviderSubmission(ctx context.Context, err error) error {
	if err == nil || errors.Is(err, context.Canceled) || safeRouteRejection(err) {
		return err
	}
	// 请求连源站都没进过，不属于"结果不明"：打成提交未确认会让任务卡在对账队列里等
	// 到过期才退款，而这里已经能确定上游没有建任务。
	if providerAttemptNotCreated(err) {
		return err
	}
	var circuit providerCircuitOpenError
	if errors.As(err, &circuit) {
		return err
	}
	if retry, _ := retryableVideoPollError(context.Background(), err); retry {
		return providerSubmissionUnknownError{Cause: err}
	}
	return err
}

// asyncProviderTaskMode 报告这个生成模式会不会建立"可回查的上游任务"。
//
// 只有这类任务的提交才需要区分"上游已受理"和"我们没拿到回执"：文本任务一次性返回，
// 没有任务号可查，把它按提交未确认处理只会让任务卡在一个既对不了账、也说不清的状态里。
func asyncProviderTaskMode(mode string) bool {
	switch strings.TrimSpace(mode) {
	case "video", "image", "audio":
		return true
	}
	return false
}

// providerDispatchRecord 记录"这次尝试是否真的向上游发出过请求"。
//
// 退款判据（Service.taskRefundVerdict）只认"能不能证明上游没有受理这次请求"。提交记录停在
// submission_unknown 时，请求可能已经出去、也可能根本没出去：本地预检、任务输入解析、
// 请求体构建这些失败都发生在发出请求之前。有了这个进程内标记，就能把"从未发出"和"发出去了
// 但没拿到回执"分开——前者上游不可能建任务，也就不可能计费。
type providerDispatchRecord struct{ issued atomic.Bool }

type providerDispatchRecordContext struct{}

func withProviderDispatchRecord(ctx context.Context, record *providerDispatchRecord) context.Context {
	if record == nil {
		return ctx
	}
	return context.WithValue(ctx, providerDispatchRecordContext{}, record)
}

// markProviderRequestIssued 在真正发起出站请求之前调用。它只表示"我们已经尝试发出"，
// 因此连接层面的失败仍算已发出——那种情况下上游可能已经受理并计费。
func markProviderRequestIssued(ctx context.Context) {
	if record, _ := ctx.Value(providerDispatchRecordContext{}).(*providerDispatchRecord); record != nil {
		record.issued.Store(true)
	}
}

func (r *providerDispatchRecord) requestIssued() bool {
	return r != nil && r.issued.Load()
}

func withProviderSubmissionKey(ctx context.Context, attempt *model.RouteAttempt) context.Context {
	if attempt == nil {
		return ctx
	}
	// Stable for this persisted attempt across worker/process recovery; an
	// explicitly new user retry or a safely rejected route gets a new attempt.
	key := uuid.NewSHA1(uuid.NameSpaceOID, []byte(attempt.TaskID+":"+attempt.ID)).String()
	return context.WithValue(ctx, providerSubmissionKeyContext{}, key)
}

// directTaskChannelModelIdentity 从任务输入快照里取出这次执行的渠道模型身份。
//
// 读不出来时返回空值：尝试行上没有渠道归属只是少一条诊断线索，不该让任务执行失败。
func (s *Service) directTaskChannelModelIdentity(task *model.Task) (string, string) {
	if task == nil || s.repo == nil {
		return "", ""
	}
	if strings.TrimSpace(task.ChannelModelID) != "" {
		if item, err := s.repo.ChannelModel(task.ChannelModelID); err == nil && item != nil {
			return item.ID, item.ChannelID
		}
		return task.ChannelModelID, ""
	}
	input, err := s.taskInputForChannelFallback(task)
	if err != nil || input == nil {
		return "", ""
	}
	item, err := s.currentChannelModelForDirectTask(task, input)
	if err != nil || item == nil {
		return "", ""
	}
	return item.ID, item.ChannelID
}

func (s *Service) createDirectTaskAttempt(task *model.Task) (*model.RouteAttempt, error) {
	if task.Attempts > 1 && task.ProviderRequestID == "" {
		return nil, routeDispatchUncertainError{"旧任务已尝试执行但缺少提交记录，为避免重复创建上游任务已停止自动重发"}
	}
	id, err := s.repo.NextPrefixedID("ATTEMPT")
	if err != nil {
		return nil, err
	}
	// 直连任务的落点只有输入快照里写着（channelId + channelModelKey），任务行上的
	// channel_model_id 一直是空的。这里把它落到尝试行上，渠道级兜底才能认出"哪些渠道已经
	// 发过"，不会在两条渠道之间来回重发。
	channelModelID, channelID := s.directTaskChannelModelIdentity(task)
	attempt := &model.RouteAttempt{ID: id, TaskID: task.ID, RouteRun: task.RouteRun, AttemptNumber: 1, ChannelModelID: channelModelID, ChannelID: channelID, Status: "selected", DispatchState: "not_sent", StartedAt: time.Now()}
	if task.ProviderRequestID != "" {
		attempt.ProviderRequestID, attempt.DispatchState = task.ProviderRequestID, "accepted"
	}
	if err := s.repo.CreateRouteAttempt(attempt); err != nil {
		return nil, err
	}
	return attempt, nil
}
