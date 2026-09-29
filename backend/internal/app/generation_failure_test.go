package app

import (
	"strings"
	"testing"

	"infinite-canvas/backend/internal/generation"
)

// 平台自身的本地预检错误带着可行动的结论，不能被通用文案顶掉，
// 否则用户只看到「模型不接受当前参数」，排查线索彻底丢失。
func TestLocalPreflightAppErrorKeepsActionableMessage(t *testing.T) {
	err := BadAuthRequest("本地资源尚未配置上游可访问地址，请设置 CANVAS_PUBLIC_BASE_URL")

	message := persistableTaskFailureMessage(err)
	if !strings.Contains(message, "CANVAS_PUBLIC_BASE_URL") {
		t.Fatalf("本地预检错误应保留原文，实际 = %q", message)
	}
	if strings.Contains(message, "模型不接受当前参数") {
		t.Fatalf("本地预检错误不应被通用文案覆盖，实际 = %q", message)
	}
	if code := taskFailureErrorCode(err); code != string(generation.CategoryInvalidParams) {
		t.Fatalf("本地预检错误的分类应保持不变，实际 = %q", code)
	}
}

// 带上游错误码的 AppError 仍然按归类结果输出通用文案，避免把上游正文透给用户。
func TestProviderCodedAppErrorUsesNormalizedCopy(t *testing.T) {
	err := BadAuthRequest("upstream raw body")
	err.Code = 40301

	message := persistableTaskFailureMessage(err)
	if strings.Contains(message, "upstream raw body") {
		t.Fatalf("带上游错误码的 AppError 不应透出原文，实际 = %q", message)
	}
}

// 上游结构化响应仍以归类结果为准，不能被本地文案改写。
func TestStructuredProviderFailureOutranksLocalMessage(t *testing.T) {
	err := WrapAppError(400, `{"error":{"message":"Invalid size","type":"invalid_request_error","param":"size","code":"invalid_request"}}`, nil)

	failure := classifyTaskFailure(err)
	if !failure.Structured {
		t.Fatalf("上游结构化响应应保留 Structured 标记，实际 = %#v", failure)
	}
	if strings.Contains(failure.UserMessage(), "invalid_request_error") {
		t.Fatalf("上游结构化响应不应透出原始字段名，实际 = %q", failure.UserMessage())
	}
}
