// Package git 实现 remediation 的只读仓库适配器。
// 它在每次 fetch 后读取配置的 production branch 最新代码，
// 并在适配器内部注入凭据；返回值、日志和错误不得包含秘密或带 userinfo 的 URL。
package git
