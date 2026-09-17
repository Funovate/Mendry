// Package redis 将 auth Session port 实现为可过期、可撤销的 Redis 记录。
// Redis key 只包含 raw token 的 SHA-256，value 只包含安全用户身份和绝对过期时间。
package redis
