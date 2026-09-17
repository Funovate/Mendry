// Package dev 保存必须由开发者显式执行、且不会进入 API 启动流程的本地数据。
package dev

import _ "embed"

// seedSQL 是项目、采集配置和事故的幂等开发数据。
//
//go:embed seed.sql
var seedSQL string

// SeedSQL 返回编译进 seed 命令的开发数据 SQL。
func SeedSQL() string { return seedSQL }
