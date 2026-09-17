package postgres

import (
	"context"
	"strings"
	"unicode"
)

type operationContextKey struct{}
type transactionContextKey struct{}

// WithOperation 为一次数据库调用声明稳定、低基数的 operation name。
// adapter 应使用类似 incident.get_by_id 的常量，不得拼接 entity ID 或用户输入。
func WithOperation(ctx context.Context, operation string) context.Context {
	return context.WithValue(ctx, operationContextKey{}, operation)
}

func operationFromContext(ctx context.Context, sql string) string {
	if operation, ok := ctx.Value(operationContextKey{}).(string); ok && validOperation(operation) {
		return operation
	}
	return sqlVerb(sql)
}

func validOperation(operation string) bool {
	if operation == "" || len(operation) > 80 {
		return false
	}
	for _, character := range operation {
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') &&
			character != '.' && character != '_' {
			return false
		}
	}
	return true
}

func sqlVerb(sql string) string {
	trimmed := trimLeadingSQLComments(sql)
	end := 0
	for end < len(trimmed) && end < 16 {
		character := trimmed[end]
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') {
			break
		}
		end++
	}
	verb := strings.ToUpper(trimmed[:end])
	switch verb {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "WITH", "CALL", "COPY",
		"BEGIN", "COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE", "SET", "SHOW",
		"CREATE", "ALTER", "DROP", "TRUNCATE", "GRANT", "REVOKE":
		return strings.ToLower(verb)
	default:
		return "unknown"
	}
}

func trimLeadingSQLComments(sql string) string {
	remaining := strings.TrimLeftFunc(sql, unicode.IsSpace)
	// sqlc 生成的 statement 通常保留 `-- name:` 前缀；这里只跳过有限种前置
	// comment 来识别 verb，绝不把 comment 或 statement 内容带入返回值。
	for range 8 {
		switch {
		case strings.HasPrefix(remaining, "--"):
			newline := strings.IndexByte(remaining, '\n')
			if newline < 0 {
				return ""
			}
			remaining = strings.TrimLeftFunc(remaining[newline+1:], unicode.IsSpace)
		case strings.HasPrefix(remaining, "/*"):
			end := strings.Index(remaining[2:], "*/")
			if end < 0 {
				return ""
			}
			remaining = strings.TrimLeftFunc(remaining[end+4:], unicode.IsSpace)
		default:
			return remaining
		}
	}
	return remaining
}

func withTransactionID(ctx context.Context, transactionID string) context.Context {
	return context.WithValue(ctx, transactionContextKey{}, transactionID)
}

func transactionIDFromContext(ctx context.Context) string {
	transactionID, _ := ctx.Value(transactionContextKey{}).(string)
	return transactionID
}
