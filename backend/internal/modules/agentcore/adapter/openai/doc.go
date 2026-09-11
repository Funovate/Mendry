// Package openai 提供不依赖业务身份的 OpenAI-compatible Chat Completions adapter。
// 调用方在 composition 阶段注入 binding；本包不读取环境、项目、账户或 secret store。
package openai
