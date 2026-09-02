package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"

	"fixthe/backend/internal/modules/remediation/domain"
)

// maxRuntimeEvidenceBytes 是 canonical runtime evidence payload 的上限，与模型
// 可见的 observation 上限一致，保证持久化 payload 与模型可见 payload 是同一
// 个有界投影（PRD R9）。
const maxRuntimeEvidenceBytes = 64 << 10

// RuntimeEvidenceWriter 是 application 拥有的窄 runtime evidence 写入边界，只
// 暴露 AppendEvidence。ToolGateway 通过显式 composition-root wiring 注入；
// 没有 writer 时 observed SSH/Docker 工具 fail closed，绝不把未持久化的原始
// 输出暴露给模型或 observer。
type RuntimeEvidenceWriter interface {
	AppendEvidence(context.Context, domain.StoredEvidence) (domain.StoredEvidence, error)
}

// runtimeEvidencePersistenceError 是稳定、不可重试的 canonical evidence 投影/
// 持久化失败。原始 adapter 输出在 application 边界被丢弃，不进入模型、tool
// invocation 或 operator log。
func runtimeEvidencePersistenceError(message string) error {
	return &domain.ToolRuntimeError{Code: "runtime_evidence_persistence", Message: message}
}

// isRuntimeEvidenceTool 报告工具是否产生需要持久化的运行时证据。
func isRuntimeEvidenceTool(tool string) bool {
	return tool == ToolSSHInspect || tool == ToolDockerLogs
}

// persistRuntimeEvidence 在 observed 执行路径上把 SSH/Docker 的成功 adapter
// 输出投影为 canonical payload 并持久化；返回 canonical payload 与证据 ID。
// 投影或持久化失败时返回稳定错误，调用方必须丢弃原始 result。
func (g *ToolGateway) persistRuntimeEvidence(
	ctx context.Context,
	run RunIdentity,
	scope domain.EvidenceScope,
	phase domain.RunState,
	catalogVersion string,
	tool string,
	result ToolResult,
) (any, string, error) {
	if g.runtimeWriter == nil {
		return nil, "", runtimeEvidencePersistenceError("runtime evidence writer is unavailable")
	}
	if strings.TrimSpace(run.RunID) == "" || strings.TrimSpace(run.IncidentID) == "" ||
		strings.TrimSpace(scope.ProjectID) == "" || strings.TrimSpace(scope.EnvironmentID) == "" ||
		strings.TrimSpace(scope.SourceID) == "" {
		return nil, "", runtimeEvidencePersistenceError("runtime evidence ownership is incomplete")
	}
	switch tool {
	case ToolSSHInspect:
		return g.persistSSHInspectEvidence(ctx, run, scope, phase, catalogVersion, result)
	case ToolDockerLogs:
		return g.persistDockerLogEvidence(ctx, run, scope, phase, catalogVersion, result)
	default:
		return nil, "", runtimeEvidencePersistenceError("tool does not produce runtime evidence")
	}
}

// persistSSHInspectEvidence 投影 ssh.inspect 的 canonical payload：{command,
// exitCode, stdout, stderr, truncated, bytesRetrieved}。stdout/stderr 经过同一
// 套凭据脱敏并裁剪到预算内；模型可见内容与持久化内容逐字节一致。
func (g *ToolGateway) persistSSHInspectEvidence(
	ctx context.Context,
	run RunIdentity,
	scope domain.EvidenceScope,
	phase domain.RunState,
	catalogVersion string,
	result ToolResult,
) (any, string, error) {
	inspect, ok := result.Payload.(domain.SSHInspectResult)
	if !ok {
		return nil, "", runtimeEvidencePersistenceError("ssh inspect result is invalid")
	}
	stdout, stdoutTruncated := boundRuntimeText(sanitizeRuntimeOutput(inspect.Stdout), maxRuntimeEvidenceBytes)
	stderr, stderrTruncated := boundRuntimeText(sanitizeRuntimeOutput(inspect.Stderr), maxRuntimeEvidenceBytes)
	payload := map[string]interface{}{
		"command":        inspect.Command,
		"exitCode":       inspect.ExitCode,
		"stdout":         stdout,
		"stderr":         stderr,
		"truncated":      inspect.Truncated || stdoutTruncated || stderrTruncated,
		"bytesRetrieved": inspect.BytesRetrieved,
	}
	fitRuntimePayload(payload)
	evidence := domain.StoredEvidence{
		Provider: "ssh", EvidenceKind: domain.EvidenceKindRuntime,
		Classification:         domain.EvidenceCorrelatedSupport,
		Outcome:                runtimeEvidenceOutcome(stdout, stderr),
		Available:              true,
		Primary:                true,
		TemporalCorrelation:    false,
		OperationalCorrelation: true,
	}
	return g.appendRuntimeEvidence(ctx, run, scope, phase, catalogVersion, ToolSSHInspect, evidence, payload)
}

// persistDockerLogEvidence 投影 docker.logs 的 canonical payload：sanitized
// container identity、stdout/stderr、截断标记、字节数、覆盖计数与归一化查询
// 元数据。返回日志行时分类为 direct_fault，否则 correlated_supporting。
func (g *ToolGateway) persistDockerLogEvidence(
	ctx context.Context,
	run RunIdentity,
	scope domain.EvidenceScope,
	phase domain.RunState,
	catalogVersion string,
	result ToolResult,
) (any, string, error) {
	docker, ok := result.Payload.(domain.DockerLogResult)
	if !ok {
		return nil, "", runtimeEvidencePersistenceError("docker logs result is invalid")
	}
	stdout, stdoutTruncated := boundRuntimeText(sanitizeRuntimeOutput(docker.Stdout), maxRuntimeEvidenceBytes)
	stderr, stderrTruncated := boundRuntimeText(sanitizeRuntimeOutput(docker.Stderr), maxRuntimeEvidenceBytes)
	returnedLines := countDockerOutputLines(docker.Stdout) + countDockerOutputLines(docker.Stderr)
	classification := domain.EvidenceCorrelatedSupport
	if returnedLines > 0 {
		// 有返回日志行时，typed docker.logs 是直接故障证据；空结果保持相关性证据。
		classification = domain.EvidenceDirectFault
	}
	coverageReason := docker.CoverageReason
	if stdoutTruncated || stderrTruncated {
		coverageReason = "byte_limit"
	}
	payload := map[string]interface{}{
		"container": map[string]interface{}{
			"name": docker.Container.Name, "id": docker.Container.ID,
			"image": docker.Container.Image, "state": docker.Container.State,
			"status": docker.Container.Status,
		},
		"stdout":             stdout,
		"stderr":             stderr,
		"truncated":          docker.Truncated || stdoutTruncated || stderrTruncated,
		"coverageLimited":    docker.CoverageLimited,
		"refinementRequired": docker.RefinementRequired || stdoutTruncated || stderrTruncated,
		"coverageReason":     coverageReason,
		"bytesRetrieved":     docker.BytesRetrieved,
		"windowLines":        docker.WindowLines,
		"returnedLines":      returnedLines,
		"filtered":           docker.FilteredLines,
		"query": map[string]interface{}{
			"since": docker.Query.Since.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
			"until": docker.Query.Until.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
			"tail":  docker.Query.Tail,
		},
	}
	if docker.Query.Pattern != "" {
		payload["query"].(map[string]interface{})["pattern"] = docker.Query.Pattern
	}
	fitRuntimePayload(payload)
	evidence := domain.StoredEvidence{
		Provider: "docker", EvidenceKind: domain.EvidenceKindRuntime,
		Classification:         classification,
		Outcome:                runtimeEvidenceOutcome(stdout, stderr),
		Available:              true,
		Primary:                true,
		TemporalCorrelation:    true,
		OperationalCorrelation: true,
		OccurredAt:             &docker.Query.Until,
	}
	return g.appendRuntimeEvidence(ctx, run, scope, phase, catalogVersion, ToolDockerLogs, evidence, payload)
}

// appendRuntimeEvidence 计算 canonical content hash 与 scope dedup key，填充
// ownership/provenance 后调用 RuntimeEvidenceWriter。dedup key 包含工具身份与
// content hash：同一 run 内相同输出的重试返回同一证据行，变化后的输出成为
// 新的不可变记录。
func (g *ToolGateway) appendRuntimeEvidence(
	ctx context.Context,
	run RunIdentity,
	scope domain.EvidenceScope,
	phase domain.RunState,
	catalogVersion string,
	tool string,
	evidence domain.StoredEvidence,
	payload map[string]interface{},
) (any, string, error) {
	canonical, err := json.Marshal(payload)
	if err != nil {
		return nil, "", runtimeEvidencePersistenceError("runtime evidence payload cannot be encoded")
	}
	if len(canonical) > maxRuntimeEvidenceBytes {
		return nil, "", runtimeEvidencePersistenceError("runtime evidence payload exceeds bound")
	}
	sum := sha256.Sum256(canonical)
	contentHash := hex.EncodeToString(sum[:])
	evidence.ProjectID = scope.ProjectID
	evidence.EnvironmentID = scope.EnvironmentID
	evidence.SourceID = scope.SourceID
	evidence.IncidentID = run.IncidentID
	evidence.RunID = run.RunID
	evidence.ContentHash = contentHash
	evidence.DeduplicationKey = tool + ":" + contentHash
	evidence.Payload = canonical
	evidence.Provenance = runtimeEvidenceProvenance(tool, phase, catalogVersion)
	evidence.ByteCount = int64(len(canonical))
	stored, err := g.runtimeWriter.AppendEvidence(ctx, evidence)
	if err != nil {
		return nil, "", runtimeEvidencePersistenceError("runtime evidence could not be persisted")
	}
	if strings.TrimSpace(stored.EvidenceID) == "" {
		return nil, "", runtimeEvidencePersistenceError("runtime evidence was persisted without an id")
	}
	// canonical payload 必须与持久化 payload 一致；使用持久化后的字节作为唯一真相。
	return json.RawMessage(canonical), stored.EvidenceID, nil
}

// runtimeEvidenceProvenance 记录逻辑工具、phase、catalog version 与 projection
// version；不含凭据、主机地址或连接 authority。
func runtimeEvidenceProvenance(tool string, phase domain.RunState, catalogVersion string) json.RawMessage {
	provenance := map[string]interface{}{
		"tool":              tool,
		"phase":             string(phase),
		"projectionVersion": 1,
	}
	if strings.TrimSpace(catalogVersion) != "" {
		provenance["catalogVersion"] = catalogVersion
	}
	encoded, _ := json.Marshal(provenance)
	return encoded
}

// runtimeEvidenceOutcome 在 sanitized 输出为空时返回 empty，否则 success。
func runtimeEvidenceOutcome(stdout, stderr string) string {
	if strings.TrimSpace(stdout) == "" && strings.TrimSpace(stderr) == "" {
		return "empty"
	}
	return "success"
}

var runtimeCredentialAssignmentPattern = regexp.MustCompile(`(?i)(["']?[A-Za-z0-9_.-]*(?:password|passwd|pwd|token|secret|authorization|api[-_]?key|access[-_]?key|private[-_]?key|credential)[A-Za-z0-9_.-]*["']?\s*[:=]\s*)(?:\[redacted\]|"[^"]*"|'[^']*'|[^\s,;}\]]+)`)

// sanitizeRuntimeOutput 应用与模型边界相同的结构化/文本凭据脱敏，并额外移除
// 复合 credential key（如 AWS_SECRET_ACCESS_KEY）、PEM private-key 块与
// fixthe-ssh* 临时 key 路径。persistence 与 model context 都消费同一
// projection，原始 adapter stdout/stderr 不得越过此边界。
func sanitizeRuntimeOutput(value string) string {
	value = redactConversationText(value)
	value = runtimeCredentialAssignmentPattern.ReplaceAllString(value, "${1}[redacted]")
	return conversationTempKeyPattern.ReplaceAllString(value, "[redacted]")
}

// boundRuntimeText 把 runtime 文本裁剪到预算内，返回裁剪后的文本与是否截断。
// 裁剪停在 rune 边界，避免把 UTF-8 序列切成非法字节。
func boundRuntimeText(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut], true
}

// fitRuntimePayload 在 JSON 编码后的 canonical payload 仍超出
// maxRuntimeEvidenceBytes 时，按 rune 边界迭代缩小较大的 stdout/stderr 原始
// 文本并置 truncated，直到落在 64KiB 上限内。有界大输出必须返回截断前缀而非
// 整体持久化失败：JSON 转义（换行/控制字符）会让编码后体积超过原始字节数，
// 直接报错会使 ssh.inspect 在输出接近上限时永远不可用，等于削弱既有 64KiB
// 输出上限的可用语义（PRD R6）。迭代次数有界，最坏情况下两条流都会指数缩小。
func fitRuntimePayload(payload map[string]interface{}) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	for iter := 0; iter < 12 && len(encoded) > maxRuntimeEvidenceBytes; iter++ {
		stdout, _ := payload["stdout"].(string)
		stderr, _ := payload["stderr"].(string)
		if len(stdout) >= len(stderr) {
			stdout = boundRuntimeHalf(stdout)
		} else {
			stderr = boundRuntimeHalf(stderr)
		}
		payload["stdout"] = stdout
		payload["stderr"] = stderr
		payload["truncated"] = true
		if _, dockerPayload := payload["refinementRequired"]; dockerPayload {
			payload["coverageLimited"] = true
			payload["refinementRequired"] = true
			payload["coverageReason"] = "byte_limit"
		}
		encoded, err = json.Marshal(payload)
		if err != nil {
			return
		}
	}
}

// boundRuntimeHalf 把文本缩小到原始字节数的一半，停在 rune 边界。
func boundRuntimeHalf(value string) string {
	if value == "" {
		return value
	}
	cut := len(value) / 2
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}
