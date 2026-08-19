# Signed inbound webhook URL and ingress

## Goal

让已配置 webhook trigger 的项目能被外部监控叫醒：FixThe 生成一条不可猜测的长 token，在配置页展示可复制的 `POST /hooks/{token}`；对方把告警通知原文 POST 过来，我们据此定位项目、保存原文、打开或更新 `P2` 事故，并走现有 remediation harness。对方不必按我们的 JSON 建模。

## Background

Trigger 配置已能持久化 `signed_webhook` + `webhook_hmac`，但没有公开入口，HMAC 也从未用于运行时识别。已登录 `POST /api/v1/projects/{projectKey}/observations` 不是公开 webhook。

实际告警源（例如腾讯云监控）发的是通知原文，类似：

```text
【告警】测试信息
告警等级：
所属账号：100013370924(测试信息)
监控对象：
触发条件：
当前数据：
· 测试信息
触发时间：2026-08-10 11:41:44
附加内容：测试信息
```

日志检索、关键词抽取、证据挂载属于已有 remediation harness：`incidents.Create` 在 `P1`/`P2` 时调用 `RemediationTrigger.Emit`，harness 再用 `evidence.search` 查已配置的日志源。Webhook 不得再实现一套检索。

相关合同：`.trellis/spec/backend/project-guidelines.md`、`incident-guidelines.md`、`authentication-guidelines.md`、`logging-guidelines.md`。

## Decisions

- D1：认证用服务端生成的随机长 token，不用 HMAC 对 body 签名。用户不必再创建 `webhook_hmac` 凭据。
- D2：token 用来定位项目。未知、错误或未启用的 token 一律拒绝，且不泄露项目是否存在。
- D3：用户不手填入站 URL。入口由服务端派生，token 由服务端生成。
- D4：token 嵌在 URL 路径里。配置页展示并复制完整地址 ` {publicBase}/hooks/{token}`。对方只需配这一条 URL。Access log 必须打码，不得整段打印 token。
- D5：路径 token 验通后必须开或更新事故。这是 trigger，不是只落 Event Stream。
- D6：按项目内 fingerprint 去重。没有同 fingerprint 的事故则新建 `Open`；已有且仍为 `Open` 则更新 lastSeen、occurrenceCount +1，不另开一条。已 `Closed` / `Recovered` 的事故本次不自动重开。
- D7：入站载荷按不透明原文接收。接受 `text/plain`、`application/json` 或表单正文，整段保存。不要求 `title` / `fingerprint` / `sourceId`。标题和 fingerprint 从正文首个非空行派生；不能对整段正文做哈希，因为同一告警每次都有不同「触发时间」。
- D8：Webhook 开出事故后走现有 incident → remediation trigger → harness 缝。本切片不新增检索适配器，也不在 ingress 里直接调 SSH/CLS/MCP。
- D9：Webhook 新建事故默认优先级 `P2`。不解析告警正文里的「告警等级」。手工创建事故的默认 `Info` 不变。
- D10：项目 admin 可在配置页随时复制完整入站 URL。另提供「重新生成」：旧 token 立刻失效。列表、日志、审计仍打码。

## Requirements

- R1：选择 webhook trigger 时，系统生成一条足够长、不可猜测的 token。用户不手写 token。
- R2：配置页和 Trigger 步只读展示完整入站 URL，支持一键复制。用户不能编辑该 URL。`custom_rule` 不展示。
- R3：公开 `POST /hooks/{token}` 不依赖登录 Session。路径 token 有效则定位项目，把请求原文写入该项目 Event Stream，并按派生 fingerprint 打开或更新事故。不得要求调用方知道内部 `sourceId`；归属使用该项目已配置的 source / environment。
- R4：项目 admin 读取配置时可拿到完整入站 URL。列表、日志、审计、非 admin 响应不得出现 token 明文。存储同时保留查找哈希和可解密密文。重新生成会使旧 token 立刻失效。
- R5：错误 token、trigger 未启用、项目或 source 不可用时拒绝，不创建 Observation / Incident。
- R6：不得把已登录 Observation POST 改成公开接口。
- R7：Webhook 开事故必须走现有 incident 创建路径，使 `P2` 事故进入已有 `RemediationTrigger`。不得在 webhook 模块内复制 harness 或日志检索。重复投递只更新已有 `Open` 事故的 lastSeen / occurrenceCount，不改优先级，不重复点火。
- R8：派生 URL 需要部署级公共基址；缺失或非法时 API 启动失败，不得静默拼出错误地址。

## Out of scope

- HMAC body 签名、时间戳窗口、重放库。
- 无 token 的裸 URL。
- `custom_rule` 执行器。
- 把腾讯云 / CLS / GitHub 告警解析成稳定 JSON 字段。
- 在 webhook 内抽取关键词并查询 SSH / CLS / MCP。
- 出站通知 webhook。
- 把已登录 Observation POST 改成公开接口。
- 把 trigger kind 从 `signed_webhook` 重命名为 `webhook`（可后续做；本切片只去掉 HMAC 必填）。

## Acceptance Criteria

- [ ] AC1：webhook trigger 配置后，页面展示可复制的只读入站 URL。用户不能编辑该字段。`custom_rule` 不展示。不再要求选择 HMAC 凭据。
- [ ] AC2：外部系统可对 `POST /hooks/{token}` 投递未登录的原文（纯文本或任意 JSON）；有效 token 保存原文，并以 `P2` 打开或更新该项目事故。不要求对方带内部字段。
- [ ] AC3：错误或未知 token、trigger 未启用、source 不可用均拒绝，且不创建 Observation / Incident。对外不区分「项目不存在」和「token 错误」。
- [ ] AC4：项目 admin 可再次复制完整 URL；viewer/operator 看不到 token。列表、日志、审计不含 token 明文。重新生成后旧 URL 失效。
- [ ] AC5：配置 API / 数据库不持久化用户填写的 webhook URL。
- [ ] AC6：同一项目同一 fingerprint 的重复投递更新已有 `Open` 事故，不另开一条，不重复点火 remediation；`Closed` / `Recovered` 事故不自动重开，但仍保存本次 Observation。
- [ ] AC7：Webhook 新建 `P2` 事故进入现有自动 remediation；webhook 模块内不做日志检索。
