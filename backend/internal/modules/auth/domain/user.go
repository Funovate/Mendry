package domain

import (
	"fmt"
	"regexp"
	"strings"
)

var usernamePattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{2,63}$`)

// User 是可安全越过 application 边界的用户身份，不包含 password hash。
type User struct {
	ID       string
	Username string
	Enabled  bool
}

// NormalizeUsername 将登录名收敛为数据库唯一约束使用的小写形式。
func NormalizeUsername(value string) (string, error) {
	username := strings.ToLower(strings.TrimSpace(value))
	if !usernamePattern.MatchString(username) {
		return "", fmt.Errorf("username must start with a letter and contain 3 to 64 safe characters")
	}
	return username, nil
}
