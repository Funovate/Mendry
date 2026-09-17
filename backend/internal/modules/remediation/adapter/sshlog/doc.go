// Package sshlog 实现 remediation 的 SSH 证据适配器。
// Search/GetContext 仍服务旧的日志窗口路径；Inspect 执行 gateway 重建后的
// inspect 命令。有界 stdout/stderr 由 gateway 投影为 canonical runtime evidence
// 后持久化，原始输出不会直接进入模型上下文。SSH 凭据只在适配器内解密并在用后清零。
package sshlog
