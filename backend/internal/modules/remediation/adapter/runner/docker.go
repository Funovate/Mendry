package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"mendry/backend/internal/modules/remediation/domain"
	"mendry/backend/internal/platform/observability"
)

const (
	defaultDockerCommand = "docker"
	maxValidationOutput  = 64 << 10
	validationCleanupTTL = 5 * time.Second
)

var immutableImageDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type WorkspaceMountProvider interface {
	ResolveWorkspaceMount(context.Context, string, string, string) (string, error)
}

type ArtifactWriter interface {
	Put(context.Context, []byte) (string, string, error)
}

type DockerOptions struct {
	Command    string
	Workspaces WorkspaceMountProvider
	Artifacts  ArtifactWriter
}

type DockerRunner struct {
	command    string
	workspaces WorkspaceMountProvider
	artifacts  ArtifactWriter
}

func NewDockerRunner(options DockerOptions) (*DockerRunner, error) {
	if options.Workspaces == nil || options.Artifacts == nil {
		return nil, fmt.Errorf("Docker runner workspace and artifact dependencies are required")
	}
	command := strings.TrimSpace(options.Command)
	if command == "" {
		command = defaultDockerCommand
	}
	return &DockerRunner{command: command, workspaces: options.Workspaces, artifacts: options.Artifacts}, nil
}

var _ domain.ValidationPort = (*DockerRunner)(nil)

func (r *DockerRunner) Run(ctx context.Context, request domain.ValidationRequest) (domain.ValidationResult, error) {
	if r == nil || r.command == "" || r.workspaces == nil || r.artifacts == nil {
		return domain.ValidationResult{}, runtimeError("docker_runner_unavailable", true, nil)
	}
	if err := request.Validate(); err != nil {
		return domain.ValidationResult{}, runtimeError("validation_request_invalid", false, err)
	}
	command, err := approvedCommand(request)
	if err != nil {
		return domain.ValidationResult{}, runtimeError("validation_command_unavailable", false, err)
	}
	if !immutableImageDigest.MatchString(request.Profile.ImageDigest) {
		return domain.ValidationResult{}, runtimeError("validation_image_invalid", false, nil)
	}
	if request.Profile.CPULimit < 1 || request.Profile.CPULimit > 16 || request.Profile.MemoryLimitMiB < 256 || request.Profile.MemoryLimitMiB > 65536 ||
		request.Profile.WorkspaceLimitMiB < 1024 || request.Profile.WorkspaceLimitMiB > 102400 {
		return domain.ValidationResult{}, runtimeError("validation_resources_invalid", false, nil)
	}
	workdir, err := containerWorkdir(request.Profile.WorkingDirectory)
	if err != nil {
		return domain.ValidationResult{}, runtimeError("validation_working_directory_invalid", false, err)
	}
	mountPath, err := r.workspaces.ResolveWorkspaceMount(ctx, request.RunID, request.WorkspaceID, request.ExpectedTreeHash)
	if err != nil {
		return domain.ValidationResult{}, err
	}
	mountPath, err = filepath.Abs(mountPath)
	if err != nil || strings.ContainsAny(mountPath, ",:") {
		return domain.ValidationResult{}, runtimeError("workspace_mount_invalid", false, err)
	}
	info, err := os.Lstat(mountPath)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return domain.ValidationResult{}, runtimeError("workspace_mount_invalid", false, err)
	}
	containerName := validationContainerName(request)
	r.cleanupContainer(containerName)

	memory := request.Profile.MemoryLimitMiB
	tmpfsMiB := memory / 2
	if tmpfsMiB < 128 {
		tmpfsMiB = 128
	}
	if tmpfsMiB > 4096 {
		tmpfsMiB = 4096
	}
	args := []string{
		"run", "--rm", "--name", containerName,
		"--label", "mendry.managed=remediation-validation",
		"--network=none", "--read-only", "--cap-drop=ALL",
		"--security-opt=no-new-privileges:true", "--pids-limit=128",
		"--cpus=" + strconv.Itoa(request.Profile.CPULimit),
		"--memory=" + strconv.Itoa(memory) + "m",
		"--memory-swap=" + strconv.Itoa(memory) + "m",
		"--ulimit=nofile=1024:1024",
		"--user=" + strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()),
		"--volume", mountPath + ":/workspace:ro",
		"--workdir", workdir,
		"--tmpfs", "/tmp:rw,nosuid,nodev,size=" + strconv.Itoa(tmpfsMiB) + "m",
		"--env", "HOME=/tmp", "--env", "TMPDIR=/tmp", "--env", "CI=true",
		"--env", "GIT_CONFIG_NOSYSTEM=1", "--env", "GIT_CONFIG_GLOBAL=/dev/null",
		"--env", "GIT_TERMINAL_PROMPT=0",
		"--pull=never", request.Profile.ImageDigest,
	}
	args = append(args, command.Argv...)
	started := time.Now().UTC()
	commandCtx, cancel := context.WithTimeout(ctx, time.Duration(command.TimeoutSeconds)*time.Second)
	defer cancel()
	run := exec.CommandContext(commandCtx, r.command, args...)
	stdout := &boundedOutput{limit: maxValidationOutput / 2}
	stderr := &boundedOutput{limit: maxValidationOutput / 2}
	run.Stdout = stdout
	run.Stderr = stderr
	runErr := run.Run()
	completed := time.Now().UTC()
	exitCode := 0
	if runErr != nil {
		if exit, ok := runErr.(*exec.ExitError); ok {
			exitCode = exit.ExitCode()
		} else {
			r.cleanupContainer(containerName)
			if ctx.Err() != nil {
				return domain.ValidationResult{}, ctx.Err()
			}
			return domain.ValidationResult{}, runtimeError("docker_validation_failed", true, runErr)
		}
	}
	if commandCtx.Err() != nil {
		r.cleanupContainer(containerName)
		if ctx.Err() != nil {
			return domain.ValidationResult{}, ctx.Err()
		}
		exitCode = -1
	}
	if _, err := r.workspaces.ResolveWorkspaceMount(ctx, request.RunID, request.WorkspaceID, request.ExpectedTreeHash); err != nil {
		return domain.ValidationResult{}, runtimeError("validation_tree_changed", false, err)
	}

	output := joinOutput(stdout.Bytes(), stderr.Bytes(), stdout.Truncated(), stderr.Truncated())
	snapshot, err := observability.SnapshotRemediationPayload(output)
	if err != nil {
		return domain.ValidationResult{}, runtimeError("validation_output_invalid", false, err)
	}
	safeOutput := []byte(snapshot.Text)
	artifactRef, outputHash, err := r.artifacts.Put(ctx, safeOutput)
	if err != nil {
		return domain.ValidationResult{}, runtimeError("validation_artifact_unavailable", true, err)
	}
	passed := runErr == nil && commandCtx.Err() == nil && exitCode == 0
	summary := "validation passed"
	if commandCtx.Err() != nil {
		summary = "validation command timed out"
	} else if !passed {
		summary = fmt.Sprintf("validation command exited with code %d", exitCode)
	}
	return domain.ValidationResult{
		RunID: request.RunID, WorkspaceID: request.WorkspaceID, CommandID: command.ID,
		CommandVersion: command.Version, TreeHash: request.ExpectedTreeHash,
		ImageDigest: request.Profile.ImageDigest, Passed: passed, ExitCode: exitCode,
		OutputArtifactRef: artifactRef, OutputHash: outputHash, OutputExcerpt: snapshot.Text,
		BytesRetrieved: int64(len(safeOutput)), Summary: summary,
		StartedAt: started, CompletedAt: completed,
	}, nil
}

func approvedCommand(request domain.ValidationRequest) (domain.ValidationCommandSnapshot, error) {
	for _, command := range request.Profile.RequiredCommands {
		if command.ID == request.CommandID && command.Version == request.CommandVersion &&
			request.Profile.WorkingDirectory != "" && len(command.Argv) > 0 && len(command.Argv) <= 64 && command.TimeoutSeconds >= 1 && command.TimeoutSeconds <= 3600 {
			for _, argument := range command.Argv {
				if strings.TrimSpace(argument) == "" || len(argument) > 4096 || strings.ContainsRune(argument, '\x00') {
					return domain.ValidationCommandSnapshot{}, fmt.Errorf("validation argv is invalid")
				}
			}
			return command, nil
		}
	}
	return domain.ValidationCommandSnapshot{}, fmt.Errorf("validation command is not in the immutable run profile")
}

func containerWorkdir(value string) (string, error) {
	if value == "" || strings.ContainsRune(value, '\x00') || filepath.IsAbs(value) {
		return "", fmt.Errorf("working directory is invalid")
	}
	clean := filepath.Clean(value)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == "." && value != "." {
		return "", fmt.Errorf("working directory escaped the repository")
	}
	for _, component := range strings.Split(strings.ReplaceAll(clean, "\\", "/"), "/") {
		if component == ".." || component == ".git" {
			return "", fmt.Errorf("working directory is invalid")
		}
	}
	if clean == "." {
		return "/workspace", nil
	}
	return "/workspace/" + filepath.ToSlash(clean), nil
}

func validationContainerName(request domain.ValidationRequest) string {
	sum := sha256.Sum256([]byte(request.RunID + "\x00" + request.CommandID + "\x00" + strconv.FormatInt(request.CommandVersion, 10) + "\x00" + request.IdempotencyKey))
	return "mendry-validation-" + hex.EncodeToString(sum[:16])
}

func (r *DockerRunner) cleanupContainer(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), validationCleanupTTL)
	defer cancel()
	command := exec.CommandContext(ctx, r.command, "rm", "--force", name)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	_ = command.Run()
}

type boundedOutput struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedOutput) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	originalLength := len(value)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
			b.truncated = true
		}
		_, _ = b.buffer.Write(value)
	} else if originalLength > 0 {
		b.truncated = true
	}
	return originalLength, nil
}

func (b *boundedOutput) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buffer.Bytes()...)
}

func (b *boundedOutput) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

func joinOutput(stdout, stderr []byte, stdoutTruncated, stderrTruncated bool) string {
	var output strings.Builder
	if len(stdout) > 0 {
		output.WriteString("stdout:\n")
		output.Write(stdout)
		output.WriteByte('\n')
	}
	if stdoutTruncated {
		output.WriteString("[stdout truncated]\n")
	}
	if len(stderr) > 0 {
		output.WriteString("stderr:\n")
		output.Write(stderr)
		output.WriteByte('\n')
	}
	if stderrTruncated {
		output.WriteString("[stderr truncated]\n")
	}
	return output.String()
}

func runtimeError(code string, retryable bool, cause error) error {
	return &domain.LifecycleRuntimeError{Code: code, Retryable: retryable, Cause: cause}
}
