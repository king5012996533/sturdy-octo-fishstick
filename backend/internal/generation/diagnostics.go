package generation

import (
	"regexp"
	"strings"
)

// 上游把生成排查信息整体落库（请求证据、failure_diagnostics），那部分属于另一个功能，
// 我们还没合；这里只取任务日志摘要要用的脱敏，保证日志不会带出本机路径。
// PathError separates the path and cause with a colon; spaces are legal in paths.
// Without a reliable delimiter, conservatively hide the remaining suffix too.
var diagnosticLocalPath = regexp.MustCompile(`(?i)(?:[a-z]:[\\/]|\\\\|/(?:Users|home|private|tmp|var|Volumes|mnt|media|run|root|opt|srv|etc)/)[^\r\n:"'<>]+`)

// DiagnosticSummary preserves the error message without copying payloads or local paths.
func DiagnosticSummary(message string) string {
	if strings.HasPrefix(strings.TrimSpace(message), "[") {
		return ""
	}
	return sanitizeProviderText(diagnosticLocalPath.ReplaceAllString(message, "[路径已隐藏]"))
}
