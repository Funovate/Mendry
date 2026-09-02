package domain

import (
	"fmt"
	"regexp"
	"strings"
)

// Role 是登录用户在 MVP 中拥有的稳定授权角色。
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
)

var usernamePattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{2,63}$`)

// User 是可安全越过 application 边界的用户身份，不包含 password hash。
type User struct {
	ID       string
	Username string
	Role     Role
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

// ParseRole 拒绝数据库或 Session 中未知的角色值。
func ParseRole(value string) (Role, error) {
	role := Role(value)
	switch role {
	case RoleAdmin, RoleOperator, RoleViewer:
		return role, nil
	default:
		return "", fmt.Errorf("unknown user role")
	}
}
