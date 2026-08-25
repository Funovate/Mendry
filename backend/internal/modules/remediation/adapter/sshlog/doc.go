// Package sshlog 实现 remediation 的 SSH 证据适配器。
// Search/GetContext 仍服务旧的日志窗口路径；Inspect 执行 gateway 重建后的
// inspect 命令，并把有界 stdout/stderr 原样返回给模型。SSH 凭据只在适配器内解密并在用后清零。
package sshlog
