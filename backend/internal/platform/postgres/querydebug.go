package postgres

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

var queryPlaceholderPattern = regexp.MustCompile(`\$(\d+)\b`)

// interpolateQuery 把 $n 替换成 PostgreSQL 字面量，生成可直接粘贴执行的语句。
// 调试模式才调用；替换从完整占位符匹配，避免 $10 被 $1 截断。
func interpolateQuery(statement string, args []any) string {
	return queryPlaceholderPattern.ReplaceAllStringFunc(statement, func(match string) string {
		index, err := strconv.Atoi(match[1:])
		if err != nil || index < 1 || index > len(args) {
			return match
		}
		return formatQueryArg(args[index-1])
	})
}

func formatQueryArg(arg any) string {
	if arg == nil {
		return "NULL"
	}
	switch value := arg.(type) {
	case *string:
		if value == nil {
			return "NULL"
		}
		return quotePostgresString(*value)
	case *time.Time:
		if value == nil {
			return "NULL"
		}
		return quotePostgresString(value.UTC().Format(time.RFC3339Nano))
	case bool:
		return formatBoolLiteral(value)
	case int:
		return strconv.Itoa(value)
	case int8:
		return strconv.FormatInt(int64(value), 10)
	case int16:
		return strconv.FormatInt(int64(value), 10)
	case int32:
		return strconv.FormatInt(int64(value), 10)
	case int64:
		return strconv.FormatInt(value, 10)
	case uint:
		return strconv.FormatUint(uint64(value), 10)
	case uint16:
		return strconv.FormatUint(uint64(value), 10)
	case uint32:
		return strconv.FormatUint(uint64(value), 10)
	case uint64:
		return strconv.FormatUint(value, 10)
	case float32:
		return strconv.FormatFloat(float64(value), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case string:
		return quotePostgresString(value)
	case []byte:
		return `'\x` + hex.EncodeToString(value) + `'::bytea`
	case time.Time:
		return quotePostgresString(value.UTC().Format(time.RFC3339Nano))
	case uuid.UUID:
		return quotePostgresString(value.String())
	case pgtype.Bool:
		if !value.Valid {
			return "NULL"
		}
		return formatBoolLiteral(value.Bool)
	case pgtype.Int2:
		if !value.Valid {
			return "NULL"
		}
		return strconv.FormatInt(int64(value.Int16), 10)
	case pgtype.Int4:
		if !value.Valid {
			return "NULL"
		}
		return strconv.FormatInt(int64(value.Int32), 10)
	case pgtype.Int8:
		if !value.Valid {
			return "NULL"
		}
		return strconv.FormatInt(value.Int64, 10)
	case pgtype.Float8:
		if !value.Valid {
			return "NULL"
		}
		return strconv.FormatFloat(value.Float64, 'f', -1, 64)
	case pgtype.Text:
		if !value.Valid {
			return "NULL"
		}
		return quotePostgresString(value.String)
	case pgtype.Timestamptz:
		if !value.Valid {
			return "NULL"
		}
		return quotePostgresString(value.Time.UTC().Format(time.RFC3339Nano))
	case pgtype.UUID:
		if !value.Valid {
			return "NULL"
		}
		return quotePostgresString(uuid.UUID(value.Bytes).String())
	default:
		if stringer, ok := arg.(fmt.Stringer); ok {
			return quotePostgresString(stringer.String())
		}
		return quotePostgresString(fmt.Sprint(arg))
	}
}

func formatBoolLiteral(value bool) string {
	if value {
		return "TRUE"
	}
	return "FALSE"
}

func quotePostgresString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
