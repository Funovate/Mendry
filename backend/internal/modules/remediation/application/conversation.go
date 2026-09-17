package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	coreapplication "mendry/backend/internal/modules/agentcore/application"
	"mendry/backend/internal/modules/remediation/domain"
)

const (
	maxConversationBytes = 256 << 10
	maxObservationBytes  = 64 << 10
	maxMessageBytes      = 64 << 10
	maxConversationItems = 32
)

// 自动 byte-threshold checkpoint 的阈值派生自 conversation 自身的模型可见
// 字节边界（本文件上方常量），而不是 run 的 evidence/repository byte
// budget：预算维度衡量 connector 外部效果，这里衡量下一轮 provider 输入。
const (
	// contextPressureStepBytes 是 T1 context-pressure 的级别步长：累计模型
	// 可见内容每增长 192 KiB（maxConversationBytes 的 3/4，约一个完整
	// context generation）允许一次自动 checkpoint；首次命中在 192 KiB。
	contextPressureStepBytes = 3 * (maxConversationBytes / 4)
	// toolOutputPressureBytes 是 T2 单次完整工具观察的输出压力线：观察达到
	// 其 maxObservationBytes 输出上限的 3/4（48 KiB）时，该有界观察本身构成
	// configured output pressure。它同时作为自动 checkpoint 的最小字节增长
	// 量子，避免同一内容被重复评估造成 checkpoint spam。
	toolOutputPressureBytes = 3 * (maxObservationBytes / 4)
)

// AgentConversation 保存一次 run 的有界模型历史和工具观察。
// bootstrap 永远保留，旧观察按时间顺序压缩；adapter 错误只以稳定分类进入
// 模型上下文，避免把 stderr、响应体或其他诊断细节变成新的泄漏边界。
type AgentConversation struct {
	bootstrap               string
	bootstrapDelivered      bool
	observations            []conversationItem
	pending                 []conversationItem
	messages                []domain.ModelMessage
	toolFailureAttempts     map[toolFailureKey]int
	dockerRefinementPending bool
	dockerRefinementVersion int
	dockerRefinementReason  string
	nextSequence            int64
	preparedThroughSequence int64
	preparedBootstrap       bool
	// cumulativeBytes 是 provider-context pressure 的保守单调代理，只增不减。
	// 它跟踪内容进入 resilient provider-native 上下文结构的时点，而不是唯一
	// payload 大小或每次请求的精确 wire bytes：
	//   - RecordModelTurn 追加的 user/assistant message 与 AppendToolResult 追加的
	//     native tool message 各按 encodedModelMessage 计入；成功 user message
	//     可能包含先前排队的 bootstrap/pending 文本，因为该 continuation 此时
	//     成为后续 History() 会重放的 native history；
	//   - strict-JSON tool result 与 protocol observation 在进入 pending textual
	//     continuation 队列时按 formatted block 计入；
	//   - bootstrap 在 conversation 创建时计入，确保首轮前已有的大 context 也
	//     对压力可见。
	// 因此 bootstrap/pending 在后续成功 user continuation 中再次序列化时会产生
	// 新的 history 压力并再次计入；这是有意的 churn/replay 计量，不是内容去重
	// 账本。反过来，observations 中仅供 ContextText 有界回退的 non-pending
	// snapshot（native tool result 与 assistant output 的副本）不是独立上下文
	// 结构，不计入，避免同一次 append 同时按 native message 和 snapshot 计量。
	// 被裁剪/逐出的旧内容不回退。该代理用于自动 byte-threshold checkpoint
	// （D2/R13），与 evidence/repository byte budget 无关；restart/continuation
	// 后 conversation 重建，代理从新 bootstrap 确定性重新开始，不持久化旧
	// generation 的字节水位。
	cumulativeBytes int
	// largeToolObservationSequence/Bytes 是最近一次块字节达到
	// toolOutputPressureBytes 的完整 tool_observation（native tool result 与
	// strict-JSON observation，组在 AppendToolResult 内完整落盘）：item
	// sequence 提供 monotone 观察身份，bytes 是含 kind/sequence wrapper 的
	// formatted block 字节。小观察不会覆盖它，因此已被 durable checkpoint
	// 覆盖的大观察不会因后续小观察重新成为“最新”（T2 stale re-fire 修正）；
	// 尚无大观察时为 0/0。
	largeToolObservationSequence int64
	largeToolObservationBytes    int
}

type toolFailureKey struct {
	tool string
	code string
}

// NativeContinuation 返回尚未进入 provider-native 历史的有界增量。
// prepared 标记只在 RecordModelTurn 成功写回时确认，provider 失败可原样重试。
func (c *AgentConversation) NativeContinuation(phasePrompt string) string {
	if c == nil {
		return ""
	}
	c.preparedBootstrap = false
	c.preparedThroughSequence = 0
	parts := []string{boundedText(phasePrompt, maxMessageBytes)}
	remaining := maxMessageBytes - len(parts[0])
	if !c.bootstrapDelivered && c.bootstrap != "" && remaining > len("\n\n## Context\n") {
		parts = append(parts, "## Context\n"+boundedText(c.bootstrap, remaining-len("\n\n## Context\n")))
		c.preparedBootstrap = true
		remaining = maxMessageBytes - len(strings.Join(parts, "\n\n"))
	}
	for _, item := range c.pending {
		if remaining <= len("\n\n## Context\n") {
			break
		}
		block := formatConversationItem(item)
		if len(parts) == 1 {
			block = "## Context\n" + block
		}
		block = boundedText(block, remaining-2)
		parts = append(parts, block)
		c.preparedThroughSequence = item.sequence
		remaining = maxMessageBytes - len(strings.Join(parts, "\n\n"))
	}
	return boundedText(strings.Join(parts, "\n\n"), maxMessageBytes)
}

type conversationItem struct {
	sequence int64
	kind     string
	content  string
}

// NewAgentConversation 创建以 bootstrap metadata 为起点的有界对话。累计字节
// 压力从 bootstrap 的已存储长度开始计数（bootstrap 是首个 continuation 的
// 独立交付，账本在创建时计一次）。
func NewAgentConversation(bootstrap string) *AgentConversation {
	stored := boundedText(bootstrap, maxObservationBytes)
	return &AgentConversation{bootstrap: stored, cumulativeBytes: len(stored)}
}

// CumulativeModelVisibleBytes 返回 conversation 的单调 provider-context
// pressure proxy。它累计内容进入 bootstrap、pending textual continuation 与
// provider-native history 结构的字节；当 bootstrap/pending 随成功 user
// continuation 成为可重放 history 时，重复序列化会作为新的 history 压力计入。
// 因此该值不是唯一 payload 大小，也不是请求 wire size。它是自动
// byte-threshold checkpoint 的保守、确定性压力源；restart 后从重建的
// bootstrap 重新开始，无需持久化旧 generation 的水位。
func (c *AgentConversation) CumulativeModelVisibleBytes() int {
	if c == nil {
		return 0
	}
	return c.cumulativeBytes
}

// LargeToolObservation 返回最近一次块字节达到 toolOutputPressureBytes 的完整
// tool_observation 的 monotone item sequence 与 formatted block 字节；尚无大
// 观察时为 0,0。小观察不覆盖大观察。tracker 的 T2 consumed 水位只与返回的
// sequence 比较，保证每条大观察至多触发一次自动 checkpoint。
func (c *AgentConversation) LargeToolObservation() (sequence int64, bytes int) {
	if c == nil {
		return 0, 0
	}
	return c.largeToolObservationSequence, c.largeToolObservationBytes
}

// ContextText 返回供不支持原生消息历史的 provider 使用的紧凑上下文。
func (c *AgentConversation) ContextText() string {
	if c == nil {
		return ""
	}
	parts := make([]string, 0, len(c.observations)+1)
	if c.bootstrap != "" {
		parts = append(parts, c.bootstrap)
	}
	remaining := maxConversationBytes - len(strings.Join(parts, "\n"))
	if remaining < 0 {
		remaining = 0
	}
	selected := make([]conversationItem, 0, len(c.observations))
	for index := len(c.observations) - 1; index >= 0 && remaining > 0; index-- {
		item := c.observations[index]
		block := formatConversationItem(item)
		if len(block) > remaining {
			block = boundedText(block, remaining)
		}
		selected = append(selected, conversationItem{sequence: item.sequence, kind: item.kind, content: block})
		remaining -= len(block) + 1
	}
	for index := len(selected) - 1; index >= 0; index-- {
		parts = append(parts, selected[index].content)
	}
	return strings.Join(parts, "\n")
}

func formatConversationItem(item conversationItem) string {
	return fmt.Sprintf("%s[%d]: %s", item.kind, item.sequence, item.content)
}

// History 返回当前轮之前的 provider-neutral 消息历史。
func (c *AgentConversation) History() []domain.ModelMessage {
	if c == nil || len(c.messages) == 0 {
		return nil
	}
	selected := coreapplication.SelectPairedHistory(toCoreMessages(c.messages), coreapplication.HistoryBounds{
		MaxItems: maxConversationItems, MaxBytes: maxConversationBytes, MaxMessageBytes: maxMessageBytes,
	})
	return fromCoreMessages(selected)
}

func encodedConversationBytes(messages []domain.ModelMessage) int {
	return coreapplication.EncodedHistoryBytes(toCoreMessages(messages))
}

func encodedModelMessage(message domain.ModelMessage) int {
	encoded, err := json.Marshal(message)
	if err != nil {
		return maxMessageBytes
	}
	return len(encoded)
}

// RecordModelTurn 将本次 user 输入和 provider 返回的 assistant 内容加入历史。
func (c *AgentConversation) RecordModelTurn(userMessage string, result domain.ModelResult) {
	if c == nil {
		return
	}
	c.messages = append(c.messages, domain.ModelMessage{
		Role:    "user",
		Content: boundedText(userMessage, maxMessageBytes),
	})
	// provider-native message 成为可重放 history 时，其编码字节进入单调
	// context-pressure proxy。user continuation 可能重放先前 bootstrap/pending；
	// 这代表新增 history 压力，不是唯一 payload 计数（见 cumulativeBytes）。
	c.cumulativeBytes += encodedModelMessage(c.messages[len(c.messages)-1])
	if c.preparedBootstrap {
		c.bootstrapDelivered = true
	}
	if c.preparedThroughSequence > 0 {
		remaining := c.pending[:0]
		for _, item := range c.pending {
			if item.sequence > c.preparedThroughSequence {
				remaining = append(remaining, item)
			}
		}
		c.pending = remaining
	}
	c.preparedBootstrap = false
	c.preparedThroughSequence = 0
	if len(result.ToolCalls) > 0 {
		c.messages = append(c.messages, domain.ModelMessage{
			Role:      "assistant",
			ToolCalls: append([]domain.ToolCall(nil), result.ToolCalls...),
		})
		c.cumulativeBytes += encodedModelMessage(c.messages[len(c.messages)-1])
		return
	}
	if result.Content != "" {
		c.messages = append(c.messages, domain.ModelMessage{
			Role:    "assistant",
			Content: boundedText(result.Content, maxMessageBytes),
		})
		c.cumulativeBytes += encodedModelMessage(c.messages[len(c.messages)-1])
		c.appendItem("assistant_output", boundedText(result.Content, maxObservationBytes), false)
	}
}

// AppendProtocolError 将可纠正的信封/协议错误变成下一轮可见的观察。
// 模型只看到稳定 code 和修正指引；原始 JSON 与 Go 类型路径不得进入下一轮。
func (c *AgentConversation) AppendProtocolError(phase domain.RunState, corrections ...ProtocolCorrection) {
	if c == nil {
		return
	}
	correction := genericProtocolCorrection(phase)
	if len(corrections) > 0 {
		correction = normalizeProtocolCorrection(phase, corrections[0])
	}
	observation := map[string]interface{}{
		"status": "error",
		"error": map[string]interface{}{
			"code":      correction.Code,
			"retryable": true,
			"message":   correction.Message,
		},
	}
	errorValue := observation["error"].(map[string]interface{})
	if correction.Path != "" {
		errorValue["path"] = correction.Path
	}
	if correction.ExpectedField != "" {
		errorValue["expectedField"] = correction.ExpectedField
	}
	encoded, encodeErr := json.Marshal(observation)
	if encodeErr != nil {
		encoded = []byte(`{"status":"error","error":{"code":"observation_encode_failed","retryable":false,"message":"protocol observation could not be encoded"}}`)
	}
	c.appendItem("protocol_observation", boundedText(string(encoded), maxObservationBytes), true)
}

// AppendRecoveryChallenge 把一次 recoverable recovery challenge（D5）作为
// 下一轮可见的 protocol observation 回喂。envelope 已经过 NewRecoveryChallenge
// 的 sanitize 与边界校验，这里只做有界 JSON 编码，不携带 provider/connector
// 细节或模型输出。
func (c *AgentConversation) AppendRecoveryChallenge(challenge domain.RecoveryChallengeV1) {
	if c == nil {
		return
	}
	observation := map[string]interface{}{
		"status": "recoverable",
		"challenge": map[string]interface{}{
			"schemaVersion":            challenge.SchemaVersion,
			"kind":                     challenge.Kind,
			"severity":                 challenge.Severity,
			"reasonCode":               challenge.ReasonCode,
			"failedActionRef":          challenge.FailedActionRef,
			"availableCapabilities":    append([]string(nil), challenge.AvailableCapabilities...),
			"suggestedRecoveryClasses": append([]string(nil), challenge.SuggestedRecoveryClasses...),
			"attempt":                  challenge.Attempt,
			"remainingBudget":          copyBudget(challenge.RemainingBudget),
			"message":                  challenge.Message,
		},
	}
	encoded, encodeErr := json.Marshal(observation)
	if encodeErr != nil {
		encoded = []byte(`{"status":"recoverable","challenge":{"reasonCode":"encode_failed","message":"recovery challenge could not be encoded"}}`)
	}
	c.appendItem("protocol_observation", boundedText(string(encoded), maxObservationBytes), true)
}

// AppendExhaustionProposalRequest 把 D6 exhaustion proposal 请求（R18）作为
// 下一轮可见的 recoverable observation 回喂。请求只含服务端固定 schema 指令、
// attempt、available capability classes、剩余预算与一个可引用的 outcomeRef；
// 不携带模型输出、凭据或 connector 细节。
func (c *AgentConversation) AppendExhaustionProposalRequest(
	attempt int,
	capabilities []string,
	remaining map[string]int64,
	outcomeRef string,
) {
	if c == nil {
		return
	}
	observation := map[string]interface{}{
		"status": "recoverable",
		"challenge": map[string]interface{}{
			"schemaVersion":         domain.RecoverySchemaVersionV1,
			"kind":                  domain.RecoveryChallengeKindExhaustion,
			"severity":              domain.RecoverySeverityRecoverable,
			"reasonCode":            "exhaustion_proof_required",
			"attempt":               attempt,
			"availableCapabilities": append([]string(nil), capabilities...),
			"remainingBudget":       copyBudget(remaining),
			"outcomeRef":            outcomeRef,
			"message":               exhaustionProposalRequestMessage,
		},
	}
	encoded, encodeErr := json.Marshal(observation)
	if encodeErr != nil {
		encoded = []byte(`{"status":"recoverable","challenge":{"reasonCode":"encode_failed","message":"exhaustion proposal request could not be encoded"}}`)
	}
	c.appendItem("protocol_observation", boundedText(string(encoded), maxObservationBytes), true)
}

// AppendCausalClosureReassessment 要求模型消解“因果已闭环但证据不足”的语义
// 冲突。该观察只包含服务端固定文案，不能携带模型输出或未净化的证据。
func (c *AgentConversation) AppendCausalClosureReassessment() {
	if c == nil {
		return
	}
	observation := map[string]interface{}{
		"status": "error",
		"error": map[string]interface{}{
			"code":      "inconsistent_causal_closure",
			"retryable": true,
			"message": "insufficient_evidence cannot be terminal while causalClosure.explainsOriginalSymptom is true. " +
				"Reassess whether each missing item is material to causal explanation or safe fixability classification. " +
				"Unknown or unmatched test authorization is an audit finding, not a root-cause gap. " +
				"Return the actual fixability when the symptom is explained; otherwise set causalClosure false and " +
				"request bounded collectMoreContext tool calls for material evidence that is still collectible.",
		},
	}
	encoded, encodeErr := json.Marshal(observation)
	if encodeErr != nil {
		encoded = []byte(`{"status":"error","error":{"code":"observation_encode_failed","retryable":false,"message":"causal-closure reassessment could not be encoded"}}`)
	}
	c.appendItem("protocol_observation", boundedText(string(encoded), maxObservationBytes), true)
}

// AppendToolResult 将工具成功、拒绝或 adapter 失败变成下一轮可见的观察。
func (c *AgentConversation) AppendToolResult(req RequestTool, result ToolResult, err error) {
	if c == nil {
		return
	}
	status := "success"
	if err == nil && req.ToolName == ToolDockerLogs {
		if result.RefinementRequired {
			c.dockerRefinementPending = true
			c.dockerRefinementVersion++
			c.dockerRefinementReason = result.RefinementReason
		} else {
			c.dockerRefinementPending = false
			c.dockerRefinementReason = ""
		}
	}
	observation := map[string]interface{}{
		"tool":        req.ToolName,
		"parameters":  redactConversationValue(req.Parameters),
		"bytes":       result.BytesRetrieved,
		"evidenceIds": append([]string(nil), result.EvidenceIDs...),
	}
	if result.ActionRef != "" {
		observation["actionRef"] = result.ActionRef
	}
	if err != nil {
		safe := c.recoveryToolError(req.ToolName, classifyToolError(err))
		status = "error"
		if _, ok := RejectionCode(err); ok {
			status = "rejected"
		}
		observation["error"] = safe
	} else {
		observation["content"] = boundedConversationValue(result.Payload, maxObservationBytes)
	}
	observation["status"] = status
	encoded, encodeErr := json.Marshal(observation)
	if encodeErr != nil {
		encoded = []byte(`{"status":"error","error":{"code":"observation_encode_failed","retryable":false,"message":"tool observation could not be encoded"}}`)
	}
	encodedText := boundedText(string(encoded), maxObservationBytes)
	c.appendItem("tool_observation", encodedText, req.CallID == "")

	// Native provider calls need the assistant tool_call followed by a tool
	// message. The strict JSON requestTool compatibility path uses the textual
	// observation above and does not manufacture an invalid native message pair.
	if req.CallID != "" {
		c.messages = append(c.messages, domain.ModelMessage{
			Role:       "tool",
			ToolCallID: req.CallID,
			Content:    encodedText,
		})
		c.cumulativeBytes += encodedModelMessage(c.messages[len(c.messages)-1])
	}
}

// PendingDockerLogRefinement 返回仍需收窄的 Docker coverage 版本。每个受限
// 结果递增版本，使 coordinator 对同一结果最多回喂一次固定修正。
func (c *AgentConversation) PendingDockerLogRefinement() (int, string, bool) {
	if c == nil {
		return 0, "", false
	}
	return c.dockerRefinementVersion, c.dockerRefinementReason, c.dockerRefinementPending
}

// AppendDockerLogRefinement 要求模型基于原查询元数据收窄时间窗或增加精确
// pattern。服务端固定文案不携带原始日志、路径或模型参数。
func (c *AgentConversation) AppendDockerLogRefinement(reason string) {
	if c == nil {
		return
	}
	if reason != "byte_limit" && reason != "tail_limit" {
		reason = "coverage_limit"
	}
	observation := map[string]interface{}{
		"status": "error",
		"error": map[string]interface{}{
			"code":      "docker_log_refinement_required",
			"retryable": true,
			"reason":    reason,
			"message": "The latest docker.logs result has incomplete coverage and cannot support a terminal diagnosis. " +
				"Use its normalized query metadata and the exact alert event time to issue one narrower docker.logs request. " +
				"Reduce the since/until window and, when a fault anchor exists, add a precise pattern with bounded context_before/context_after. " +
				"Do not treat a tail-only or byte-truncated result as proof that runtime evidence is absent.",
		},
	}
	encoded, encodeErr := json.Marshal(observation)
	if encodeErr != nil {
		encoded = []byte(`{"status":"error","error":{"code":"observation_encode_failed","retryable":false,"message":"Docker log refinement could not be encoded"}}`)
	}
	c.appendItem("protocol_observation", boundedText(string(encoded), maxObservationBytes), true)
}

func (c *AgentConversation) appendItem(kind, content string, nativePending bool) {
	c.nextSequence++
	item := conversationItem{
		sequence: c.nextSequence,
		kind:     kind,
		content:  boundedText(content, maxObservationBytes),
	}
	c.observations = append(c.observations, item)
	// D2/R13 context-pressure proxy：进入 pending textual continuation 的项
	// 在排队时计量；non-pending snapshot（native tool result / assistant output
	// 的 ContextText 副本）不形成额外结构，由对应 native message 计量。
	// 工具观察无论走哪个通道都更新 T2 大观察身份：块字节达到
	// toolOutputPressureBytes 才记录（小观察不覆盖），sequence 保持 monotone。
	// trim 旧 item 不回退，保证压力代理单调且 anti-spam 水位只向前推进。
	blockBytes := len(formatConversationItem(item))
	if nativePending {
		c.cumulativeBytes += blockBytes
	}
	if kind == "tool_observation" && blockBytes >= toolOutputPressureBytes {
		c.largeToolObservationSequence = item.sequence
		c.largeToolObservationBytes = blockBytes
	}
	if nativePending {
		c.pending = append(c.pending, item)
	}
	if len(c.observations) > maxConversationItems {
		c.observations = c.observations[len(c.observations)-maxConversationItems:]
	}
	if len(c.pending) > maxConversationItems {
		c.pending = c.pending[len(c.pending)-maxConversationItems:]
	}
}

type safeToolError struct {
	Code             string `json:"code"`
	Retryable        bool   `json:"retryable"`
	Message          string `json:"message"`
	RecoveryAction   string `json:"recoveryAction"`
	FailureAttempt   int    `json:"failureAttempt"`
	RetriesRemaining int    `json:"retriesRemaining"`
}

const maxModelToolCorrectiveRetries = 1

func (c *AgentConversation) recoveryToolError(tool string, safe safeToolError) safeToolError {
	if c.toolFailureAttempts == nil {
		c.toolFailureAttempts = make(map[toolFailureKey]int)
	}
	key := toolFailureKey{tool: boundedText(tool, 256), code: safe.Code}
	c.toolFailureAttempts[key]++
	safe.FailureAttempt = c.toolFailureAttempts[key]

	if safe.FailureAttempt > maxModelToolCorrectiveRetries {
		safe.RecoveryAction = "use_fallback"
		return safe
	}
	switch {
	case correctableToolErrorCode(safe.Code):
		safe.RecoveryAction = "correct_request"
	case safe.Retryable:
		safe.RecoveryAction = "retry_transient"
	default:
		safe.RecoveryAction = "use_fallback"
		return safe
	}
	safe.RetriesRemaining = maxModelToolCorrectiveRetries
	return safe
}

func correctableToolErrorCode(code string) bool {
	switch code {
	case "invalid_arguments", "path_out_of_scope", "not_found", "connector_not_found":
		return true
	default:
		return false
	}
}

func classifyToolError(err error) safeToolError {
	if err == nil {
		return safeToolError{}
	}
	if code, ok := RejectionCode(err); ok {
		message := "tool request rejected by policy"
		switch code {
		case RejectArguments:
			message = "tool arguments are invalid"
		case RejectPathScope:
			message = "path is outside the repository scope"
		case RejectBudget:
			message = "tool request exceeds its bound"
		case RejectOutOfPhase:
			message = "tool is unavailable in the current phase"
		case RejectUnavailable:
			message = "tool is unavailable"
		}
		return safeToolError{Code: string(code), Message: message}
	}
	if errors.Is(err, context.Canceled) {
		return safeToolError{Code: "canceled", Message: "tool call was canceled"}
	}
	var runtimeErr *domain.ToolRuntimeError
	if errors.As(err, &runtimeErr) {
		code := safeRuntimeCode(runtimeErr.Code)
		return safeToolError{
			Code:      code,
			Retryable: safeRuntimeRetryable(code, runtimeErr.Retryable),
			Message:   safeRuntimeMessage(code),
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return safeToolError{Code: "connector_timeout", Retryable: true, Message: "connector request timed out"}
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "permission"), strings.Contains(lower, "unauthorized"), strings.Contains(lower, "authentication"):
		return safeToolError{Code: "connector_authorization", Message: "connector authorization failed"}
	case strings.Contains(lower, "not found"), strings.Contains(lower, "missing"):
		return safeToolError{Code: "connector_not_found", Message: "requested connector resource was not found"}
	case strings.Contains(lower, "remote command"):
		return safeToolError{Code: "remote_execution", Retryable: true, Message: "remote connector execution failed"}
	default:
		// Unknown adapter errors remain retryable observations, but their raw
		// text never crosses the application boundary into model context.
		return safeToolError{Code: "connector_failure", Retryable: true, Message: "connector request failed"}
	}
}

func safeRuntimeCode(code string) string {
	switch code {
	case "canceled", "connector_timeout", "transport", "rate_limit", "remote_execution",
		"authentication", "authorization", "not_found", "invalid_response", "invalid_configuration",
		"capability_unavailable", "policy_unconfigured", "invalid_arguments", "tool_unavailable",
		"connector_authorization", "connector_not_found", "connector_failure",
		"provider_detail_unavailable", "provider_detail_invalid", "provider_detail_redirect_rejected",
		"provider_detail_oversized", "provider_detail_timeout", "provider_detail_persistence",
		"runtime_evidence_persistence":
		return code
	default:
		return "connector_failure"
	}
}

func safeRuntimeRetryable(code string, requested bool) bool {
	if !requested {
		return false
	}
	switch code {
	case "connector_timeout", "transport", "rate_limit", "remote_execution", "connector_failure", "provider_detail_unavailable", "provider_detail_timeout":
		return true
	default:
		return false
	}
}

func safeRuntimeMessage(code string) string {
	switch code {
	case "canceled":
		return "connector request was canceled"
	case "connector_timeout":
		return "connector request timed out"
	case "transport":
		return "connector transport failed"
	case "rate_limit":
		return "connector rate limit reached"
	case "remote_execution":
		return "remote connector execution failed"
	case "authentication":
		return "connector authentication failed"
	case "authorization", "connector_authorization":
		return "connector authorization failed"
	case "not_found", "connector_not_found":
		return "connector resource was not found"
	case "invalid_response":
		return "connector returned an invalid response"
	case "invalid_configuration":
		return "connector configuration is invalid"
	case "capability_unavailable":
		return "connector capability is unavailable"
	case "policy_unconfigured":
		return "tool policy is unavailable"
	case "invalid_arguments":
		return "tool arguments are invalid"
	case "tool_unavailable":
		return "tool is unavailable"
	case "provider_detail_unavailable":
		return "Tencent CLS detail is unavailable"
	case "provider_detail_invalid":
		return "Tencent CLS detail response is invalid"
	case "provider_detail_redirect_rejected":
		return "Tencent CLS detail redirect was rejected"
	case "provider_detail_oversized":
		return "Tencent CLS detail response exceeded its size bound"
	case "provider_detail_timeout":
		return "Tencent CLS detail request timed out"
	case "provider_detail_persistence":
		return "Tencent CLS detail could not be persisted"
	case "runtime_evidence_persistence":
		return "runtime evidence could not be persisted"
	default:
		return "connector request failed"
	}
}

func boundedConversationValue(value any, limit int) any {
	normalized := normalizeConversationValue(value)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return map[string]interface{}{"truncated": true, "reason": "unserializable"}
	}
	if len(encoded) <= limit {
		return normalized
	}
	return map[string]interface{}{
		"truncated": true,
		"bytes":     len(encoded),
		"preview":   boundedText(string(encoded), limit),
	}
}

func normalizeConversationValue(value any) any {
	switch current := value.(type) {
	case domain.SSHInspectResult:
		// inspect stdout/stderr 与 canonical runtime evidence 使用同一套脱敏：
		// 凭据形态文本、PEM 私钥块与临时 key 路径都不进入模型边界。
		return map[string]interface{}{
			"command":        current.Command,
			"exitCode":       current.ExitCode,
			"stdout":         sanitizeRuntimeOutput(current.Stdout),
			"stderr":         sanitizeRuntimeOutput(current.Stderr),
			"truncated":      current.Truncated,
			"bytesRetrieved": current.BytesRetrieved,
		}
	case domain.DockerLogResult:
		// Docker stdout/stderr 保留完整 operational evidence 的字段形状，
		// 仅在模型边界隔离明显 credential/token 文本。
		query := map[string]interface{}{}
		if !current.Query.Since.IsZero() {
			query["since"] = current.Query.Since.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
		}
		if !current.Query.Until.IsZero() {
			query["until"] = current.Query.Until.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
		}
		if current.Query.Tail > 0 {
			query["tail"] = current.Query.Tail
		}
		if current.Query.Pattern != "" {
			query["pattern"] = current.Query.Pattern
		}
		return map[string]interface{}{
			"container": map[string]interface{}{
				"name": current.Container.Name, "id": current.Container.ID,
				"image": current.Container.Image, "state": current.Container.State,
				"status": current.Container.Status,
			},
			"stdout":              redactConversationText(current.Stdout),
			"stderr":              redactConversationText(current.Stderr),
			"truncated":           current.Truncated,
			"coverage_limited":    current.CoverageLimited,
			"refinement_required": current.RefinementRequired,
			"coverage_reason":     current.CoverageReason,
			"bytesRetrieved":      current.BytesRetrieved,
			"window_lines":        current.WindowLines,
			"returned_lines":      countDockerOutputLines(current.Stdout) + countDockerOutputLines(current.Stderr),
			"filtered":            current.FilteredLines,
			"query":               query,
		}
	default:
		return redactConversationValue(value)
	}
}

func boundedConversationMap(value map[string]interface{}) map[string]interface{} {
	if value == nil {
		return nil
	}
	bounded := boundedConversationValue(value, maxObservationBytes)
	if result, ok := bounded.(map[string]interface{}); ok {
		return result
	}
	return map[string]interface{}{"truncated": true}
}

func redactConversationValue(value any) any {
	switch current := value.(type) {
	case domain.FileContent:
		// []byte 会被默认 JSON 编码为可还原的 base64；先转成文本，才能在
		// 模型边界应用和普通日志/证据相同的凭据脱敏规则。
		return map[string]interface{}{
			"path":      redactConversationText(current.Path),
			"content":   redactConversationText(string(current.Content)),
			"truncated": current.Truncated,
			"reason":    current.Reason,
		}
	case domain.WorkspaceFile:
		// 工作区文件同 repository FileContent 一样先转成文本，避免 []byte
		// 被 encoding/json 编成可还原的 base64。
		return map[string]interface{}{
			"path":      redactConversationText(current.Path),
			"content":   redactConversationText(string(current.Content)),
			"truncated": current.Truncated,
			"reason":    current.Reason,
		}
	case domain.PatchResult:
		return map[string]interface{}{
			"workspaceId": current.WorkspaceID, "applied": current.Applied,
			"alreadyApplied": current.AlreadyApplied, "artifactRef": current.ArtifactRef,
			"contentHash": current.ContentHash, "resultTreeHash": current.ResultTreeHash,
			"changedFiles": current.ChangedFiles, "bytesRetrieved": current.BytesRetrieved,
			"summary": redactConversationText(current.Summary),
		}
	case domain.ValidationResult:
		return map[string]interface{}{
			"workspaceId": current.WorkspaceID, "commandId": current.CommandID,
			"commandVersion": current.CommandVersion, "passed": current.Passed,
			"exitCode": current.ExitCode, "outputArtifactRef": current.OutputArtifactRef,
			"outputHash": current.OutputHash, "outputExcerpt": redactConversationText(current.OutputExcerpt),
			"bytesRetrieved": current.BytesRetrieved, "summary": redactConversationText(current.Summary),
		}
	case domain.WorkspaceStatus:
		return map[string]interface{}{
			"workspaceId": current.Identity.WorkspaceID, "baselineCommit": current.Identity.BaselineCommit,
			"baseTreeHash": current.Identity.BaseTreeHash, "currentTreeHash": current.Identity.CurrentTreeHash,
			"version": current.Identity.Version, "clean": current.Clean,
			"changedFiles": current.ChangedFiles, "bytes": current.Bytes,
		}
	case []byte:
		return redactConversationText(string(current))
	case string:
		return redactConversationText(current)
	case map[string]interface{}:
		out := make(map[string]interface{}, len(current))
		for key, item := range current {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "password") || strings.Contains(lower, "secret") ||
				strings.Contains(lower, "token") || strings.Contains(lower, "authorization") ||
				strings.Contains(lower, "credential") || strings.Contains(lower, "api_key") || strings.Contains(lower, "apikey") {
				out[key] = "[redacted]"
				continue
			}
			out[key] = redactConversationValue(item)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(current))
		for index, item := range current {
			out[index] = redactConversationValue(item)
		}
		return out
	case nil, bool, float64, float32,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return value
	default:
		// Tool adapters may return a domain struct or a JSON-compatible named
		// type. Normalize those values before recursively applying key/value
		// redaction; an encoding failure becomes a safe marker.
		encoded, err := json.Marshal(value)
		if err != nil {
			return "[unserializable]"
		}
		var normalized interface{}
		if err := json.Unmarshal(encoded, &normalized); err != nil {
			return "[unserializable]"
		}
		return redactConversationValue(normalized)
	}
}

var (
	conversationPEMPattern         = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`)
	conversationSecretTokenPattern = regexp.MustCompile(`(?i)sk-[A-Za-z0-9_-]{8,}`)
	conversationBearerPattern      = regexp.MustCompile(`(?i)(\bbearer\s+)[A-Za-z0-9._~+/=-]{8,}`)
	conversationRemotePattern      = regexp.MustCompile(`(?i)\b(https?|ssh)://[^/\s:@]+:[^@\s/]+@`)
	conversationAssignmentPattern  = regexp.MustCompile(`(?i)(\b(?:password|token|secret|authorization|api[-_]?key|credential)\b\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
	conversationTempKeyPattern     = regexp.MustCompile(`(?i)(?:^|[\s:=])(/[^\s]*mendry-ssh(?:log|inspect)?-[^\s]+)`)
)

func redactConversationText(value string) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	value = conversationPEMPattern.ReplaceAllString(value, "[redacted]")
	value = conversationSecretTokenPattern.ReplaceAllString(value, "[redacted]")
	value = conversationBearerPattern.ReplaceAllString(value, "${1}[redacted]")
	value = conversationRemotePattern.ReplaceAllString(value, "${1}://[redacted]@")
	return conversationAssignmentPattern.ReplaceAllString(value, "${1}[redacted]")
}

func copyMessage(message domain.ModelMessage) domain.ModelMessage {
	message.ToolCalls = append([]domain.ToolCall(nil), message.ToolCalls...)
	return message
}

func boundedText(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
