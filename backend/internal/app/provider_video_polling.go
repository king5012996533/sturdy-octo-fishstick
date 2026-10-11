package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"infinite-canvas/backend/internal/outbound"
)

const defaultVideoPollInterval = 30 * time.Second

// videoPollStaleAfter 是"上游迟迟不返回结果"的耐心上限。
//
// 超过它任务不会被判失败、也不会退款：聚合上游按建单计费，我们停下轮询不代表它没在跑，
// 更不代表那笔钱没花出去（见 Service.taskRefundVerdict）。这里只做一件事——把阶段文案
// 从"上游生成中"换成明确的"上游长时间未返回结果"，让用户知道不是我们卡住了，并且可以
// 自己决定继续等还是取消。轮询本身照常跑到任务超时。
const videoPollStaleAfter = 20 * time.Minute

const videoPollStaleStage = "上游长时间未返回结果，可继续等待或取消后重新提交"

type videoPollPolicy struct {
	InitialDelay          time.Duration
	Interval              time.Duration
	StaleAfter            time.Duration
	MaxNotFoundMisses     int
	MaxMalformedResponses int
	MaxDownloadTries      int
	RetryTransient        bool
	Sleep                 func(context.Context, time.Duration) error
	Notify                func(context.Context, string, videoPollEvent, error)
}

type videoPollEvent string

const (
	videoPollEventRetrying  videoPollEvent = "retrying"
	videoPollEventRecovered videoPollEvent = "recovered"
	videoPollEventStale     videoPollEvent = "stale"
)

// providerPollStaleKey 把"这一轮已经等太久了"钉在轮询上下文里。
//
// 光在超时那一刻改一次文案没用：下一次成功轮询又会把阶段写回"上游生成中"，界面上的说明
// 只闪一下。标记挂到上下文上以后，后续每次进度同步都会沿用长等待文案，直到上游真的出结果。
type providerPollStaleKey struct{}

func providerPollStale(ctx context.Context) bool {
	stale, _ := ctx.Value(providerPollStaleKey{}).(bool)
	return stale
}

type videoPollOutcome struct {
	Done   bool
	Result map[string]interface{}
}

type videoDownloadError struct {
	TaskID string
	Cause  error
}

func (e videoDownloadError) Error() string {
	if strings.TrimSpace(e.TaskID) == "" {
		return fmt.Sprintf("视频结果下载失败：%v", e.Cause)
	}
	return fmt.Sprintf("视频结果下载失败（任务 %s）：%v", e.TaskID, e.Cause)
}

func (e videoDownloadError) Unwrap() error { return e.Cause }

func defaultVideoPollPolicy() videoPollPolicy {
	return videoPollPolicy{
		InitialDelay:          defaultVideoPollInterval,
		Interval:              defaultVideoPollInterval,
		StaleAfter:            videoPollStaleAfter,
		MaxNotFoundMisses:     3,
		MaxMalformedResponses: 3,
		MaxDownloadTries:      3,
		RetryTransient:        true,
		Sleep:                 sleepContext,
		Notify:                notifyTaskVideoPollEvent,
	}
}

func runVideoPollLoop(ctx context.Context, taskID string, policy videoPollPolicy, query func(context.Context) (videoPollOutcome, error)) (map[string]interface{}, error) {
	policy = normalizeVideoPollPolicy(policy)
	deadline := providerPollingDeadline(ctx)
	pollContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	ctx = pollContext
	nextDelay := policy.InitialDelay
	notFoundMisses := 0
	malformedResponses := 0
	retrying := false
	staleNotified := false
	pollStartedAt := time.Now()
	for time.Now().Before(deadline) {
		if !staleNotified && time.Since(pollStartedAt) >= policy.StaleAfter {
			staleNotified = true
			policy.Notify(ctx, taskID, videoPollEventStale, nil)
			ctx = context.WithValue(ctx, providerPollStaleKey{}, true)
		}
		if nextDelay > 0 {
			if err := policy.Sleep(ctx, nextDelay); err != nil {
				return nil, err
			}
		}
		queryStartedAt := time.Now()
		outcome, err := query(ctx)
		queryDuration := time.Since(queryStartedAt)
		if err != nil {
			if !policy.RetryTransient {
				return nil, err
			}
			retry, notFound := retryableVideoPollError(ctx, err)
			if !retry {
				return nil, err
			}
			malformed := isTransientResponseDecodeError(err)
			if malformed {
				malformedResponses++
				notFoundMisses = 0
				if malformedResponses >= policy.MaxMalformedResponses {
					return nil, err
				}
			} else if notFound {
				malformedResponses = 0
				notFoundMisses++
				if notFoundMisses >= policy.MaxNotFoundMisses {
					return nil, err
				}
			} else {
				malformedResponses = 0
				notFoundMisses = 0
			}
			if !retrying {
				retrying = true
				policy.Notify(ctx, taskID, videoPollEventRetrying, err)
			}
			nextDelay = max(policy.Interval, providerRetryAfter(err))
			continue
		}
		notFoundMisses = 0
		malformedResponses = 0
		nextDelay = policy.Interval
		// 有些上游把状态查询当长轮询：一次响应本身就要几分钟。响应耗时已经超过间隔时，
		// 再补睡一个间隔只是白白拖长等待，直接立刻发下一次查询。
		if queryDuration >= policy.Interval {
			nextDelay = 0
		}
		if retrying {
			retrying = false
			policy.Notify(ctx, taskID, videoPollEventRecovered, nil)
		}
		if outcome.Done {
			return outcome.Result, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, context.DeadlineExceeded
}

func normalizeVideoPollPolicy(policy videoPollPolicy) videoPollPolicy {
	if policy.Interval <= 0 {
		policy.Interval = defaultVideoPollInterval
	}
	if policy.StaleAfter <= 0 {
		policy.StaleAfter = videoPollStaleAfter
	}
	if policy.MaxNotFoundMisses <= 0 {
		policy.MaxNotFoundMisses = 3
	}
	if policy.MaxMalformedResponses <= 0 {
		policy.MaxMalformedResponses = 3
	}
	if policy.MaxDownloadTries <= 0 {
		policy.MaxDownloadTries = 3
	}
	if policy.Sleep == nil {
		policy.Sleep = sleepContext
	}
	if policy.Notify == nil {
		policy.Notify = func(context.Context, string, videoPollEvent, error) {}
	}
	return policy
}

func runVideoDownload(ctx context.Context, taskID string, policy videoPollPolicy, download func(context.Context) ([]byte, string, error)) ([]byte, string, error) {
	policy = normalizeVideoPollPolicy(policy)
	var lastErr error
	for attempt := 1; attempt <= policy.MaxDownloadTries; attempt++ {
		data, mimeType, err := download(ctx)
		if err == nil {
			return data, mimeType, nil
		}
		lastErr = err
		retry, _ := retryableVideoPollError(ctx, err)
		if !retry || attempt == policy.MaxDownloadTries {
			return nil, "", videoDownloadError{TaskID: taskID, Cause: err}
		}
		delay := max(policy.Interval, providerRetryAfter(err))
		if err := policy.Sleep(ctx, delay); err != nil {
			return nil, "", err
		}
	}
	return nil, "", videoDownloadError{TaskID: taskID, Cause: lastErr}
}

func retryableVideoPollError(ctx context.Context, err error) (retry bool, notFound bool) {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return false, false
	}
	var downloadError videoDownloadError
	if errors.As(err, &downloadError) {
		return false, false
	}
	if code, _ := ChannelSlotFailureDetails(err); code != "" {
		return true, false
	}
	var circuitOpen providerCircuitOpenError
	if errors.As(err, &circuitOpen) {
		return true, false
	}
	if isTransientResponseDecodeError(err) {
		return true, false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrClosedPipe) || outbound.IsConnectionInterrupted(err) {
		return true, false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true, false
	}
	var httpErr providerHTTPError
	if errors.As(err, &httpErr) {
		if isProviderTaskNotReadyError(httpErr) {
			return true, true
		}
		if httpErr.StatusCode == http.StatusNotFound {
			return true, true
		}
		switch httpErr.StatusCode {
		case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooEarly, http.StatusTooManyRequests:
			return true, false
		default:
			return httpErr.StatusCode >= http.StatusInternalServerError, false
		}
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return networkError.Timeout() || networkError.Temporary(), false
	}
	return false, false
}

func isTransientResponseDecodeError(err error) bool {
	var decodeError providerResponseDecodeError
	if errors.As(err, &decodeError) {
		return true
	}
	var syntaxError *json.SyntaxError
	return errors.As(err, &syntaxError)
}

func notifyTaskVideoPollEvent(ctx context.Context, _ string, event videoPollEvent, eventErr error) {
	metadata, ok := ctx.Value(providerAnalyticsKey{}).(providerAnalyticsContext)
	service := providerService(metadata)
	if !ok || service == nil || service.repo == nil || metadata.TaskID == "" {
		return
	}
	if event == videoPollEventStale {
		// 只改文案，不改进度、不改状态：任务仍然在跑，用户随时可以取消（取消走的是
		// 另一条路径，与这里的判断无关）。
		if err := service.repo.UpdateTaskRunningStage(metadata.TaskID, videoPollStaleStage); err != nil {
			log.Printf("video poll stale stage update failed: task_id=%s error=%v", metadata.TaskID, err)
			return
		}
		_ = service.log(metadata.UserID, metadata.TaskID, "warn", "上游长时间未返回结果，轮询继续", "")
		return
	}
	message := "上游视频查询暂时异常，将继续轮询原任务"
	level := "warn"
	payload := ""
	if eventErr != nil {
		payload = eventErr.Error()
	}
	if event == videoPollEventRecovered {
		message = "上游视频查询已恢复"
		level = "info"
	}
	_ = service.log(metadata.UserID, metadata.TaskID, level, message, payload)
}

func isProviderTaskNotReadyError(httpErr providerHTTPError) bool {
	if httpErr.StatusCode != http.StatusBadRequest && httpErr.StatusCode != http.StatusNotFound {
		return false
	}
	var payload map[string]any
	if json.Unmarshal([]byte(httpErr.Body), &payload) != nil {
		return false
	}
	code, message := providerFailureDetails(payload)
	for _, value := range []string{code, message} {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "task_not_exist", "task_not_found", "task not exist", "task not found":
			return true
		}
	}
	return false
}

func providerRetryAfter(err error) time.Duration {
	var httpErr providerHTTPError
	if errors.As(err, &httpErr) && httpErr.RetryAfter > 0 {
		return httpErr.RetryAfter
	}
	return 0
}
