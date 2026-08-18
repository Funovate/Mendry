# 统一后端成功响应包装

## Goal

为后端 API 建立一致的成功响应契约，并同步前端解析与自动化测试，降低各接口对响应形状的特殊判断成本。

## Background

- `backend/internal/platform/httpserver.WriteError` 已统一返回 `{"error": {...}}` 错误 envelope。
- `backend/internal/platform/httpserver.WriteJSON` 当前直接序列化成功值。
- 认证、项目、事故、观察和系统 handler 当前分别返回裸对象、裸数组或 `204 No Content`。
- `frontend/src/api.ts` 当前直接使用 zod schema 解析成功响应的对象/数组，错误单独解析 `error` envelope。
- 仓库中未发现现有成功响应 envelope 契约；该任务需要建立新契约并同步所有受影响调用方。

## Requirements

### R1. 成功响应统一包装

所有带 JSON body 的 API 成功响应必须使用统一的成功 envelope；对象和列表都必须通过同一 `data` 字段承载，不能继续返回裸对象或裸数组。

成功 envelope 还必须提供统一的顶层 `code` 和 `message`，并提供请求级元信息。`code` 固定使用字符串机器码 `"ok"`，`message` 固定使用安全的用户可见文本 `"OK"`。列表响应必须提供服务端计算的 `total`，不能让前端用当前页长度推断总数。

拟定形状：

```json
{
  "code": "ok",
  "message": "OK",
  "data": {},
  "meta": {
    "requestId": "8a0b1b0f7567f11440f392ada3988b0d",
    "durationMs": 12,
    "total": 1
  }
}
```

`meta.total` 仅用于列表响应；单对象响应不需要伪造总数。`meta.requestId` 与
`X-Request-ID` 保持一致，`meta.durationMs` 由 HTTP 边界从请求开始到成功响应写出前测量，
用于诊断而不是业务计时。`204 No Content` 继续为空 body。

### R2. 错误契约保持稳定

现有 `{"error": {"code", "message", "requestId"}}` 错误 envelope、HTTP 状态码、request ID 和错误语义保持兼容。错误响应不需要伪造 `data`、`total` 或 `durationMs`。

### R3. 前端契约同步

前端 API 客户端必须在一个公共解析层校验并解包成功 envelope；业务组件和 query/mutation 调用方继续接收现有领域对象/数组类型，不得在各组件重复解包。列表 API 的公共返回类型同时暴露 `items` 和 `total`，以便页面需要时使用服务端总数。

### R4. 空响应和基础系统路由

`204 No Content` 继续为空 body；`/livez`、`/readyz` 和 `/api/v1/system/status` 的 JSON 成功响应也必须遵循最终确定的成功 envelope。

### R5. 测试与文档

后端公共响应测试、各 HTTP adapter 成功路径测试、前端 API 测试和端到端 mock contract 必须覆盖新的 envelope；相关 backend/frontend spec 需要记录最终契约。

## Acceptance Criteria

- 所有 JSON 成功 endpoint 的 body 均符合最终确定的成功 envelope，列表为空时仍保持稳定的数组字段而不是 `null`。
- 成功 envelope 的 `code` 始终为 `"ok"`、`message` 始终为 `"OK"`；对象响应含 `data`，列表响应含 `data` 和服务端 `meta.total`。
- 成功 JSON 响应的 `meta.requestId` 与 `X-Request-ID` 相同，`meta.durationMs` 为非负请求耗时毫秒数；错误响应继续使用既有错误 envelope。
- 所有现有错误响应继续符合错误 envelope，且错误 request ID 与 header 一致。
- 前端 API 公共层能解析成功 envelope，现有页面行为和类型不需要业务组件感知 envelope。
- 认证、项目、事故、观察、系统路由以及端到端 mock 测试均通过，并且没有裸 JSON 成功响应遗留。
- `make check`、`make build`、`make generate-check` 及 frontend 检查通过。

## Out of Scope

- 不改变业务字段命名、HTTP 状态码、鉴权、项目隔离或错误 code 语义。
- 不为 `204 No Content` 强制增加 JSON body，除非最终契约决策明确要求改为带 body 的成功响应。
- 不引入除 `total` 之外的分页游标、排序协议、额外版本迁移或旧/新 envelope 双写兼容层。
