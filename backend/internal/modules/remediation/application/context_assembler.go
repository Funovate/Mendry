package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fixthe/backend/internal/modules/remediation/domain"
)

// ContextAssembler 构造 remediation 首轮的 control-plane bootstrap metadata。
// 远程 Git、SSH 和日志读取全部延后到 ToolGateway，连接器失败不能阻断首轮
// model turn；该类型因此不持有或调用任何 read port。
type ContextAssembler struct {
}

// NewContextAssembler 保留旧构造签名以兼容既有 wiring；read port 不会在
// preparing_context 阶段被调用。
func NewContextAssembler(
	_ domain.RepositoryReadPort,
	_ domain.EvidenceLogPort,
) *ContextAssembler {
	return &ContextAssembler{}
}

// AssembleInitialContext 构造不包含远程数据的首轮 bootstrap context。
func (c *ContextAssembler) AssembleInitialContext(
	ctx context.Context,
	ref domain.RepoRef,
	scope domain.EvidenceScope,
) (string, domain.Effect, error) {
	return c.AssembleInitialContextObserved(ctx, RunIdentity{}, noopRunObserver{}, ref, scope, domain.SourceCapabilitySnapshot{})
}

// AssembleInitialContextObserved 校验部署 commit 并记录 metadata observation，
// 不执行 Git、SSH 或日志 API 读取；可选 MCP catalog probe 由后续 runtime 独立
// 控制，不能让本函数阻塞 diagnosing 状态。
func (c *ContextAssembler) AssembleInitialContextObserved(
	ctx context.Context,
	run RunIdentity,
	observer RunObserver,
	ref domain.RepoRef,
	scope domain.EvidenceScope,
	source domain.SourceCapabilitySnapshot,
) (string, domain.Effect, error) {
	return c.AssembleInitialContextWithEvidenceObserved(ctx, run, observer, ref, scope, source, domain.BootstrapEvidence{})
}

// AssembleInitialContextWithEvidenceObserved adds the triggering Observation
// reference and safe pre-run evidence to the metadata-only bootstrap. Remote
// Git/SSH reads remain deferred to tools.
func (c *ContextAssembler) AssembleInitialContextWithEvidenceObserved(
	ctx context.Context,
	run RunIdentity,
	observer RunObserver,
	ref domain.RepoRef,
	scope domain.EvidenceScope,
	source domain.SourceCapabilitySnapshot,
	bootstrap domain.BootstrapEvidence,
) (string, domain.Effect, error) {
	observer = normalizeRunObserver(observer)
	if strings.TrimSpace(ref.Commit) == "" {
		return "", domain.Effect{}, fmt.Errorf("assemble bootstrap context: deployed commit is required")
	}
	bootstrap = prepareBootstrapEvidence(bootstrap)
	started := time.Now()
	repositoryStatus := "configured"
	if strings.TrimSpace(ref.ProjectID) == "" {
		repositoryStatus = "identity_pending"
	}
	evidenceStatus := "configured"
	if strings.TrimSpace(scope.SourceID) == "" {
		evidenceStatus = "unavailable"
	}
	metadata := map[string]interface{}{
		"project_id":      ref.ProjectID,
		"environment_id":  scope.EnvironmentID,
		"source_id":       scope.SourceID,
		"source_kind":     source.Kind,
		"deployed_commit": ref.Commit,
		"repository":      repositoryStatus,
		"evidence":        evidenceStatus,
		"remote_reads":    "tool_driven",
		"alert_quality":   bootstrap.AlertQuality,
		"evidence_count":  len(bootstrap.Records),
		"time_basis":      bootstrap.TimeBasis,
		"time_certainty":  bootstrap.TimeCertainty,
	}
	if bootstrap.Observation != nil {
		metadata["observation_id"] = bootstrap.Observation.ID
	}
	if !bootstrap.TimeRange.Start.IsZero() && !bootstrap.TimeRange.End.IsZero() {
		metadata["time_window_start"] = bootstrap.TimeRange.Start.UTC().Format(time.RFC3339Nano)
		metadata["time_window_end"] = bootstrap.TimeRange.End.UTC().Format(time.RFC3339Nano)
	}
	if source.Kind == "ssh" {
		metadata["ssh_host"] = source.SSHHost
		metadata["ssh_user"] = source.SSHUser
		metadata["ssh_project_folder"] = source.SSHProjectFolder
		metadata["ssh_log_path"] = source.SSHLogPath
		metadata["ssh_deployment"] = source.SSHDeploymentKind
		if source.SSHDeploymentKind == "docker" {
			metadata["ssh_container_name"] = source.SSHContainerName
		}
	}
	observer.ContextCompleted(ctx, ContextObservation{
		Run: run, Phase: domain.RunStatePreparingContext, Operation: "bootstrap.metadata",
		Duration: time.Since(started), Outcome: "success",
		Bytes: bootstrapEvidenceBytes(bootstrap), PayloadKind: "bootstrap_metadata", Payload: metadata,
	})

	var b strings.Builder
	fmt.Fprintf(&b, "remediation bootstrap: historical_commit=%s; repository_reads=current_production_branch\n", ref.Commit)
	fmt.Fprintf(&b, "repository: project=%s status=%s reads=tool_driven\n", ref.ProjectID, repositoryStatus)
	fmt.Fprintf(&b, "evidence: environment=%s source=%s kind=%s status=%s reads=tool_driven\n", scope.EnvironmentID, scope.SourceID, source.Kind, evidenceStatus)
	bootstrapEvidenceBytes := renderBootstrapEvidence(&b, bootstrap)
	if source.Kind == "ssh" {
		fmt.Fprintf(&b, "ssh inspect hints: host=%s user=%s projectFolder=%s logPath=%s\n", source.SSHHost, source.SSHUser, source.SSHProjectFolder, source.SSHLogPath)
		if source.SSHDeploymentKind == "docker" {
			fmt.Fprintf(&b, "ssh deployment: docker container=%s; use docker.logs for bounded incident-window stdout/stderr (the adapter re-resolves the saved exact name) and ssh.inspect for read-only host/network/process diagnostics. Never pass a container ID to docker.logs.\n", source.SSHContainerName)
		} else {
			fmt.Fprint(&b, "logPath is a bootstrap hint for where logs often live, not a file that exists by name. Use ssh.inspect to ls that directory and discover actual file names before reading; the harness never auto-tails logPath.\n")
		}
	}
	if bootstrapEvidenceBytes == 0 {
		fmt.Fprint(&b, "No triggering evidence payload has been loaded yet. Use the advertised bounded read tools; connector failures are observations.")
	} else {
		fmt.Fprint(&b, "Use the triggering evidence as an anchor, then collect bounded context before asserting causality. Connector failures are observations.")
	}
	return b.String(), domain.Effect{EvidenceBytes: bootstrapEvidenceBytes}, nil
}

func bootstrapEvidenceBytes(value domain.BootstrapEvidence) int64 {
	var builder strings.Builder
	return renderBootstrapEvidence(&builder, prepareBootstrapEvidence(value))
}
