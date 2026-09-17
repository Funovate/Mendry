// Package file 提供 Agent Core 的单进程 durable JSON RunStore。
// 它负责进程锁、严格快照校验和 atomic fsync+rename，不负责网络或业务配置解析。
package file
