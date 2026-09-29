package app

import (
	"errors"
	"strings"

	"infinite-canvas/backend/internal/generation"
	"infinite-canvas/backend/internal/kernel"
)

func classifyProviderHTTP(err providerHTTPError) generation.Failure {
	return generation.ClassifyHTTP(err.StatusCode, err.Status, err.Body)
}

func classifyTaskFailure(err error) generation.Failure {
	if err == nil {
		return generation.ClassifyError(nil)
	}
	var httpErr providerHTTPError
	if errors.As(err, &httpErr) {
		failure := classifyProviderHTTP(httpErr)
		return applyAppFailureWrappers(err, failure)
	}
	var payload providerPayloadError
	if errors.As(err, &payload) {
		failure := generation.ClassifyText(firstNonEmpty(payload.raw, payload.message))
		return applyAppFailureWrappers(err, failure)
	}
	var decode providerResponseDecodeError
	if errors.As(err, &decode) {
		failure := generation.ClassifyError(decode.Err)
		if failure.Category == generation.CategoryUnknown {
			failure = generation.ClassifyText(decode.Error())
		}
		if failure.Category == generation.CategoryUnknown {
			failure.Category = generation.CategoryMalformedResponse
			failure.Reason = ""
			failure.Action = ""
		}
		return applyAppFailureWrappers(err, failure)
	}
	return applyAppFailureWrappers(err, generation.ClassifyError(err))
}

func applyAppFailureWrappers(err error, failure generation.Failure) generation.Failure {
	var download videoDownloadError
	if errors.As(err, &download) {
		failure = generation.WithDownloadFailure(failure, download.TaskID)
	}
	var pending providerStatePendingError
	if errors.As(err, &pending) {
		failure.Category = generation.CategorySubmissionUncertain
		failure.Uncertain = true
		failure.TaskID = firstNonEmpty(failure.TaskID, pending.TaskID)
		failure.Reason = ""
		failure.Action = ""
	}
	var circuit providerCircuitOpenError
	if errors.As(err, &circuit) {
		return generation.CircuitOpenFailure()
	}
	if code, _ := ChannelSlotFailureDetails(err); code != "" {
		failure = generation.WithConcurrencyFailure(failure)
	}
	var appErr *kernel.AppError
	if errors.As(err, &appErr) && appErr != nil {
		classified := generation.ClassifyAppError(appErr.Status, appErr.Code, string(appErr.Reason), appErr.Message)
		if failure.Category == generation.CategoryUnknown || classified.Category != generation.CategoryUnknown {
			failure = classified
		}
		// 平台自己造出来的 AppError，Message 就是最终结论（例如本地预检缺少公网素材
		// 地址）。这类错误没有上游响应正文可归类，只能用通用文案覆盖，会让用户看到
		// 「模型不接受当前参数」而真正原因在任务、审计与排查日志里一起消失。
		if isLocalAppError(appErr) && !failure.Structured {
			failure.Reason = strings.TrimSpace(appErr.Message)
			failure.Action = ""
		}
	}
	return failure
}

// isLocalAppError 判断 AppError 是否由平台自身构造，而不是从上游响应里解析出来的。
// NewAppError 会把 Code 设成 Status；只有拿到上游错误码时才会写入不同的 Code，
// 因此两者相等即表示这条错误是本地结论，Message 可以原样展示给用户。
func isLocalAppError(err *kernel.AppError) bool {
	return err != nil && err.Code == err.Status && strings.TrimSpace(err.Message) != ""
}

func persistableTaskFailureMessage(err error) string {
	return classifyTaskFailure(err).UserMessage()
}

func taskFailureErrorCode(err error) string {
	return classifyTaskFailure(err).ErrorCode()
}

func persistedFailureErrorCode(message string, stage string) string {
	failure := generation.ClassifyText(message)
	if stage == "submission_unknown" {
		return string(generation.CategorySubmissionUncertain)
	}
	return failure.ErrorCode()
}

func persistedFailureBlocksRetry(message string, stage string) bool {
	if stage == "submission_unknown" {
		return true
	}
	return generation.ClassifyText(message).BlocksAutomaticRetry()
}

func (s *Service) UserFacingProviderHTTPError(status int, statusText string, body string) string {
	message := generation.ClassifyHTTP(status, statusText, body).UserMessage()
	if s == nil {
		return message
	}
	return s.InterceptResponseText(message)
}
