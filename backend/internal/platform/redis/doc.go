// Package redis 管理可选的进程级 go-redis client、连接池观测和健康检查。
// 该包只服务 disposable operational state；Redis 不得作为业务真相或任务队列。
// application/domain contract 不得暴露 go-redis 类型，具体 feature port 由消费方定义。
package redis
