package application

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxInspectCommandBytes = 4096
	maxInspectPipelineSegs = 3
)

var inspectAllowlist = map[string]struct{}{
	"ls": {}, "cat": {}, "head": {}, "tail": {}, "grep": {}, "egrep": {}, "fgrep": {},
	"find": {}, "stat": {}, "wc": {}, "file": {}, "readlink": {}, "realpath": {},
	"pwd": {}, "date": {}, "uname": {}, "hostname": {}, "df": {}, "du": {}, "ps": {},
	"journalctl": {}, "dmesg": {}, "id": {}, "env": {}, "printenv": {},
}

// parsedInspectCommand 保存 gateway 重建后的 quoted argv。Command 才是适配器
// 实际执行的远端字符串，不得把模型原始输入交给 SSH。
type parsedInspectCommand struct {
	Command string
}

// inspectToken 保留解码后的 argv 文本，以及 glob 元字符是否出现在未加引号、
// 未转义的位置。引号内的 *?[ 和 | 是字面量；未加引号的 ls *.log 或裸管道仍须在 SSH 前拒绝。
type inspectToken struct {
	value        string
	unquotedGlob bool
	pipe         bool
}

// parseInspectCommand 在任何 SSH 调用之前把模型字符串解析成允许的 inspect
// 命令。拒绝策略必须稳定：非法语法、重定向、环境赋值、sudo 和相对 .. 都
// 返回 invalid_arguments，而不是把原始字符串交给远端 shell。
func parseInspectCommand(raw string) (parsedInspectCommand, error) {
	if strings.TrimSpace(raw) == "" {
		return parsedInspectCommand{}, fmt.Errorf("command is required")
	}
	if !utf8.ValidString(raw) {
		return parsedInspectCommand{}, fmt.Errorf("command contains invalid utf-8")
	}
	if len(raw) > maxInspectCommandBytes {
		return parsedInspectCommand{}, fmt.Errorf("command exceeds bound")
	}
	if strings.ContainsAny(raw, "\n\r") {
		return parsedInspectCommand{}, fmt.Errorf("command contains a newline")
	}
	if strings.ContainsRune(raw, 0) {
		return parsedInspectCommand{}, fmt.Errorf("command contains a null byte")
	}
	if strings.Contains(raw, "`") || strings.Contains(raw, "$(") {
		return parsedInspectCommand{}, fmt.Errorf("command substitution is not allowed")
	}
	tokens, err := tokenizeInspect(raw)
	if err != nil {
		return parsedInspectCommand{}, err
	}
	segments, err := splitInspectPipeline(tokens)
	if err != nil {
		return parsedInspectCommand{}, err
	}
	quoted := make([]string, 0, len(segments))
	for _, segment := range segments {
		if err := validateInspectSegment(segment); err != nil {
			return parsedInspectCommand{}, err
		}
		quoted = append(quoted, quoteInspectArgv(segment))
	}
	return parsedInspectCommand{Command: strings.Join(quoted, " | ")}, nil
}

func tokenizeInspect(raw string) ([]inspectToken, error) {
	var tokens []inspectToken
	var current strings.Builder
	quote := rune(0)
	escaped := false
	unquotedGlob := false
	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, inspectToken{value: current.String(), unquotedGlob: unquotedGlob})
		current.Reset()
		unquotedGlob = false
	}
	for _, r := range raw {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if quote == 0 && r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case ';', '&', '<', '>', '(', ')', '{', '}', '\t':
			return nil, fmt.Errorf("command contains rejected syntax")
		case '|':
			flush()
			if len(tokens) > 0 && tokens[len(tokens)-1].pipe {
				return nil, fmt.Errorf("command contains rejected syntax")
			}
			tokens = append(tokens, inspectToken{value: "|", pipe: true})
		default:
			if unicode.IsSpace(r) {
				flush()
				continue
			}
			if isGlobMeta(r) {
				unquotedGlob = true
			}
			current.WriteRune(r)
		}
	}
	if escaped {
		return nil, fmt.Errorf("command ends with a dangling escape")
	}
	if quote != 0 {
		return nil, fmt.Errorf("command has an unclosed quote")
	}
	flush()
	if len(tokens) == 0 {
		return nil, fmt.Errorf("command is required")
	}
	return tokens, nil
}

func splitInspectPipeline(tokens []inspectToken) ([][]inspectToken, error) {
	var segments [][]inspectToken
	current := make([]inspectToken, 0, len(tokens))
	for _, token := range tokens {
		if token.pipe {
			if len(current) == 0 {
				return nil, fmt.Errorf("command has an empty pipeline segment")
			}
			segments = append(segments, current)
			current = nil
			continue
		}
		current = append(current, token)
	}
	if len(current) == 0 {
		return nil, fmt.Errorf("command has an empty pipeline segment")
	}
	segments = append(segments, current)
	if len(segments) > maxInspectPipelineSegs {
		return nil, fmt.Errorf("command pipeline exceeds three segments")
	}
	return segments, nil
}

func validateInspectSegment(argv []inspectToken) error {
	if len(argv) == 0 {
		return fmt.Errorf("command has an empty pipeline segment")
	}
	binary := argv[0].value
	if strings.Contains(binary, "=") {
		return fmt.Errorf("environment assignment is not allowed")
	}
	if strings.Contains(binary, "/") {
		return fmt.Errorf("command binary must be an allowlisted inspect name")
	}
	if _, ok := inspectAllowlist[binary]; !ok {
		if binary == "sudo" {
			return fmt.Errorf("sudo is not allowed")
		}
		return fmt.Errorf("command binary %q is not allowlisted", binary)
	}
	if binary == "find" {
		for _, arg := range argv[1:] {
			switch strings.ToLower(arg.value) {
			case "-exec", "-execdir", "-ok", "-okdir", "-delete":
				return fmt.Errorf("find mutation flags are not allowed")
			}
		}
	}
	if binary == "journalctl" {
		for _, arg := range argv[1:] {
			lower := strings.ToLower(arg.value)
			if lower == "-f" || lower == "--follow" || strings.HasPrefix(lower, "--vacuum-") {
				return fmt.Errorf("journalctl follow or vacuum flags are not allowed")
			}
		}
	}
	for _, arg := range argv {
		if strings.Contains(arg.value, "=") && !strings.HasPrefix(arg.value, "-") {
			return fmt.Errorf("environment assignment is not allowed")
		}
		if strings.Contains(arg.value, "..") {
			return fmt.Errorf("relative parent path segments are not allowed")
		}
		if arg.unquotedGlob {
			return fmt.Errorf("unquoted glob expansion is not allowed")
		}
	}
	return nil
}

// isGlobMeta 只识别未加引号时才会被远端 shell 展开的通配符。
func isGlobMeta(r rune) bool {
	return r == '*' || r == '?' || r == '['
}

func quoteInspectArgv(argv []inspectToken) string {
	quoted := make([]string, 0, len(argv))
	for _, arg := range argv {
		quoted = append(quoted, shellQuote(arg.value))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
