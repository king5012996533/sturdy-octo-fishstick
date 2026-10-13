package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// 图片创建阶段的三种失败，必须落进三种不同的处置：
// 边缘网关 5xx 可以重放，供应商 JSON 错误不能重放只能按提交未确认处理，
// 参数非法这类明确拒绝既不能重放也不能被当成"结果不明"。

const edgeGatewayBody = "error code: 502"

func imageSubmissionTestInput(serverURL string) canvasGenerationInput {
	return canvasGenerationInput{
		Mode:   "image",
		Prompt: "a cat",
		Config: providerConfig{BaseURL: serverURL, Model: "openai/gpt-image-2", InterfaceType: "replicate-prediction-image"},
	}
}

func TestImageCreateReplaysEdgeGatewayFailure(t *testing.T) {
	allowLoopbackProviderTest(t)
	creates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			creates++
			_, _ = io.Copy(io.Discard, r.Body)
			if creates == 1 {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(edgeGatewayBody))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"pred-1","status":"starting"}`))
			return
		}
		if r.URL.Path == "/image.png" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"pred-1","status":"succeeded","output":["http://` + r.Host + `/image.png"]}`))
	}))
	defer server.Close()

	adapter := officialSourceProviderAdapter(t, "replicate-prediction-image", "replicate-prediction-image")
	result, err := runProtocolAdapterTaskWithPolicy(context.Background(), imageSubmissionTestInput(server.URL), adapter, fastVideoPollPolicy())
	if err != nil {
		t.Fatalf("边缘网关错误重放后应当出图，实际报错: %v", err)
	}
	if creates != 2 {
		t.Fatalf("创建请求次数 = %d，want 2（第一次 502 后重放一次）", creates)
	}
	if images, _ := result["images"].([]interface{}); len(images) != 1 {
		t.Fatalf("结果 = %#v，want 一张图", result)
	}
}

// 供应商自己的 5xx 带 JSON 正文，说明请求已经到达上游、只是被上游拒掉，
// 源站有没有建任务无从判断，不能重放。
func TestImageCreateDoesNotReplayProviderJSONFailure(t *testing.T) {
	allowLoopbackProviderTest(t)
	creates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		creates++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"detail":"upstream exploded"}`))
	}))
	defer server.Close()

	adapter := officialSourceProviderAdapter(t, "replicate-prediction-image", "replicate-prediction-image")
	_, err := runProtocolAdapterTaskWithPolicy(context.Background(), imageSubmissionTestInput(server.URL), adapter, fastVideoPollPolicy())
	if creates != 1 {
		t.Fatalf("创建请求次数 = %d，want 1（供应商 JSON 错误不得重放）", creates)
	}
	var unknown providerSubmissionUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("供应商 5xx 拿不到回执，必须标成提交未确认，实际: %v", err)
	}
}

// 参数非法是上游明确拒绝、不会建任务也不会计费：既不能重放，也不能打上"结果不明"的标记，
// 否则用户会看到一个既不该重试、也退不了款的失败。
func TestImageCreateKeepsSafeRejectionUnwrapped(t *testing.T) {
	allowLoopbackProviderTest(t)
	creates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		creates++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid size"}}`))
	}))
	defer server.Close()

	adapter := officialSourceProviderAdapter(t, "replicate-prediction-image", "replicate-prediction-image")
	_, err := runProtocolAdapterTaskWithPolicy(context.Background(), imageSubmissionTestInput(server.URL), adapter, fastVideoPollPolicy())
	if creates != 1 {
		t.Fatalf("创建请求次数 = %d，want 1（明确拒绝不得重放）", creates)
	}
	var unknown providerSubmissionUnknownError
	if errors.As(err, &unknown) {
		t.Fatalf("上游明确拒绝不能被标成提交未确认: %v", err)
	}
}

// 504/524 是"网关把请求转出去了、只是没等到响应"：源站可能已经建好任务并计费，
// 重放就是重复下单，必须留给上层按提交未确认处理。
func TestImageCreateDoesNotReplayGatewayTimeout(t *testing.T) {
	allowLoopbackProviderTest(t)
	for _, status := range []int{http.StatusGatewayTimeout, 524} {
		creates := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			creates++
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(status)
			_, _ = w.Write([]byte("error code: " + strconv.Itoa(status)))
		}))
		adapter := officialSourceProviderAdapter(t, "replicate-prediction-image", "replicate-prediction-image")
		_, err := runProtocolAdapterTaskWithPolicy(context.Background(), imageSubmissionTestInput(server.URL), adapter, fastVideoPollPolicy())
		server.Close()
		if creates != 1 {
			t.Fatalf("状态 %d 的创建请求次数 = %d，want 1（网关超时不得重放）", status, creates)
		}
		var unknown providerSubmissionUnknownError
		if !errors.As(err, &unknown) {
			t.Fatalf("状态 %d 没拿到回执，必须标成提交未确认，实际: %v", status, err)
		}
	}
}

// 文本任务一次性返回、没有可回查的任务号，把它按"提交未确认"处理只会让任务卡在
// 一个既对不了账、也说不清的状态里。
func TestTextModeStaysOutOfSubmissionUncertainty(t *testing.T) {
	for _, mode := range []string{"text", ""} {
		if asyncProviderTaskMode(mode) {
			t.Fatalf("模式 %q 不应被当成会建立上游任务的生成模式", mode)
		}
	}
	for _, mode := range []string{"image", "video", "audio"} {
		if !asyncProviderTaskMode(mode) {
			t.Fatalf("模式 %q 应当进入提交未确认的判据", mode)
		}
	}
}
