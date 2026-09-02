package application

import (
	"context"
	"fmt"
	"strings"

	"fixthe/backend/internal/modules/remediation/domain"
)

const (
	// ToolWorkspaceStatus 读取 run-owned 工作区的 bounded 状态。
	ToolWorkspaceStatus = "workspace.status"
	// ToolWorkspaceReadFile 读取工作区内的 bounded 文件。
	ToolWorkspaceReadFile = "workspace.read_file"
	// ToolWorkspaceApplyPatch 将小范围 patch 应用到工作区。
	ToolWorkspaceApplyPatch = "workspace.apply_patch"
	// ToolWorkspaceRunValidation 执行一个 approved command ID。
	ToolWorkspaceRunValidation = "workspace.run_validation"
)

const maxLifecyclePatchBytes = 64 << 10

// LifecycleToolGateway 将 patch/validation 工具的 schema、路径、tree CAS、
// approved command 和外部端口校验集中在应用边界。它不接收 shell 字符串或凭据。
type LifecycleToolGateway struct {
	workspace  domain.WorkspacePort
	validation domain.ValidationPort
}

// NewLifecycleToolGateway 创建 lifecycle 工具网关。
func NewLifecycleToolGateway(workspace domain.WorkspacePort, validation domain.ValidationPort) *LifecycleToolGateway {
	return &LifecycleToolGateway{workspace: workspace, validation: validation}
}

// DefinitionsForPhase 返回当前 lifecycle phase 的 bounded workspace schemas。
func (g *LifecycleToolGateway) DefinitionsForPhase(phase domain.RunState) []domain.ToolDefinition {
	names := []string{}
	switch phase {
	case domain.RunStatePatching:
		names = []string{ToolWorkspaceStatus, ToolWorkspaceReadFile, ToolWorkspaceApplyPatch}
	case domain.RunStateValidating:
		names = []string{ToolWorkspaceStatus, ToolWorkspaceReadFile, ToolWorkspaceRunValidation}
	}
	defs := make([]domain.ToolDefinition, 0, len(names))
	for _, name := range names {
		defs = append(defs, domain.ToolDefinition{Name: name, Description: lifecycleToolDescription(name), Parameters: lifecycleToolSchema(name)})
	}
	return defs
}

// Execute 执行一个已广告的 workspace/validation 请求。
// idempotencyKey 与 expectedTreeHash 由 coordinator 生成，模型不能覆盖。
func (g *LifecycleToolGateway) Execute(
	ctx context.Context,
	phase domain.RunState,
	workspace domain.WorkspaceIdentity,
	commandVersions map[string]int64,
	idempotencyKey string,
	tool string,
	params map[string]interface{},
) (ToolResult, error) {
	if !lifecycleToolAdvertised(tool, phase) {
		return ToolResult{}, &ToolRejection{Code: RejectOutOfPhase, Tool: tool, Message: "lifecycle tool is unavailable in the current phase"}
	}
	if err := workspace.Validate(); err != nil {
		return ToolResult{}, lifecycleRuntimeError("workspace_invalid", false, err)
	}
	if err := validateLifecycleToolParams(tool, params); err != nil {
		return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: tool, Message: err.Error()}
	}

	switch tool {
	case ToolWorkspaceStatus:
		if g.workspace == nil {
			return ToolResult{}, lifecycleRuntimeError("workspace_unavailable", true, nil)
		}
		status, err := g.workspace.Status(ctx, workspace)
		if err != nil {
			return ToolResult{}, err
		}
		if err := status.Identity.Validate(); err != nil {
			return ToolResult{}, lifecycleRuntimeError("workspace_invalid_response", false, err)
		}
		return ToolResult{Tool: tool, Summary: fmt.Sprintf("workspace status clean=%t files=%d", status.Clean, len(status.ChangedFiles)), BytesRetrieved: status.Bytes, Payload: status}, nil

	case ToolWorkspaceReadFile:
		if g.workspace == nil {
			return ToolResult{}, lifecycleRuntimeError("workspace_unavailable", true, nil)
		}
		pathValue, _ := params["path"].(string)
		maxBytes := int64(1 << 20)
		if value, ok := numericArg(params["maxBytes"]); ok {
			maxBytes = value
		}
		file, err := g.workspace.ReadFile(ctx, workspace, pathValue, maxBytes)
		if err != nil {
			return ToolResult{}, err
		}
		if err := validateWorkspaceFile(file); err != nil {
			return ToolResult{}, lifecycleRuntimeError("workspace_invalid_response", false, err)
		}
		return ToolResult{Tool: tool, Summary: fmt.Sprintf("%s (%d bytes, truncated=%t)", file.Path, len(file.Content), file.Truncated), BytesRetrieved: int64(len(file.Content)), Payload: file}, nil

	case ToolWorkspaceApplyPatch:
		if g.workspace == nil {
			return ToolResult{}, lifecycleRuntimeError("workspace_unavailable", true, nil)
		}
		patch, _ := params["patch"].(string)
		request := domain.PatchRequest{
			WorkspaceID: workspace.WorkspaceID, Patch: patch, ExpectedTreeHash: workspace.CurrentTreeHash, IdempotencyKey: idempotencyKey,
		}
		if err := request.Validate(); err != nil {
			return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: tool, Message: err.Error()}
		}
		result, err := g.workspace.ApplyPatch(ctx, workspace, request)
		if err != nil {
			return ToolResult{}, err
		}
		if err := result.Validate(); err != nil {
			return ToolResult{}, lifecycleRuntimeError("patch_invalid_response", false, err)
		}
		return ToolResult{Tool: tool, Summary: result.Summary, BytesRetrieved: result.BytesRetrieved, Payload: result}, nil

	case ToolWorkspaceRunValidation:
		if g.validation == nil {
			return ToolResult{}, lifecycleRuntimeError("validation_unavailable", true, nil)
		}
		commandID, _ := params["commandId"].(string)
		version, ok := commandVersions[commandID]
		if !ok || version < 1 {
			return ToolResult{}, &ToolRejection{Code: RejectUnavailable, Tool: tool, Message: "validation command is not approved for this run"}
		}
		request := domain.ValidationRequest{
			RunID: workspace.RunID, WorkspaceID: workspace.WorkspaceID, CommandID: commandID,
			CommandVersion: version, ExpectedTreeHash: workspace.CurrentTreeHash, IdempotencyKey: idempotencyKey,
		}
		if err := request.Validate(); err != nil {
			return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: tool, Message: err.Error()}
		}
		result, err := g.validation.Run(ctx, request)
		if err != nil {
			return ToolResult{}, err
		}
		if err := result.Validate(); err != nil {
			return ToolResult{}, lifecycleRuntimeError("validation_invalid_response", false, err)
		}
		return ToolResult{Tool: tool, Summary: result.Summary, BytesRetrieved: result.BytesRetrieved, Payload: result}, nil
	default:
		return ToolResult{}, &ToolRejection{Code: RejectUnavailable, Tool: tool, Message: "lifecycle tool is not registered"}
	}
}

func lifecycleToolAdvertised(tool string, phase domain.RunState) bool {
	switch phase {
	case domain.RunStatePatching:
		return tool == ToolWorkspaceStatus || tool == ToolWorkspaceReadFile || tool == ToolWorkspaceApplyPatch
	case domain.RunStateValidating:
		return tool == ToolWorkspaceStatus || tool == ToolWorkspaceReadFile || tool == ToolWorkspaceRunValidation
	default:
		return false
	}
}

func lifecycleToolSchema(tool string) map[string]interface{} {
	object := func(properties map[string]interface{}, required ...string) map[string]interface{} {
		if properties == nil {
			properties = map[string]interface{}{}
		}
		schema := map[string]interface{}{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	switch tool {
	case ToolWorkspaceStatus:
		return object(nil)
	case ToolWorkspaceReadFile:
		return object(map[string]interface{}{
			"path":     map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 512},
			"maxBytes": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 1 << 20},
		}, "path")
	case ToolWorkspaceApplyPatch:
		return object(map[string]interface{}{
			"patch": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": maxLifecyclePatchBytes},
		}, "patch")
	case ToolWorkspaceRunValidation:
		return object(map[string]interface{}{
			"commandId": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 128},
		}, "commandId")
	default:
		return object(nil)
	}
}

func lifecycleToolDescription(tool string) string {
	switch tool {
	case ToolWorkspaceStatus:
		return "Read bounded status for the isolated run workspace."
	case ToolWorkspaceReadFile:
		return "Read a bounded repository-relative file from the isolated workspace."
	case ToolWorkspaceApplyPatch:
		return "Apply one bounded patch against the current workspace tree precondition."
	case ToolWorkspaceRunValidation:
		return "Run one administrator-approved validation command ID; shell text is not accepted."
	default:
		return "Bounded remediation lifecycle tool."
	}
}

func validateLifecycleToolParams(tool string, params map[string]interface{}) error {
	if params == nil {
		return fmt.Errorf("parameters must be an object")
	}
	schema := lifecycleToolSchema(tool)
	properties, _ := schema["properties"].(map[string]interface{})
	for key := range params {
		if _, ok := properties[key]; !ok {
			return fmt.Errorf("unknown parameter %q", key)
		}
	}
	if required, _ := schema["required"].([]string); len(required) > 0 {
		for _, key := range required {
			if _, ok := params[key]; !ok {
				return fmt.Errorf("%s is required", key)
			}
		}
	}
	for key, value := range params {
		switch key {
		case "path", "patch", "commandId":
			if _, ok := value.(string); !ok || strings.TrimSpace(value.(string)) == "" {
				return fmt.Errorf("%s must be a non-empty string", key)
			}
		case "maxBytes":
			n, ok := numericArg(value)
			if !ok || !isIntegerArgument(value) || n < 1 || n > 1<<20 {
				return fmt.Errorf("maxBytes is outside its bound")
			}
		}
	}
	if patch, ok := params["patch"].(string); ok && len(patch) > maxLifecyclePatchBytes {
		return fmt.Errorf("patch exceeds %d bytes", maxLifecyclePatchBytes)
	}
	if pathValue, ok := params["path"].(string); ok {
		if err := validateRepoPath(pathValue); err != nil || strings.TrimSpace(pathValue) == "" {
			return fmt.Errorf("path must be repository-relative")
		}
	}
	return nil
}

func validateWorkspaceFile(file domain.WorkspaceFile) error {
	if err := validateRepoPath(file.Path); err != nil || strings.TrimSpace(file.Path) == "" {
		return fmt.Errorf("workspace file path is invalid")
	}
	if len(file.Content) > 1<<20 {
		return fmt.Errorf("workspace file exceeds 1 MiB")
	}
	if file.Reason != "" && len(file.Reason) > 128 {
		return fmt.Errorf("workspace file reason exceeds bounds")
	}
	return nil
}

func lifecycleRuntimeError(code string, retryable bool, cause error) error {
	return &domain.LifecycleRuntimeError{Code: code, Retryable: retryable, Cause: cause}
}
