package domain_test

import (
	"context"
	"reflect"
	"testing"

	"fixthe/backend/internal/modules/remediation/domain"
)

// TestPortContractNoCredentials 验证所有 port 接口不暴露凭据、裸客户端或 provider SDK 类型。
func TestPortContractNoCredentials(t *testing.T) {
	// 检查所有 port 方法的参数和返回值
	ports := []interface{}{
		(*domain.RepositoryReadPort)(nil),
		(*domain.EvidenceLogPort)(nil),
		(*domain.SSHInspectPort)(nil),
		(*domain.LLMProviderPort)(nil),
		(*domain.RunStore)(nil),
	}

	forbiddenTypes := []string{
		"*url.Userinfo",
		"*ssh.ClientConfig",
		"*ssh.Signer",
		"*http.Client",
		"*tls.Config",
		"*pgxpool.Pool",
		"*redis.Client",
		"password",
		"credential",
		"token",
		"secret",
		"OpenAI",
		"Anthropic",
	}

	for _, port := range ports {
		portType := reflect.TypeOf(port).Elem()
		t.Run(portType.Name(), func(t *testing.T) {
			for i := 0; i < portType.NumMethod(); i++ {
				method := portType.Method(i)
				methodType := method.Type

				// 检查参数（跳过 context.Context，接口方法没有 receiver）
				for j := 1; j < methodType.NumIn(); j++ {
					param := methodType.In(j)
					checkType(t, method.Name, "parameter", param, forbiddenTypes)
				}

				// 检查返回值
				for j := 0; j < methodType.NumOut(); j++ {
					ret := methodType.Out(j)
					checkType(t, method.Name, "return", ret, forbiddenTypes)
				}
			}
		})
	}
}

func checkType(t *testing.T, methodName, location string, typ reflect.Type, forbidden []string) {
	typeName := typ.String()

	for _, f := range forbidden {
		if contains(typeName, f) {
			t.Errorf("method %s %s type %q contains forbidden pattern %q",
				methodName, location, typeName, f)
		}
	}

	// 递归检查结构体字段
	if typ.Kind() == reflect.Struct {
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			checkType(t, methodName, location+"."+field.Name, field.Type, forbidden)
		}
	}

	// 检查指针和切片的元素类型
	if typ.Kind() == reflect.Ptr || typ.Kind() == reflect.Slice {
		checkType(t, methodName, location, typ.Elem(), forbidden)
	}
}

func contains(s, substr string) bool {
	// 简单的子串匹配，忽略大小写
	sLower := toLower(s)
	substrLower := toLower(substr)
	return indexOf(sLower, substrLower) >= 0
}

func toLower(s string) string {
	var result []rune
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			result = append(result, r+32)
		} else {
			result = append(result, r)
		}
	}
	return string(result)
}

func indexOf(s, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// TestContextIsFirstParameter 验证所有方法的第一个参数是 context.Context。
func TestContextIsFirstParameter(t *testing.T) {
	ports := []interface{}{
		(*domain.RepositoryReadPort)(nil),
		(*domain.EvidenceLogPort)(nil),
		(*domain.SSHInspectPort)(nil),
		(*domain.LLMProviderPort)(nil),
		(*domain.RunStore)(nil),
	}

	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()

	for _, port := range ports {
		portType := reflect.TypeOf(port).Elem()
		t.Run(portType.Name(), func(t *testing.T) {
			for i := 0; i < portType.NumMethod(); i++ {
				method := portType.Method(i)
				methodType := method.Type

				if methodType.NumIn() < 1 {
					t.Errorf("method %s has no parameters", method.Name)
					continue
				}

				firstParam := methodType.In(0)
				if !firstParam.Implements(ctxType) {
					t.Errorf("method %s first parameter is %s, expected context.Context",
						method.Name, firstParam)
				}
			}
		})
	}
}
