package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestVideoPollRecoversFromTransientHTTPError(t *testing.T) {
	attempts := 0
	result, err := runVideoPollLoop(context.Background(), "provider-task-1", fastVideoPollPolicy(), func(context.Context) (videoPollOutcome, error) {
		attempts++
		switch attempts {
		case 1:
			return videoPollOutcome{}, providerHTTPError{StatusCode: http.StatusBadGateway, Status: "502 Bad Gateway"}
		case 2:
			return videoPollOutcome{}, nil
		default:
			return videoPollOutcome{Done: true, Result: map[string]interface{}{"mode": "video"}}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 3 || result["mode"] != "video" {
		t.Fatalf("attempts = %d, result = %#v", attempts, result)
	}
}

func TestVideoPollWaitsBeforeFirstQueryAndUsesRetryAfter(t *testing.T) {
	var waits []time.Duration
	policy := fastVideoPollPolicy()
	policy.InitialDelay = 10 * time.Millisecond
	policy.Interval = 10 * time.Millisecond
	policy.Sleep = func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}
	attempts := 0
	_, err := runVideoPollLoop(context.Background(), "provider-task-1", policy, func(context.Context) (videoPollOutcome, error) {
		attempts++
		if attempts == 1 {
			return videoPollOutcome{}, providerHTTPError{StatusCode: http.StatusTooManyRequests, RetryAfter: 20 * time.Millisecond}
		}
		return videoPollOutcome{Done: true, Result: map[string]interface{}{"mode": "video"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}; !reflect.DeepEqual(waits, want) {
		t.Fatalf("waits = %v, want %v", waits, want)
	}
}

func TestVideoPollStopsImmediatelyOnAuthenticationError(t *testing.T) {
	attempts := 0
	_, err := runVideoPollLoop(context.Background(), "provider-task-1", fastVideoPollPolicy(), func(context.Context) (videoPollOutcome, error) {
		attempts++
		return videoPollOutcome{}, providerHTTPError{StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized"}
	})
	var httpErr providerHTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("error = %#v, want 401 provider error", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestVideoPollStopsAfterThreeConsecutiveNotFoundResponses(t *testing.T) {
	attempts := 0
	_, err := runVideoPollLoop(context.Background(), "provider-task-1", fastVideoPollPolicy(), func(context.Context) (videoPollOutcome, error) {
		attempts++
		return videoPollOutcome{}, providerHTTPError{StatusCode: http.StatusNotFound, Status: "404 Not Found"}
	})
	var httpErr providerHTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound {
		t.Fatalf("error = %#v, want 404 provider error", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestVideoPollSuccessfulPendingResponseResetsNotFoundCounter(t *testing.T) {
	attempts := 0
	result, err := runVideoPollLoop(context.Background(), "provider-task-1", fastVideoPollPolicy(), func(context.Context) (videoPollOutcome, error) {
		attempts++
		switch attempts {
		case 1, 3:
			return videoPollOutcome{}, providerHTTPError{StatusCode: http.StatusNotFound, Status: "404 Not Found"}
		case 2:
			return videoPollOutcome{}, nil
		default:
			return videoPollOutcome{Done: true, Result: map[string]interface{}{"mode": "video"}}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 4 || result["mode"] != "video" {
		t.Fatalf("attempts = %d, result = %#v", attempts, result)
	}
}

func TestVideoPollRetriesMalformedResponseTwiceThenSucceeds(t *testing.T) {
	attempts := 0
	result, err := runVideoPollLoop(context.Background(), "provider-task-1", fastVideoPollPolicy(), func(context.Context) (videoPollOutcome, error) {
		attempts++
		if attempts <= 2 {
			return videoPollOutcome{}, &json.SyntaxError{Offset: int64(attempts)}
		}
		return videoPollOutcome{Done: true, Result: map[string]interface{}{"mode": "video"}}, nil
	})
	if err != nil || attempts != 3 || result["mode"] != "video" {
		t.Fatalf("result = %#v, error = %v, attempts = %d", result, err, attempts)
	}
}

func TestVideoPollStopsAfterThreeMalformedResponses(t *testing.T) {
	attempts := 0
	_, err := runVideoPollLoop(context.Background(), "provider-task-1", fastVideoPollPolicy(), func(context.Context) (videoPollOutcome, error) {
		attempts++
		return videoPollOutcome{}, &json.SyntaxError{Offset: int64(attempts)}
	})
	var syntaxError *json.SyntaxError
	if !errors.As(err, &syntaxError) || attempts != 3 {
		t.Fatalf("error = %#v, attempts = %d, want three attempts and syntax error", err, attempts)
	}
}

func TestVideoPollTreatsCircuitAndInterruptedReadsAsTransient(t *testing.T) {
	for _, err := range []error{providerCircuitOpenError{}, io.ErrUnexpectedEOF, io.ErrClosedPipe} {
		retry, notFound := retryableVideoPollError(context.Background(), err)
		if !retry || notFound {
			t.Fatalf("error %T classified as retry=%v notFound=%v", err, retry, notFound)
		}
	}
}

func TestVideoPollNotifiesOnlyWhenRetryStartsAndRecovers(t *testing.T) {
	var events []videoPollEvent
	policy := fastVideoPollPolicy()
	policy.Notify = func(_ context.Context, _ string, event videoPollEvent, _ error) {
		events = append(events, event)
	}
	attempts := 0
	_, err := runVideoPollLoop(context.Background(), "provider-task-1", policy, func(context.Context) (videoPollOutcome, error) {
		attempts++
		switch attempts {
		case 1, 2:
			return videoPollOutcome{}, providerHTTPError{StatusCode: http.StatusBadGateway}
		case 3:
			return videoPollOutcome{}, nil
		default:
			return videoPollOutcome{Done: true, Result: map[string]interface{}{"mode": "video"}}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []videoPollEvent{videoPollEventRetrying, videoPollEventRecovered}
	if len(events) != len(want) || events[0] != want[0] || events[1] != want[1] {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestVideoPollContextCancellationInterruptsInitialWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	_, err := runVideoPollLoop(ctx, "provider-task-1", defaultVideoPollPolicy(), func(context.Context) (videoPollOutcome, error) {
		called = true
		return videoPollOutcome{}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if called {
		t.Fatal("query ran after cancellation")
	}
}

func TestVideoPollAppliesFallbackDeadlineToWaitAndQuery(t *testing.T) {
	policy := fastVideoPollPolicy()
	waitHadDeadline := false
	queryHadDeadline := false
	policy.Sleep = func(ctx context.Context, _ time.Duration) error {
		_, waitHadDeadline = ctx.Deadline()
		return nil
	}
	_, err := runVideoPollLoop(context.Background(), "provider-task-1", policy, func(ctx context.Context) (videoPollOutcome, error) {
		_, queryHadDeadline = ctx.Deadline()
		return videoPollOutcome{Done: true, Result: map[string]interface{}{"mode": "video"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !waitHadDeadline || !queryHadDeadline {
		t.Fatalf("wait deadline = %v, query deadline = %v", waitHadDeadline, queryHadDeadline)
	}
}

func TestVideoDownloadRetriesTransientFailure(t *testing.T) {
	attempts := 0
	waits := 0
	policy := fastVideoPollPolicy()
	policy.Sleep = func(context.Context, time.Duration) error {
		waits++
		return nil
	}
	data, mimeType, err := runVideoDownload(context.Background(), "provider-task-1", policy, func(context.Context) ([]byte, string, error) {
		attempts++
		if attempts == 1 {
			return nil, "", providerHTTPError{StatusCode: http.StatusBadGateway, Status: "502 Bad Gateway"}
		}
		return []byte("video"), "video/mp4", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "video" || mimeType != "video/mp4" || attempts != 2 || waits != 1 {
		t.Fatalf("data = %q, mime = %q, attempts = %d, waits = %d", data, mimeType, attempts, waits)
	}
}

func TestVideoDownloadStopsAfterThreeTransientFailures(t *testing.T) {
	attempts := 0
	_, _, err := runVideoDownload(context.Background(), "provider-task-1", fastVideoPollPolicy(), func(context.Context) ([]byte, string, error) {
		attempts++
		return nil, "", providerHTTPError{StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable"}
	})
	var httpErr providerHTTPError
	if !errors.As(err, &httpErr) || attempts != 3 {
		t.Fatalf("error = %#v, attempts = %d, want three attempts and provider error", err, attempts)
	}
}

func TestVideoPollDoesNotRetryExhaustedDownload(t *testing.T) {
	attempts := 0
	_, err := runVideoPollLoop(context.Background(), "provider-task-1", fastVideoPollPolicy(), func(context.Context) (videoPollOutcome, error) {
		attempts++
		return videoPollOutcome{}, videoDownloadError{TaskID: "provider-task-1", Cause: providerHTTPError{StatusCode: http.StatusBadGateway}}
	})
	var downloadError videoDownloadError
	if !errors.As(err, &downloadError) || attempts != 1 {
		t.Fatalf("error = %#v, poll attempts = %d, want one exhausted download", err, attempts)
	}
}

func fastVideoPollPolicy() videoPollPolicy {
	policy := defaultVideoPollPolicy()
	policy.InitialDelay = time.Millisecond
	policy.Interval = time.Millisecond
	policy.Sleep = func(context.Context, time.Duration) error { return nil }
	return policy
}

// 有些上游把状态查询当长轮询：一次响应本身就要几分钟。响应耗时已经超过轮询间隔时，
// 再补睡一个间隔只是白白拖长用户的等待，必须直接发下一次查询。
func TestVideoPollSkipsIntervalAfterSlowResponse(t *testing.T) {
	var waits []time.Duration
	policy := fastVideoPollPolicy()
	policy.InitialDelay = 0
	policy.Interval = 20 * time.Millisecond
	policy.Sleep = func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}
	attempts := 0
	result, err := runVideoPollLoop(context.Background(), "provider-task-1", policy, func(context.Context) (videoPollOutcome, error) {
		attempts++
		if attempts == 1 {
			time.Sleep(30 * time.Millisecond)
			return videoPollOutcome{}, nil
		}
		return videoPollOutcome{Done: true, Result: map[string]interface{}{"mode": "video"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["mode"] != "video" {
		t.Fatalf("result = %#v", result)
	}
	if len(waits) != 0 {
		t.Fatalf("waits = %v, want none after a response slower than the interval", waits)
	}
}

// 反过来，响应很快时仍要按间隔节流，不能变成忙轮询。
func TestVideoPollKeepsIntervalAfterFastResponse(t *testing.T) {
	var waits []time.Duration
	policy := fastVideoPollPolicy()
	policy.InitialDelay = 0
	policy.Interval = 20 * time.Millisecond
	policy.Sleep = func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}
	attempts := 0
	_, err := runVideoPollLoop(context.Background(), "provider-task-1", policy, func(context.Context) (videoPollOutcome, error) {
		attempts++
		if attempts < 2 {
			return videoPollOutcome{}, nil
		}
		return videoPollOutcome{Done: true, Result: map[string]interface{}{"mode": "video"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []time.Duration{20 * time.Millisecond}; !reflect.DeepEqual(waits, want) {
		t.Fatalf("waits = %v, want %v", waits, want)
	}
}

// 上游长时间不返回结果时必须换文案：任务照跑、钱照扣，但界面不能再显示"上游生成中"，
// 否则用户看到的就是一个永远转不完的圈。
func TestVideoPollNotifiesStaleAfterBudget(t *testing.T) {
	var events []videoPollEvent
	policy := fastVideoPollPolicy()
	policy.InitialDelay = 0
	policy.StaleAfter = time.Nanosecond
	policy.Notify = func(_ context.Context, _ string, event videoPollEvent, _ error) {
		events = append(events, event)
	}
	attempts := 0
	_, err := runVideoPollLoop(context.Background(), "provider-task-1", policy, func(context.Context) (videoPollOutcome, error) {
		attempts++
		if attempts < 4 {
			return videoPollOutcome{}, nil
		}
		return videoPollOutcome{Done: true, Result: map[string]interface{}{"mode": "video"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []videoPollEvent{videoPollEventStale}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v（每个任务只提示一次）", events, want)
	}
}

// 预算没到之前不能抢跑：这条反例保证上面的测试不是"总会通知"的假阳性。
func TestVideoPollStaysQuietBeforeStaleBudget(t *testing.T) {
	var events []videoPollEvent
	policy := fastVideoPollPolicy()
	policy.InitialDelay = 0
	policy.StaleAfter = time.Hour
	policy.Notify = func(_ context.Context, _ string, event videoPollEvent, _ error) {
		events = append(events, event)
	}
	attempts := 0
	_, err := runVideoPollLoop(context.Background(), "provider-task-1", policy, func(context.Context) (videoPollOutcome, error) {
		attempts++
		if attempts < 4 {
			return videoPollOutcome{}, nil
		}
		return videoPollOutcome{Done: true, Result: map[string]interface{}{"mode": "video"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %v, want none", events)
	}
}

// 长等待标记必须留在上下文里：否则下一次成功轮询会把阶段写回"上游生成中"，
// 用户看到的提示只闪一下（前端阶段文案直接来自这里）。
func TestVideoPollStaleFlagSticksForLaterQueries(t *testing.T) {
	policy := fastVideoPollPolicy()
	// 首次等待也要真实发生：否则第一次查询在 0ms 就发出，等不到预算到点。
	policy.InitialDelay = 20 * time.Millisecond
	policy.Interval = 20 * time.Millisecond
	policy.StaleAfter = 10 * time.Millisecond
	policy.Sleep = func(_ context.Context, delay time.Duration) error {
		time.Sleep(delay)
		return nil
	}
	var observed []bool
	attempts := 0
	_, err := runVideoPollLoop(context.Background(), "provider-task-1", policy, func(ctx context.Context) (videoPollOutcome, error) {
		attempts++
		observed = append(observed, providerPollStale(ctx))
		if attempts < 3 {
			return videoPollOutcome{}, nil
		}
		return videoPollOutcome{Done: true, Result: map[string]interface{}{"mode": "video"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []bool{false, true, true}; !reflect.DeepEqual(observed, want) {
		t.Fatalf("stale flags = %v, want %v", observed, want)
	}
}

func runVideoTaskForTest(ctx context.Context, input canvasGenerationInput) (map[string]interface{}, error) {
	return runVideoTaskWithPolicy(ctx, input, fastVideoPollPolicy())
}
