package domain

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// LifecycleSchemaVersionV1 是 Phase 3 lifecycle effect 与外部结果的 schema 标识。
const LifecycleSchemaVersionV1 = "v1"

const (
	LifecycleEffectWorkspace    LifecycleEffectKind = "workspace"
	LifecycleEffectPatch        LifecycleEffectKind = "patch"
	LifecycleEffectValidation   LifecycleEffectKind = "validation"
	LifecycleEffectPublication  LifecycleEffectKind = "publication"
	LifecycleEffectWorkspaceEnd LifecycleEffectKind = "workspace_cleanup"
)

// LifecycleEffectKind 标识需要跨进程恢复的外部效果类别。
type LifecycleEffectKind string

// LifecycleRuntimeError 是 workspace/validation/publication adapter 返回的安全失败分类。
type LifecycleRuntimeError struct {
	Code      string
	Retryable bool
	Cause     error
}

// Error 返回不包含远端输出、命令正文或 credential 的稳定分类。
func (e *LifecycleRuntimeError) Error() string {
	if e == nil || e.Code == "" {
		return "lifecycle runtime failure"
	}
	return e.Code
}

// Unwrap 保留 typed cause 的判定能力，但调用方只应持久化 Code。
func (e *LifecycleRuntimeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// IsKnown 报告 effect kind 是否属于当前 lifecycle 合同。
func (k LifecycleEffectKind) IsKnown() bool {
	switch k {
	case LifecycleEffectWorkspace, LifecycleEffectPatch, LifecycleEffectValidation,
		LifecycleEffectPublication, LifecycleEffectWorkspaceEnd:
		return true
	default:
		return false
	}
}

// LifecycleEffectState 是外部效果在 durable store 中的最新状态。
type LifecycleEffectState string

const (
	LifecycleEffectStarted     LifecycleEffectState = "started"
	LifecycleEffectSucceeded   LifecycleEffectState = "succeeded"
	LifecycleEffectRecoverable LifecycleEffectState = "recoverable"
	LifecycleEffectFailed      LifecycleEffectState = "failed"
)

// IsKnown 报告 effect 状态是否可安全持久化。
func (s LifecycleEffectState) IsKnown() bool {
	switch s {
	case LifecycleEffectStarted, LifecycleEffectSucceeded, LifecycleEffectRecoverable, LifecycleEffectFailed:
		return true
	default:
		return false
	}
}

// PlanPolicyInput 是 planCandidates 进入 patching 前的服务端校验输入。
type PlanPolicyInput struct {
	RunID          string
	BaselineCommit string
	Candidates     []RepairPlanCandidate
	RecommendedID  string
}

// PlanPolicyDecision 是 plan policy 对模型候选的结构化反馈。
// AcceptedPlanIDs 只列出服务端实际允许的候选，模型推荐仍需再次与该列表匹配。
type PlanPolicyDecision struct {
	Accepted              bool
	Severity              RecoverySeverity
	ReasonCode            string
	Message               string
	AcceptedPlanIDs       []string
	RejectedPlanIDs       []string
	RequiredValidationIDs []string
	SelectedPlanID        string
}

// PlanPolicyEvaluator 是项目策略对候选计划的只读校验端口。
type PlanPolicyEvaluator interface {
	EvaluatePlan(context.Context, PlanPolicyInput) (PlanPolicyDecision, error)
}

// WorkspaceRequest 是创建或恢复一次 run-owned 隔离工作区的输入。
type WorkspaceRequest struct {
	RunID          string
	ProjectID      string
	BaselineCommit string
	IdempotencyKey string
}

// WorkspaceIdentity 是工作区和 immutable deployed baseline 的绑定身份。
type WorkspaceIdentity struct {
	WorkspaceID     string
	RunID           string
	BaselineCommit  string
	BaseTreeHash    string
	CurrentTreeHash string
	Version         int64
}

// WorkspaceStatus 是有界的工作区状态投影，不包含完整仓库或凭据。
type WorkspaceStatus struct {
	Identity     WorkspaceIdentity
	Clean        bool
	ChangedFiles []string
	Bytes        int64
}

// WorkspaceFile 是从隔离工作区读取的有界文件内容。
type WorkspaceFile struct {
	Path      string
	Content   []byte
	Truncated bool
	Reason    string
}

// PatchRequest 是一次小范围、幂等的工作区 patch 操作。
type PatchRequest struct {
	WorkspaceID      string
	Patch            string
	ExpectedTreeHash string
	IdempotencyKey   string
}

// PatchResult 是 patch adapter 返回的有界结果和内容寻址身份。
type PatchResult struct {
	WorkspaceID    string
	Applied        bool
	AlreadyApplied bool
	ArtifactRef    string
	ContentHash    string
	ResultTreeHash string
	ChangedFiles   []string
	BytesRetrieved int64
	Summary        string
}

// WorkspacePort 负责 run-owned 隔离工作区的创建、读取、patch 和销毁。
// 实现不得把 Git 写凭据、宿主机路径或 container-engine authority 传给调用方。
type WorkspacePort interface {
	Ensure(context.Context, WorkspaceRequest) (WorkspaceIdentity, error)
	Status(context.Context, WorkspaceIdentity) (WorkspaceStatus, error)
	ReadFile(context.Context, WorkspaceIdentity, string, int64) (WorkspaceFile, error)
	ApplyPatch(context.Context, WorkspaceIdentity, PatchRequest) (PatchResult, error)
	Destroy(context.Context, WorkspaceIdentity) error
}

// ValidationRequest 是只能引用 approved command ID 的验证请求；不接受 shell 字符串。
type ValidationRequest struct {
	RunID            string
	WorkspaceID      string
	CommandID        string
	CommandVersion   int64
	ExpectedTreeHash string
	IdempotencyKey   string
}

// ValidationResult 是经过 runner 脱敏、裁剪后的验证结果。
type ValidationResult struct {
	RunID             string
	WorkspaceID       string
	CommandID         string
	CommandVersion    int64
	Passed            bool
	ExitCode          int
	OutputArtifactRef string
	OutputHash        string
	OutputExcerpt     string
	BytesRetrieved    int64
	Summary           string
	StartedAt         time.Time
	CompletedAt       time.Time
}

// Validate 校验 validation 请求只引用有界身份和 approved command version。
func (r ValidationRequest) Validate() error {
	if err := lifecycleRequiredText("validation run id", r.RunID, 128); err != nil {
		return err
	}
	if err := lifecycleRequiredText("validation workspace id", r.WorkspaceID, 256); err != nil {
		return err
	}
	if err := lifecycleRequiredText("validation command id", r.CommandID, 128); err != nil {
		return err
	}
	if r.CommandVersion < 1 {
		return fmt.Errorf("validation command version must be positive")
	}
	if err := lifecycleRequiredText("validation expected tree hash", r.ExpectedTreeHash, 128); err != nil {
		return err
	}
	return validateIdempotencyKey(r.IdempotencyKey)
}

// ValidationPort 执行项目策略已批准且由 run 快照的 command ID。
type ValidationPort interface {
	Run(context.Context, ValidationRequest) (ValidationResult, error)
}

// PublicationRequest 是从已验证 artifact 向受保护 SCM 发布 change 的输入。
// 请求只携带内容寻址 artifact 和安全元数据，不携带 Git/SCM credential。
type PublicationRequest struct {
	RunID            string
	ProjectID        string
	BaselineCommit   string
	TargetBranch     string
	BranchRef        string
	CommitMessage    string
	PatchArtifactRef string
	PatchContentHash string
	ExpectedTreeHash string
	IdempotencyKey   string
}

// PublicationResult 是发布后的 branch/commit/change-request 安全投影。
type PublicationResult struct {
	BranchRef           string
	CommitHash          string
	DraftChangeRef      string
	CompareURL          string
	BaselineCommit      string
	TargetBranch        string
	HumanReviewRequired bool
	AlreadyPublished    bool
	Summary             string
}

// PublicationPort 创建或复用 branch、commit、push 和 draft change request；
// 接口刻意没有 merge、deploy 或 production mutation 方法。
type PublicationPort interface {
	Publish(context.Context, PublicationRequest) (PublicationResult, error)
}

// LifecycleEffect 是一次外部效果的 latest durable projection。
// 该记录不保存 raw patch、命令输出、model turn 或 credential；这些内容只能通过
// 已批准的 content-addressed artifact reference 重新取得。
type LifecycleEffect struct {
	EffectID         string
	RunID            string
	Kind             LifecycleEffectKind
	IdempotencyKey   string
	State            LifecycleEffectState
	Attempt          int
	BaselineCommit   string
	WorkspaceID      string
	BaseTreeHash     string
	ResultTreeHash   string
	ArtifactRef      string
	ContentHash      string
	CommandID        string
	CommandVersion   int64
	ValidationKnown  bool
	ValidationPassed bool
	BranchRef        string
	TargetBranch     string
	CommitHash       string
	DraftChangeRef   string
	CompareURL       string
	ErrorCode        string
	Summary          string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// LifecycleStore 持久化 workspace、patch、validation、publication 的幂等效果。
// Upsert 必须以 run + kind + idempotencyKey 为唯一边界，并保持已成功效果不可被
// 后续失败状态覆盖。
type LifecycleStore interface {
	GetLifecycleEffect(context.Context, string, LifecycleEffectKind, string) (LifecycleEffect, error)
	UpsertLifecycleEffect(context.Context, LifecycleEffect) (LifecycleEffect, error)
	ListLifecycleEffects(context.Context, string) ([]LifecycleEffect, error)
}

// ErrLifecycleEffectNotFound 表示当前 run 尚无指定幂等效果记录。
var ErrLifecycleEffectNotFound = fmt.Errorf("remediation lifecycle effect not found")

// Validate 校验 plan policy 的服务端反馈边界。
func (d PlanPolicyDecision) Validate() error {
	if d.Severity != "" && !d.Severity.IsKnown() {
		return fmt.Errorf("plan policy severity is invalid")
	}
	if err := lifecycleOptionalText("plan policy reason code", d.ReasonCode, 128); err != nil {
		return err
	}
	if err := lifecycleOptionalText("plan policy message", d.Message, 1024); err != nil {
		return err
	}
	if len(d.AcceptedPlanIDs) > 64 || len(d.RejectedPlanIDs) > 64 || len(d.RequiredValidationIDs) > 32 {
		return fmt.Errorf("plan policy lists exceed bounds")
	}
	for _, value := range append(append(append([]string{}, d.AcceptedPlanIDs...), d.RejectedPlanIDs...), d.RequiredValidationIDs...) {
		if err := lifecycleRequiredText("plan policy identifier", value, 128); err != nil {
			return err
		}
	}
	if d.SelectedPlanID != "" {
		if err := lifecycleRequiredText("selected plan id", d.SelectedPlanID, 128); err != nil {
			return err
		}
	}
	if d.Accepted && d.SelectedPlanID == "" {
		return fmt.Errorf("accepted plan policy decision requires selected plan")
	}
	if d.Accepted && !containsLifecycleString(d.AcceptedPlanIDs, d.SelectedPlanID) {
		return fmt.Errorf("selected plan is not in accepted plan IDs")
	}
	return nil
}

// Validate 校验隔离工作区请求，并确保 baseline/key 不含控制字符。
func (r WorkspaceRequest) Validate() error {
	if err := lifecycleRequiredText("workspace run id", r.RunID, 128); err != nil {
		return err
	}
	if err := lifecycleRequiredText("workspace project id", r.ProjectID, 128); err != nil {
		return err
	}
	if err := lifecycleRequiredText("workspace baseline commit", r.BaselineCommit, 256); err != nil {
		return err
	}
	return validateIdempotencyKey(r.IdempotencyKey)
}

// Validate 校验工作区身份，防止跨 run 或跨 baseline 复用。
func (i WorkspaceIdentity) Validate() error {
	if err := lifecycleRequiredText("workspace id", i.WorkspaceID, 256); err != nil {
		return err
	}
	if err := lifecycleRequiredText("workspace run id", i.RunID, 128); err != nil {
		return err
	}
	if err := lifecycleRequiredText("workspace baseline commit", i.BaselineCommit, 256); err != nil {
		return err
	}
	if err := lifecycleRequiredText("workspace base tree hash", i.BaseTreeHash, 128); err != nil {
		return err
	}
	if err := lifecycleRequiredText("workspace current tree hash", i.CurrentTreeHash, 128); err != nil {
		return err
	}
	if i.Version < 1 {
		return fmt.Errorf("workspace version must be positive")
	}
	return nil
}

// Validate 校验 patch 请求的大小、baseline CAS 和幂等边界。
func (r PatchRequest) Validate() error {
	if err := lifecycleRequiredText("patch workspace id", r.WorkspaceID, 256); err != nil {
		return err
	}
	if strings.TrimSpace(r.Patch) == "" {
		return fmt.Errorf("patch is required")
	}
	if len(r.Patch) > 64<<10 {
		return fmt.Errorf("patch exceeds 65536 bytes")
	}
	if err := lifecycleRequiredText("patch expected tree hash", r.ExpectedTreeHash, 128); err != nil {
		return err
	}
	return validateIdempotencyKey(r.IdempotencyKey)
}

// Validate 校验 patch 结果是否具备内容寻址身份。
func (r PatchResult) Validate() error {
	if err := lifecycleRequiredText("patch workspace id", r.WorkspaceID, 256); err != nil {
		return err
	}
	if !r.Applied && !r.AlreadyApplied {
		return fmt.Errorf("patch result must report applied or already applied")
	}
	if err := lifecycleRequiredText("patch artifact ref", r.ArtifactRef, 512); err != nil {
		return err
	}
	if err := lifecycleRequiredText("patch content hash", r.ContentHash, 128); err != nil {
		return err
	}
	if err := validateContentHash("patch content hash", r.ContentHash); err != nil {
		return err
	}
	if err := lifecycleRequiredText("patch result tree hash", r.ResultTreeHash, 128); err != nil {
		return err
	}
	if r.BytesRetrieved < 0 || r.BytesRetrieved > 64<<10 {
		return fmt.Errorf("patch result bytes are out of bounds")
	}
	if len(r.ChangedFiles) > 128 {
		return fmt.Errorf("patch changed files exceed bounds")
	}
	for _, path := range r.ChangedFiles {
		if err := validateWorkspacePath(path); err != nil {
			return err
		}
	}
	return lifecycleOptionalText("patch summary", r.Summary, 1024)
}

// Validate 校验 validation result 的 approved command 与 bounded output 身份。
func (r ValidationResult) Validate() error {
	if err := lifecycleRequiredText("validation run id", r.RunID, 128); err != nil {
		return err
	}
	if err := lifecycleRequiredText("validation workspace id", r.WorkspaceID, 256); err != nil {
		return err
	}
	if err := lifecycleRequiredText("validation command id", r.CommandID, 128); err != nil {
		return err
	}
	if r.CommandVersion < 1 {
		return fmt.Errorf("validation command version must be positive")
	}
	if r.ExitCode < -1 {
		return fmt.Errorf("validation exit code is invalid")
	}
	if r.OutputArtifactRef != "" {
		if err := lifecycleOptionalText("validation artifact ref", r.OutputArtifactRef, 512); err != nil {
			return err
		}
		if r.OutputHash == "" {
			return fmt.Errorf("validation artifact ref requires output hash")
		}
	}
	if r.OutputHash != "" {
		if err := lifecycleOptionalText("validation output hash", r.OutputHash, 128); err != nil {
			return err
		}
		if err := validateContentHash("validation output hash", r.OutputHash); err != nil {
			return err
		}
	}
	if len(r.OutputExcerpt) > 64<<10 {
		return fmt.Errorf("validation output excerpt exceeds 65536 bytes")
	}
	if r.BytesRetrieved < 0 || r.BytesRetrieved > 64<<10 {
		return fmt.Errorf("validation result bytes are out of bounds")
	}
	if !r.StartedAt.IsZero() && !r.CompletedAt.IsZero() && r.CompletedAt.Before(r.StartedAt) {
		return fmt.Errorf("validation completion precedes start")
	}
	return lifecycleOptionalText("validation summary", r.Summary, 1024)
}

// Validate 校验 publication 请求，尤其是 baseline/tree CAS 和 branch scope。
func (r PublicationRequest) Validate() error {
	if err := lifecycleRequiredText("publication run id", r.RunID, 128); err != nil {
		return err
	}
	if err := lifecycleRequiredText("publication project id", r.ProjectID, 128); err != nil {
		return err
	}
	if err := lifecycleRequiredText("publication baseline commit", r.BaselineCommit, 256); err != nil {
		return err
	}
	if err := validateRef("publication target branch", r.TargetBranch); err != nil {
		return err
	}
	if err := validateRef("publication branch ref", r.BranchRef); err != nil {
		return err
	}
	if r.BranchRef == r.TargetBranch {
		return fmt.Errorf("publication branch must differ from target branch")
	}
	if err := lifecycleRequiredText("publication commit message", r.CommitMessage, 256); err != nil {
		return err
	}
	if err := lifecycleRequiredText("publication patch artifact", r.PatchArtifactRef, 512); err != nil {
		return err
	}
	if err := lifecycleRequiredText("publication patch hash", r.PatchContentHash, 128); err != nil {
		return err
	}
	if err := validateContentHash("publication patch hash", r.PatchContentHash); err != nil {
		return err
	}
	if err := lifecycleRequiredText("publication expected tree hash", r.ExpectedTreeHash, 128); err != nil {
		return err
	}
	return validateIdempotencyKey(r.IdempotencyKey)
}

// Validate 校验 publication 结果，并强制保留 human merge gate。
func (r PublicationResult) Validate() error {
	if err := validateRef("publication result branch ref", r.BranchRef); err != nil {
		return err
	}
	if r.BranchRef == r.TargetBranch {
		return fmt.Errorf("publication result branch must differ from target branch")
	}
	if err := lifecycleRequiredText("publication result commit hash", r.CommitHash, 256); err != nil {
		return err
	}
	if r.DraftChangeRef != "" {
		if err := lifecycleOptionalText("publication draft change ref", r.DraftChangeRef, 512); err != nil {
			return err
		}
	}
	if r.CompareURL != "" {
		lower := strings.ToLower(r.CompareURL)
		if strings.Contains(r.CompareURL, "@") || strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "authorization") || strings.Contains(lower, "api_key") {
			return fmt.Errorf("publication compare URL contains authority-bearing data")
		}
		if err := lifecycleOptionalText("publication compare URL", r.CompareURL, 1024); err != nil {
			return err
		}
	}
	if strings.TrimSpace(r.BaselineCommit) == "" || strings.TrimSpace(r.TargetBranch) == "" {
		return fmt.Errorf("publication result baseline and target are required")
	}
	if !r.HumanReviewRequired {
		return fmt.Errorf("publication result must require human review")
	}
	return lifecycleOptionalText("publication summary", r.Summary, 1024)
}

// Validate 校验 lifecycle effect 的持久化字段和 bounded error metadata。
func (e LifecycleEffect) Validate() error {
	if err := lifecycleRequiredText("lifecycle effect run id", e.RunID, 128); err != nil {
		return err
	}
	if !e.Kind.IsKnown() {
		return fmt.Errorf("lifecycle effect kind is invalid")
	}
	if err := validateIdempotencyKey(e.IdempotencyKey); err != nil {
		return err
	}
	if !e.State.IsKnown() {
		return fmt.Errorf("lifecycle effect state is invalid")
	}
	if e.Attempt < 1 {
		return fmt.Errorf("lifecycle effect attempt must be positive")
	}
	for label, value := range map[string]string{
		"baseline commit":  e.BaselineCommit,
		"workspace id":     e.WorkspaceID,
		"base tree hash":   e.BaseTreeHash,
		"result tree hash": e.ResultTreeHash,
		"artifact ref":     e.ArtifactRef,
		"content hash":     e.ContentHash,
		"command id":       e.CommandID,
		"branch ref":       e.BranchRef,
		"target branch":    e.TargetBranch,
		"commit hash":      e.CommitHash,
		"draft change ref": e.DraftChangeRef,
		"compare URL":      e.CompareURL,
		"error code":       e.ErrorCode,
		"summary":          e.Summary,
	} {
		if err := lifecycleOptionalText(label, value, maxLifecycleFieldLength(label)); err != nil {
			return err
		}
	}
	if e.CompareURL != "" {
		lower := strings.ToLower(e.CompareURL)
		if strings.Contains(e.CompareURL, "@") || strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "authorization") || strings.Contains(lower, "api_key") {
			return fmt.Errorf("lifecycle effect compare URL contains authority-bearing data")
		}
	}
	if e.CommandVersion < 0 {
		return fmt.Errorf("lifecycle effect command version must not be negative")
	}
	if e.State == LifecycleEffectSucceeded {
		switch e.Kind {
		case LifecycleEffectWorkspace:
			if e.BaselineCommit == "" || e.WorkspaceID == "" || e.BaseTreeHash == "" || e.ResultTreeHash == "" {
				return fmt.Errorf("successful workspace effect is missing identity")
			}
		case LifecycleEffectPatch:
			if e.WorkspaceID == "" || e.ArtifactRef == "" || e.ContentHash == "" || e.ResultTreeHash == "" {
				return fmt.Errorf("successful patch effect is missing replay identity")
			}
			if err := validateContentHash("lifecycle patch content hash", e.ContentHash); err != nil {
				return err
			}
		case LifecycleEffectValidation:
			if !e.ValidationKnown || e.CommandID == "" || e.CommandVersion < 1 {
				return fmt.Errorf("successful validation effect is missing authoritative result")
			}
			if e.ContentHash != "" {
				if err := validateContentHash("lifecycle validation content hash", e.ContentHash); err != nil {
					return err
				}
			}
		case LifecycleEffectPublication:
			if e.BaselineCommit == "" || e.TargetBranch == "" || e.BranchRef == "" || e.CommitHash == "" || e.ArtifactRef == "" || e.ContentHash == "" {
				return fmt.Errorf("successful publication effect is missing replay identity")
			}
			if err := validateContentHash("lifecycle publication content hash", e.ContentHash); err != nil {
				return err
			}
		}
	}
	if e.UpdatedAt.Before(e.CreatedAt) && !e.CreatedAt.IsZero() && !e.UpdatedAt.IsZero() {
		return fmt.Errorf("lifecycle effect updated time precedes created time")
	}
	return nil
}

func containsLifecycleString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validateContentHash(label, value string) error {
	if len(value) != 64 {
		return fmt.Errorf("%s must be a 64-character SHA-256 hex value", label)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("%s must be a SHA-256 hex value", label)
	}
	return nil
}

func lifecycleRequiredText(label, value string, max int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", label)
	}
	return lifecycleOptionalText(label, value, max)
}

func lifecycleOptionalText(label, value string, max int) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s is not valid UTF-8", label)
	}
	if max > 0 && utf8.RuneCountInString(value) > max {
		return fmt.Errorf("%s exceeds bounds", label)
	}
	for _, r := range value {
		if r == '\x00' || r == '\r' || r == '\n' {
			return fmt.Errorf("%s contains unsupported control characters", label)
		}
	}
	return nil
}

func validateIdempotencyKey(value string) error {
	if err := lifecycleRequiredText("idempotency key", value, 256); err != nil {
		return err
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		switch r {
		case ':', '-', '_', '.', '/':
		default:
			return fmt.Errorf("idempotency key contains unsupported characters")
		}
	}
	return nil
}

func validateWorkspacePath(path string) error {
	if err := lifecycleRequiredText("workspace path", path, 512); err != nil {
		return err
	}
	if strings.HasPrefix(path, "/") || strings.Contains(path, "\\") {
		return fmt.Errorf("workspace path must be repository-relative")
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == ".." || segment == "" {
			return fmt.Errorf("workspace path contains traversal or empty segment")
		}
	}
	return nil
}

func validateRef(label, value string) error {
	if err := lifecycleRequiredText(label, value, 256); err != nil {
		return err
	}
	if strings.HasPrefix(value, "-") || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") ||
		strings.Contains(value, "//") || strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.HasSuffix(value, ".") ||
		strings.ContainsAny(value, " ~^:?*[\\\\") {
		return fmt.Errorf("%s is outside the allowed ref scope", label)
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".") || strings.HasSuffix(component, ".lock") {
			return fmt.Errorf("%s is outside the allowed ref scope", label)
		}
	}
	return nil
}

func maxLifecycleFieldLength(label string) int {
	switch label {
	case "error code":
		return 128
	case "summary", "compare URL":
		return 1024
	default:
		return 512
	}
}
