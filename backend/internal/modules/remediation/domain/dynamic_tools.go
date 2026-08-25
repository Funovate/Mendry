package domain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// SourceCapabilitySnapshot is the credential-free source state captured for a
// remediation run. Declared capabilities describe what the project enabled;
// Supported describes what this binary can execute.
type SourceCapabilitySnapshot struct {
	ProjectID         string
	SourceID          string
	Kind              string
	Enabled           bool
	Declared          []string
	Version           int64
	Supported         bool
	UnavailableReason string
	// SSHHost / SSHUser / SSHProjectFolder / SSHLogPath 是给模型的 inspect 提示，
	// 不含凭据；logPath 只表示日志常出现的目录，不是要自动 tail 的文件。
	SSHHost           string
	SSHUser           string
	SSHProjectFolder  string
	SSHLogPath        string
	SSHDeploymentKind string
	SSHContainerName  string
}

// Allows reports whether the source declaration contains a capability.
func (s SourceCapabilitySnapshot) Allows(capability string) bool {
	for _, value := range s.Declared {
		if value == capability {
			return true
		}
	}
	return false
}

// ToolPolicyEntry is an administrator-approved MCP tool. The original server
// tool name is used for policy lookup; the model sees only the namespaced name
// produced by the trusted runtime.
type ToolPolicyEntry struct {
	ToolName    string
	Phases      []RunState
	EffectClass string
}

// ToolPolicySnapshot is immutable for the lifetime of a remediation run.
// Missing policy is represented by an empty snapshot and exposes no dynamic
// MCP tools.
type ToolPolicySnapshot struct {
	ProjectID string
	SourceID  string
	Version   int64
	Hash      string
	Entries   []ToolPolicyEntry
}

type policyEntryJSON struct {
	ToolName    string     `json:"toolName"`
	Phases      []RunState `json:"phases"`
	EffectClass string     `json:"effect"`
}

// ParseToolPolicy validates the persisted, project-scoped MCP allowlist. The
// server's annotations never enter this contract: only an administrator-owned
// read grant can make a discovered tool executable.
func ParseToolPolicy(projectID, sourceID string, version int64, storedHash string, raw json.RawMessage) (ToolPolicySnapshot, error) {
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(sourceID) == "" || version <= 0 {
		return ToolPolicySnapshot{}, fmt.Errorf("remediation tool policy identity or version is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var encoded []policyEntryJSON
	if err := decoder.Decode(&encoded); err != nil {
		return ToolPolicySnapshot{}, fmt.Errorf("decode remediation tool policy: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return ToolPolicySnapshot{}, fmt.Errorf("remediation tool policy has trailing data")
		}
		return ToolPolicySnapshot{}, fmt.Errorf("decode remediation tool policy trailing data: %w", err)
	}
	if len(encoded) > 128 {
		return ToolPolicySnapshot{}, fmt.Errorf("remediation tool policy has too many entries")
	}
	seen := make(map[string]struct{}, len(encoded))
	entries := make([]ToolPolicyEntry, 0, len(encoded))
	for index := range encoded {
		item := &encoded[index]
		item.ToolName = strings.TrimSpace(item.ToolName)
		if err := validateToolPolicyEntry(item.ToolName, item.Phases, item.EffectClass); err != nil {
			return ToolPolicySnapshot{}, err
		}
		phaseSet := make(map[RunState]struct{}, len(item.Phases))
		for _, phase := range item.Phases {
			if _, exists := phaseSet[phase]; exists {
				return ToolPolicySnapshot{}, fmt.Errorf("remediation tool policy repeats a phase")
			}
			phaseSet[phase] = struct{}{}
		}
		key := item.ToolName + "\x00" + strings.Join(runStateStrings(item.Phases), "\x00")
		if _, exists := seen[key]; exists {
			return ToolPolicySnapshot{}, fmt.Errorf("remediation tool policy repeats an entry")
		}
		seen[key] = struct{}{}
		entries = append(entries, ToolPolicyEntry{
			ToolName: item.ToolName, Phases: append([]RunState(nil), item.Phases...), EffectClass: item.EffectClass,
		})
	}
	computedHash := toolPolicyHash(entries)
	if storedHash != "" && storedHash != computedHash {
		return ToolPolicySnapshot{}, fmt.Errorf("remediation tool policy hash does not match entries")
	}
	return ToolPolicySnapshot{
		ProjectID: projectID, SourceID: sourceID, Version: version,
		Hash: computedHash, Entries: entries,
	}, nil
}

// ValidateFor 校验 resolver 返回的 policy 是否仍绑定到当前 project/source。
// 缺失 policy 用 version=0、空 hash、空 entries 表示；任何部分存在的快照都必须
// 通过完整的 effect、phase、重复项和 hash 校验，避免 fake 或损坏行扩大权限。
func (p ToolPolicySnapshot) ValidateFor(projectID, sourceID string) error {
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(sourceID) == "" ||
		p.ProjectID != projectID || p.SourceID != sourceID {
		return fmt.Errorf("remediation tool policy ownership is invalid")
	}
	if p.Version == 0 && p.Hash == "" && len(p.Entries) == 0 {
		return nil
	}
	if p.Version <= 0 || p.Hash == "" || len(p.Entries) > 128 {
		return fmt.Errorf("remediation tool policy version or hash is invalid")
	}
	seen := make(map[string]struct{}, len(p.Entries))
	for _, entry := range p.Entries {
		if err := validateToolPolicyEntry(entry.ToolName, entry.Phases, entry.EffectClass); err != nil {
			return err
		}
		phaseNames := runStateStrings(entry.Phases)
		key := entry.ToolName + "\x00" + strings.Join(phaseNames, "\x00")
		if _, exists := seen[key]; exists {
			return fmt.Errorf("remediation tool policy repeats an entry")
		}
		seen[key] = struct{}{}
	}
	if p.Hash != toolPolicyHash(p.Entries) {
		return fmt.Errorf("remediation tool policy hash does not match entries")
	}
	return nil
}

func validateToolPolicyEntry(toolName string, phases []RunState, effectClass string) error {
	if strings.TrimSpace(toolName) == "" || len(toolName) > 128 || effectClass != "read" || len(phases) == 0 || len(phases) > 4 {
		return fmt.Errorf("remediation tool policy entry is invalid")
	}
	for _, phase := range phases {
		if !policyPhaseAllowed(phase) {
			return fmt.Errorf("remediation tool policy phase is invalid")
		}
	}
	return nil
}

func toolPolicyHash(entries []ToolPolicyEntry) string {
	encoded := make([]policyEntryJSON, 0, len(entries))
	for _, entry := range entries {
		encoded = append(encoded, policyEntryJSON{
			ToolName: entry.ToolName, Phases: append([]RunState(nil), entry.Phases...), EffectClass: entry.EffectClass,
		})
	}
	canonical, _ := json.Marshal(encoded)
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

func policyPhaseAllowed(phase RunState) bool {
	switch phase {
	case RunStateDiagnosing, RunStateCollectingMoreContext, RunStatePlanning:
		return true
	default:
		return false
	}
}

func runStateStrings(phases []RunState) []string {
	values := make([]string, 0, len(phases))
	for _, phase := range phases {
		values = append(values, string(phase))
	}
	return values
}

// DynamicToolScope contains only opaque run/source identities. It deliberately
// does not carry credentials, endpoints, or a client/session handle.
type DynamicToolScope struct {
	RunID         string
	ProjectID     string
	SourceID      string
	SourceVersion int64
	PolicyVersion int64
}

// DynamicToolDefinition is the trusted catalog representation returned by an
// MCP server. Server annotations are retained as descriptive data only; the
// policy snapshot remains the authority to expose or call the tool.
type DynamicToolDefinition struct {
	SourceID    string
	ServerID    string
	Name        string
	Description string
	InputSchema map[string]interface{}
	Annotations map[string]interface{}
}

// DynamicToolCatalog is a bounded discovery result. The application layer
// namespaces names and filters policy before handing definitions to a model.
type DynamicToolCatalog struct {
	SourceID  string
	ServerID  string
	Version   string
	Hash      string
	Tools     []DynamicToolDefinition
	Truncated bool
}

// DynamicToolCall maps a model-visible route back to the original MCP name.
// Only the trusted gateway may construct this value.
type DynamicToolCall struct {
	SourceID  string
	ServerID  string
	Name      string
	Arguments map[string]interface{}
}

// DynamicToolResult is already bounded by the trusted runtime before it
// crosses into the Tool Gateway.
type DynamicToolResult struct {
	Payload        any
	BytesRetrieved int64
	EvidenceIDs    []string
	Truncated      bool
}

// ToolRuntimeError is the safe error contract between a connector runtime and
// the gateway. Message must be suitable for operators and the model; raw
// stderr, response bodies, headers, and credentials never belong here.
type ToolRuntimeError struct {
	Code      string
	Retryable bool
	Message   string
}

func (e *ToolRuntimeError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Message
}

// DynamicToolRuntimePort owns MCP initialization, discovery, calls, transport
// credentials, and per-run session cleanup.
type DynamicToolRuntimePort interface {
	Discover(context.Context, DynamicToolScope) (DynamicToolCatalog, error)
	Call(context.Context, DynamicToolScope, DynamicToolCall) (DynamicToolResult, error)
	CloseRun(context.Context, string) error
}

// ToolPolicyResolver loads the project/source policy snapshot without exposing
// persistence or administrative credentials to the remediation agent.
type ToolPolicyResolver interface {
	ResolveToolPolicy(context.Context, string, string) (ToolPolicySnapshot, error)
}

// SourceCapabilityResolver loads durable source metadata without performing a
// connector read. It is optional for legacy in-memory coordinator wiring.
type SourceCapabilityResolver interface {
	ResolveSourceCapability(context.Context, string, string) (SourceCapabilitySnapshot, error)
}
