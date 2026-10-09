package generation_test

import (
	"encoding/json"
	"infinite-canvas/backend/internal/generation"
	"os"
	"testing"
)

func TestModerationContract(t *testing.T) {
	data, err := os.ReadFile("../../../fixtures/moderation-errors.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct{ Code, Message, Category, Reason, Action string }
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Code, func(t *testing.T) {
			inputs := []string{fixture.Message}
			for _, code := range []string{fixture.Code, "invalid_request_error", "upstream_error", "400"} {
				raw, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": fixture.Message}, "request_id": "req_moderation_123"})
				inputs = append(inputs, string(raw))
			}
			codeOnly, _ := json.Marshal(map[string]any{"error": map[string]string{"code": fixture.Code}})
			inputs = append(inputs, string(codeOnly))
			for _, input := range inputs {
				first := generation.ClassifyText(input)
				persisted := generation.ClassifyText(first.UserMessage())
				for _, f := range []generation.Failure{first, persisted} {
					if string(f.Category) != fixture.Category || f.Reason != fixture.Reason || f.Action != fixture.Action || !f.IsModeration() || !f.BlocksAutomaticRetry() || f.Retryable {
						t.Fatalf("input %s: %+v", input, f)
					}
				}
				if persisted.RequestID != first.RequestID {
					t.Fatalf("lost ID: %+v -> %+v", first, persisted)
				}
			}
		})
	}
}

func TestGenericWrapperRefinementPreservesAuthoritativeCodes(t *testing.T) {
	persisted := "生成音频未通过上游版权审核。请调整音乐或音频相关提示词；如使用了参考音频，请检查并更换后重新生成。"
	for typeCode, category := range map[string]generation.FailureCategory{"authentication_error": generation.CategoryAuth, "permission_denied": generation.CategoryPermission} {
		for _, field := range []string{"type", "status"} {
			raw, _ := json.Marshal(map[string]any{"error": map[string]string{field: typeCode, "message": persisted}})
			if got := generation.ClassifyText(string(raw)); got.Category != category {
				t.Fatalf("authoritative type %s: %+v", typeCode, got)
			}
		}
	}
	raw, _ := json.Marshal(map[string]any{"error": map[string]string{"code": "moderation_output", "message": persisted}, "request_id": "req_outer123", "task_id": "task_outer123"})
	if got := generation.ClassifyText(string(raw)); got.RequestID != "req_outer123" || got.TaskID != "task_outer123" {
		t.Fatalf("outer IDs lost: %+v", got)
	}
	if got := generation.ClassifyText("The request failed because the output may contain sensitive information."); got.Category != generation.CategoryModerationOutput {
		t.Fatalf("untyped output: %+v", got)
	}
	for _, tc := range []struct {
		Raw  string
		Want generation.FailureCategory
	}{
		{`{"error":{"code":"invalid_request_error","message":"Rate limit exceeded"}}`, generation.CategoryThrottled},
		{`{"error":{"code":"invalid_api_key","message":"The output audio may be related to copyright restrictions."}}`, generation.CategoryAuth},
		{`{"error":{"code":"invalid_request_error","message":"invalid parameter"},"prompt":"The output audio may be related to copyright restrictions."}`, generation.CategoryInvalidParams},
	} {
		if got := generation.ClassifyText(tc.Raw); got.Category != tc.Want {
			t.Fatalf("%s: %+v", tc.Raw, got)
		}
	}
}

// Replicate（以及部分 OpenAI 兼容图片接口）的审核驳回只写「输入或输出被判定为敏感」，
// 既不给方向也不给媒介。这条措辞以前完全认不出，任务会落到 unknown：用户看到「生成失败」，
// 平台也不会按审核类目退预扣积分。这里把结论和文案一起钉死。
func TestModerationSensitiveFlagFromReplicate(t *testing.T) {
	const reason = "输入或输出未通过上游内容安全审核"
	const action = "请调整提示词或参考素材后重新生成"
	raws := []string{
		"Prediction failed: Async prediction failed: ModelError: The input or output was flagged as sensitive. Please try again with different inputs. (E005) (uIJ6l3ruRD)",
		"声明式协议任务失败（任务 w0p3yewvwxrmw0d146qv74tgp0）：Prediction failed: Async prediction failed: ModelError: The input or output was flagged as sensitive. Please try again with different inputs. (E005) (uIJ6l3ruRD)",
		"The generated output was flagged as potentially sensitive by the safety system.",
	}
	for _, raw := range raws {
		first := generation.ClassifyText(raw)
		persisted := generation.ClassifyText(first.UserMessage())
		for _, f := range []generation.Failure{first, persisted} {
			if f.Category != generation.CategoryModerationOutput || f.Reason != reason || f.Action != action || !f.IsModeration() || f.Retryable {
				t.Fatalf("%s: %+v", raw, f)
			}
		}
	}
}

// 提示词回显里出现同样的措辞时不能当成审核结论，否则用户贴一张被判敏感的图做提示词，
// 连参数错误都会被误判成审核驳回。
func TestModerationSensitiveFlagIgnoredInPromptEcho(t *testing.T) {
	raw := `{"error":{"code":"invalid_request_error","message":"invalid parameter"},"prompt":"the reference image was flagged as sensitive"}`
	if got := generation.ClassifyText(raw); got.Category != generation.CategoryInvalidParams {
		t.Fatalf("prompt echo should not be moderation: %+v", got)
	}
}
