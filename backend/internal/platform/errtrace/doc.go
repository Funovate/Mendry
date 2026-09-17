// Package errtrace 在 error 包装边界捕获有界调用栈，并为最终诊断边界选择最接近
// 底层 cause 的已捕获栈；它不负责记录日志或构造客户端错误响应。
package errtrace
