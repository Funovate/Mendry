// Package git 实现 remediation 的只读仓库适配器。
// 它在不可变 deployed commit 上执行 list_tree / read_file / search / history，
// 并在适配器内部注入凭据；返回值、日志和错误不得包含秘密或带 userinfo 的 URL。
package git
