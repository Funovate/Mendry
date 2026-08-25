// Package openai 实现 remediation 的 LLMProviderPort。
// 它用净 HTTP 调用 OpenAI Chat Completions，模型固定为 gpt-5.6；
// API 密钥来自项目加密凭据（约定名 openai / kind http_bearer），SDK 类型不越过包边界。
package openai
