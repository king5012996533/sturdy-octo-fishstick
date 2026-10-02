package kernel

// 业务信封 code：成功为 0；失败默认等于 HTTP 状态。需要区分同一状态的不同原因时，用 status*100+n。
// 不使用与 HTTP 无关的 1001/2001 号段，避免和现有 42901 以及前端按 status/code 判断重试打架。
const (
	CodeOK = 0

	CodeInvalidArgument = 400
	CodeUnauthorized    = 401
	CodeForbidden       = 403
	CodeNotFound        = 404
	CodeConflict        = 409
	CodeTooManyRequests = 429
	CodeInternal        = 500
	CodeBadGateway      = 502
	CodeUnavailable     = 503
	CodeTimeout         = 504

	CodeQuotaExceeded       = 40301
	CodeIdempotencyConflict = 40901
	// CodeCanvasRevisionConflict 与 CodeIdempotencyConflict 同属 409，但处置方式相反：
	// 幂等冲突是"这次请求重复了，照原结果返回"，画布版本冲突是"你手上的版本旧了"。
	// 前端要按后者去收敛版本再重试，所以必须在 reason 上区分开，不能停在笼统的 conflict。
	CodeCanvasRevisionConflict = 40902
	CodeRateLimited            = 42901

	// CodeInsufficientCredits 复用的是 HTTP 402 的语义：请求本身没毛病，是账户
	// 余额不够。不并进 CodeQuotaExceeded——套餐配额用尽该引导升级订阅，积分不足
	// 该引导充值，前端要给出的是两个不同的下一步动作。
	CodeInsufficientCredits = 40201
)

// ErrorReason 是稳定机器可读原因，前端应判断 reason 而不是解析 msg。
type ErrorReason string

const (
	ReasonInvalidArgument        ErrorReason = "invalid_argument"
	ReasonUnauthorized           ErrorReason = "unauthorized"
	ReasonForbidden              ErrorReason = "forbidden"
	ReasonNotFound               ErrorReason = "not_found"
	ReasonConflict               ErrorReason = "conflict"
	ReasonCanvasRevisionConflict ErrorReason = "canvas_revision_conflict"
	ReasonFailedPrecondition     ErrorReason = "failed_precondition"
	ReasonQuotaExceeded          ErrorReason = "quota_exceeded"
	ReasonRateLimited            ErrorReason = "rate_limited"
	ReasonInsufficientCredits    ErrorReason = "insufficient_credits"
	ReasonUnavailable            ErrorReason = "unavailable"
	ReasonTimeout                ErrorReason = "timeout"
	ReasonInternal               ErrorReason = "internal"
	ReasonBadGateway             ErrorReason = "bad_gateway"
)

func ReasonForStatus(status int) ErrorReason {
	switch status {
	case CodeInvalidArgument:
		return ReasonInvalidArgument
	case CodeUnauthorized:
		return ReasonUnauthorized
	case CodeForbidden:
		return ReasonForbidden
	case CodeNotFound:
		return ReasonNotFound
	case CodeConflict:
		return ReasonConflict
	case CodeTooManyRequests:
		return ReasonRateLimited
	case CodeBadGateway:
		return ReasonBadGateway
	case CodeUnavailable:
		return ReasonUnavailable
	case CodeTimeout:
		return ReasonTimeout
	default:
		if status >= 500 {
			return ReasonInternal
		}
		return ReasonInvalidArgument
	}
}
