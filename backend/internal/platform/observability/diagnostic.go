package observability

import "strings"

// DiagnosticMaxBytes 限制单个日志字段中的 operator 失败诊断大小，保留定位失败边界所需的上下文。
const DiagnosticMaxBytes = 4 * 1024

// SnapshotDiagnostic 规范化并限制 operator 诊断；内部日志保留原始诊断，payload 和 model context 继续使用各自的有界 observation contract。
func SnapshotDiagnostic(raw string) (message string, truncated bool) {
	raw = strings.TrimSpace(strings.ToValidUTF8(raw, "\uFFFD"))
	if raw == "" {
		return "", false
	}
	return truncateUTF8(raw, DiagnosticMaxBytes)
}
