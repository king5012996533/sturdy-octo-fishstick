package app

import (
	"strings"
	"testing"

	"infinite-canvas/backend/internal/generation"
	"infinite-canvas/backend/internal/model"
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

// TestProtocolTaskFailureKeepsSpecificCategories 覆盖"上游已给出终态结论"的归类。
//
// 上游任务终态失败时我们只拿到一段原话，但原话里可能写着审核驳回、参数非法或额度不足——
// 这些有专属文案和退款口径，不能被更粗的"异步任务失败"盖掉。只有原话什么都说明不了时，
// 才按 async_failed 归，避免用户为一次没有产出的生成付钱。
func TestProtocolTaskFailureKeepsSpecificCategories(t *testing.T) {
	cases := []struct {
		name         string
		message      string
		wantCategory generation.FailureCategory
		wantReason   string
	}{
		{
			name:         "审核驳回仍然按审核归",
			message:      "Prediction failed: Async prediction failed: ModelError: The input or output was flagged as sensitive. Please try again with different inputs. (E005)",
			wantCategory: generation.CategoryModerationOutput,
			wantReason:   "输入或输出未通过上游内容安全审核",
		},
		{
			name:         "上游说了原因但没有类目，按没有产出归并展示原话",
			message:      "视频生成未成功，请稍后重试；若多次失败请更换素材或提示词。",
			wantCategory: generation.CategoryAsyncFailed,
			wantReason:   "视频生成未成功，请稍后重试；若多次失败请更换素材或提示词。",
		},
		{
			name:         "上游什么都没说，用类目文案兜底",
			message:      "",
			wantCategory: generation.CategoryAsyncFailed,
			wantReason:   "生成任务没有完成",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			failure := classifyTaskFailure(providerTaskFailedError{Message: testCase.message, TaskID: "2106878811934052352"})
			if failure.Category != testCase.wantCategory {
				t.Fatalf("类目 = %q，期望 %q", failure.Category, testCase.wantCategory)
			}
			if failure.Reason != testCase.wantReason {
				t.Fatalf("原因 = %q，期望 %q", failure.Reason, testCase.wantReason)
			}
			if !strings.Contains(failure.UserMessage(), strings.TrimRight(testCase.wantReason, "。")) {
				t.Fatalf("用户看到的话术丢了原因：%q", failure.UserMessage())
			}
		})
	}
}

// TestProtocolTaskFailureIsRefundable 覆盖退款判据：上游明确回执没有产出就不该收钱。
func TestProtocolTaskFailureIsRefundable(t *testing.T) {
	svc, _ := newTaskCreditTestService(t)
	task := &model.Task{ID: "task-async-failed", UserID: "user-1", ProviderRequestID: "2106878811934052352"}
	refundable, reason := svc.taskRefundVerdict(task, providerTaskFailedError{Message: "", TaskID: "2106878811934052352"})
	if !refundable {
		t.Fatalf("上游明确回执没有产出必须退预扣，实际拒绝：%s", reason)
	}
}
