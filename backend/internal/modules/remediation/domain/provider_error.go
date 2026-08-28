package domain

// ProviderRuntimeError 是 provider adapter 到 remediation coordinator 的安全失败分类。
// Code 只允许使用 adapter 与 application 共同约定的低基数值；Cause 仅用于
// errors.Is / errors.As 保留兼容性，不应被持久化或直接展示给模型。
type ProviderRuntimeError struct {
	Code      string
	Retryable bool
	Cause     error
}

// Error 返回不含 provider response、credential 或底层错误正文的安全分类。
func (e *ProviderRuntimeError) Error() string {
	if e == nil || e.Code == "" {
		return "provider runtime failure"
	}
	return e.Code
}

// Unwrap 保留 typed cause 的判定能力，同时让 application 只依赖安全 Code。
func (e *ProviderRuntimeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
