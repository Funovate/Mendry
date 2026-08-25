// Package application 编排 remediation 用例：同步 coordinator、手动 start 和 console review 读取。
// 冻结 RunStore 签名不变；计划和通知走 companion port，避免把凭据或 SDK 类型带进 application。
package application
