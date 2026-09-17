// Package awssns 验证 AWS SNS HTTPS envelope，并完成受限的订阅确认。
// 该 adapter 不读取 AWS API、不接收 Access Key，也不持久化 control capability URL。
package awssns
