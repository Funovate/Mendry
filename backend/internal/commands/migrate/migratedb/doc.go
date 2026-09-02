// Package migratedb 包含由 sqlc 生成的 migration metadata typed query。
// 生成文件不得手工修改；runner 只通过本 package 访问 migration history 和
// advisory lock，避免在 Go 控制流中散落 SQL 字符串。
package migratedb
