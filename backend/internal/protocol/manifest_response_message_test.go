package protocol

import (
	"context"
	"encoding/json"
	"testing"
)

// 上游把失败原因写成字符串 error 时，即使没有 error.message，也必须把原因带出来。
//
// Replicate 的 prediction 失败响应形如 {"status":"failed","error":"Prediction failed: ..."}。
// 修复前 message 映射只认 response.error.message（对象形态），字符串形态取不到值，任务失败
// 原因会退化成「上游返回失败状态」，用户看不出是内容安全审核，平台也无法据此判退积分。
func TestManifestResponseMessageFallsBackToStringError(t *testing.T) {
	adapter := officialPackageAdapter(t, "replicate-prediction-image.beeftv-plugin", "replicate-prediction-image")
	const upstreamError = "Prediction failed: Async prediction failed: ModelError: The input or output was flagged as sensitive. Please try again with different inputs. (E005) (uIJ6l3ruRD)"
	encoded, err := json.Marshal(upstreamError)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"id":"w0p3yewvwxrmw0d146qv74tgp0","status":"failed","error":` + string(encoded) + `,"output":null}`)
	result, err := adapter.ParsePoll(context.Background(), PollContext{TaskID: "w0p3yewvwxrmw0d146qv74tgp0"}, body)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusFailed {
		t.Fatalf("status = %q", result.Status)
	}
	if result.Message != upstreamError {
		t.Fatalf("message = %q", result.Message)
	}
}

// error 是对象时，仍然优先取 error.message，不能被新增的兜底项抢走。
func TestManifestResponseMessagePrefersNestedErrorMessage(t *testing.T) {
	env := map[string]any{"response": map[string]any{
		"error":   map[string]any{"code": "invalid_request_error", "message": "模型不接受当前参数"},
		"message": "顶层干扰文案",
	}}
	value := map[string]any{"$coalesce": []any{
		map[string]any{"$ref": "response.error.message"},
		map[string]any{"$ref": "response.message"},
		map[string]any{"$ref": "response.fail_reason"},
		map[string]any{"$ref": "response.error"},
	}}
	if got := manifestResponseString(value, env); got != "模型不接受当前参数" {
		t.Fatalf("message = %q", got)
	}
}

// error 不是文字时不能渲染成 "false"，也不能把空对象当成文案。
func TestManifestResponseMessageSkipsNonTextValues(t *testing.T) {
	value := map[string]any{"$coalesce": []any{
		map[string]any{"$ref": "response.error.message"},
		map[string]any{"$ref": "response.error"},
	}}
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{name: "bool false", value: false, want: ""},
		{name: "bool true", value: true, want: ""},
		{name: "empty object", value: map[string]any{}, want: ""},
		{name: "plain string", value: "上游拒绝了这次请求", want: "上游拒绝了这次请求"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]any{"response": map[string]any{"error": tc.value}}
			if got := manifestResponseString(value, env); got != tc.want {
				t.Fatalf("message = %q, want %q", got, tc.want)
			}
		})
	}
}
