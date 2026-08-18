package config

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// WithOptionalDotEnv 在 base lookup 之上叠加工作目录中的 dotenv 文件。
// 进程环境里已经存在的 key 保持不变，避免本地文件覆盖显式 export 或编排系统注入的值。
// 文件缺失时返回 base，因此生产环境没有 `.env` 时仍可只靠环境变量启动。
// 解析失败只报告路径和行号，不回显赋值内容。
func WithOptionalDotEnv(base Lookup, path string) (Lookup, error) {
	if base == nil {
		return nil, fieldError(dotenvPath(path), "lookup is required")
	}
	if strings.TrimSpace(path) == "" {
		return nil, fieldError(".env", "path is required")
	}

	values, err := readOptionalDotEnv(path)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return base, nil
	}

	return func(key string) (string, bool) {
		if value, ok := base(key); ok {
			return value, true
		}
		value, ok := values[key]
		return value, ok
	}, nil
}

func readOptionalDotEnv(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("configuration %s cannot be read", path)
	}
	return parseDotEnv(path, data)
}

func parseDotEnv(path string, data []byte) (map[string]string, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	scanner := bufio.NewScanner(bytes.NewReader(data))
	values := make(map[string]string)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		key, value, skip, err := parseDotEnvLine(scanner.Text())
		if err != nil {
			return nil, fmt.Errorf("configuration %s:%d %s", path, lineNumber, err.Error())
		}
		if skip {
			continue
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("configuration %s cannot be parsed", path)
	}
	return values, nil
}

func parseDotEnvLine(line string) (string, string, bool, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", true, nil
	}
	if isExportAssignment(trimmed) {
		return "", "", false, fmt.Errorf("must be KEY=VALUE")
	}

	key, rawValue, found := strings.Cut(trimmed, "=")
	if !found {
		return "", "", false, fmt.Errorf("must be KEY=VALUE")
	}
	key = strings.TrimSpace(key)
	if !isEnvKey(key) {
		return "", "", false, fmt.Errorf("has an invalid key")
	}
	value, err := parseDotEnvValue(rawValue)
	if err != nil {
		return "", "", false, err
	}
	return key, value, false, nil
}

func parseDotEnvValue(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}

	quote := value[0]
	if quote == '"' || quote == '\'' {
		if len(value) < 2 || value[len(value)-1] != quote {
			return "", fmt.Errorf("has invalid quoting")
		}
		return value[1 : len(value)-1], nil
	}
	if strings.Contains(value, "#") {
		return "", fmt.Errorf("has an inline comment")
	}
	return value, nil
}

func isExportAssignment(line string) bool {
	if line == "export" {
		return true
	}
	if !strings.HasPrefix(line, "export") {
		return false
	}
	return unicode.IsSpace(rune(line[len("export")]))
}

func isEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for index, character := range key {
		if index == 0 {
			if character != '_' && !unicode.IsLetter(character) {
				return false
			}
			continue
		}
		if character != '_' && !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			return false
		}
	}
	return true
}

func dotenvPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ".env"
	}
	return path
}
