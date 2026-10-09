package app

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"infinite-canvas/backend/internal/model"
)

type providerSubmissionKeyContext struct{}

// A lost receipt is not a rejected generation. Only gateways with a verified
// idempotency contract may replay creation with the SAME durable attempt key.
type providerSubmissionUnknownError struct{ Cause error }

func (e providerSubmissionUnknownError) Error() string {
	return fmt.Sprintf("提交结果尚未确认：%v", e.Cause)
}
func (e providerSubmissionUnknownError) Unwrap() error { return e.Cause }

func uncertainVideoSubmission(ctx context.Context, err error) error {
	if err == nil || errors.Is(err, context.Canceled) || safeRouteRejection(err) {
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

func (s *Service) createDirectTaskAttempt(task *model.Task) (*model.RouteAttempt, error) {
	if task.Attempts > 1 && task.ProviderRequestID == "" {
		return nil, routeDispatchUncertainError{"旧任务已尝试执行但缺少提交记录，为避免重复创建上游任务已停止自动重发"}
	}
	id, err := s.repo.NextPrefixedID("ATTEMPT")
	if err != nil {
		return nil, err
	}
	attempt := &model.RouteAttempt{ID: id, TaskID: task.ID, RouteRun: task.RouteRun, AttemptNumber: 1, ChannelModelID: task.ChannelModelID, Status: "selected", DispatchState: "not_sent", StartedAt: time.Now()}
	if task.ProviderRequestID != "" {
		attempt.ProviderRequestID, attempt.DispatchState = task.ProviderRequestID, "accepted"
	}
	if err := s.repo.CreateRouteAttempt(attempt); err != nil {
		return nil, err
	}
	return attempt, nil
}
