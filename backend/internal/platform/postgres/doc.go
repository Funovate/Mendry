// Package postgres 管理进程独占的 pgxpool、数据库可观测性和事务基础原语。
// 该包不拥有业务 repository 或 schema；业务 SQL 和 row mapping 必须留在对应
// module 的 adapter/postgres 内，application/domain contract 不得暴露 pgx 类型。
package postgres
