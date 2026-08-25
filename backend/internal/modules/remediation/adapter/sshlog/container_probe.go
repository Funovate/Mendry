package sshlog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"sort"
	"strings"
	"time"

	projectapplication "fixthe/backend/internal/modules/projects/application"
	projectdomain "fixthe/backend/internal/modules/projects/domain"
	"fixthe/backend/internal/platform/observability"
)

const (
	defaultContainerInventoryCommand = "docker ps -a --no-trunc --format '{{json .}}'"
	maxContainerInventoryBytes       = 256 << 10
	maxContainerInventoryEntries     = 100
)

// ContainerProbeOptions 构造 admin-only Docker inventory adapter；不接受模型命令。
type ContainerProbeOptions struct {
	Secrets        SecretLoader
	Cipher         projectapplication.Cipher
	SSHCommand     string
	CommandTimeout time.Duration
	Logger         *slog.Logger
}

// ContainerProbe 通过固定的 docker ps 只读命令返回有界容器身份。
type ContainerProbe struct {
	secrets SecretLoader
	cipher  projectapplication.Cipher
	command string
	timeout time.Duration
	logger  *slog.Logger
}

// NewContainerProbe 构造不暴露 credential 或远端命令的 Docker inventory probe。
func NewContainerProbe(options ContainerProbeOptions) (*ContainerProbe, error) {
	if options.Secrets == nil || options.Cipher == nil {
		return nil, fmt.Errorf("docker container probe dependencies are required")
	}
	command := strings.TrimSpace(options.SSHCommand)
	if command == "" {
		command = defaultSSHCommand
	}
	timeout := options.CommandTimeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &ContainerProbe{
		secrets: options.Secrets,
		cipher:  options.Cipher,
		command: command,
		timeout: timeout,
		logger:  options.Logger,
	}, nil
}

var _ projectapplication.ContainerProbePort = (*ContainerProbe)(nil)

// ListContainers 执行固定的 docker ps -a inventory；输出超限或解析失败时整体失败，
// 避免 UI 把不完整 inventory 当成完整选择集。
func (p *ContainerProbe) ListContainers(ctx context.Context, request projectapplication.ContainerProbeRequest) ([]projectdomain.DockerContainer, error) {
	if err := projectdomain.ValidateSSHContainerProbe(request.Host, request.Port, request.User, request.CredentialSecretID); err != nil {
		return nil, err
	}
	started := time.Now()
	encrypted, err := p.secrets.GetEncryptedSecret(ctx, request.ProjectID, request.CredentialSecretID)
	if err != nil {
		return nil, fmt.Errorf("load SSH credential: %w", err)
	}
	if encrypted.Kind != projectdomain.SecretSSHPrivateKey {
		return nil, fmt.Errorf("SSH container probe requires a private key")
	}
	plaintext, err := p.cipher.Decrypt(request.ProjectID, encrypted.ID, encrypted.Kind, encrypted.Ciphertext, encrypted.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decrypt SSH credential: %w", err)
	}
	defer clearBytes(plaintext)

	reader := &Reader{command: p.command, timeout: p.timeout}
	args, cleanup, err := reader.sshArgs(SourceConfig{
		ProjectID: request.ProjectID, Host: request.Host, Port: request.Port, User: request.User,
		CredentialSecretID: request.CredentialSecretID, CredentialKind: encrypted.Kind, plaintext: plaintext,
	}, defaultContainerInventoryCommand)
	if err != nil {
		return nil, fmt.Errorf("prepare SSH container probe: %w", err)
	}
	defer cleanup()

	stdout, stderr, truncated, runErr := runBoundedSSHCommand(ctx, p.command, args, p.timeout, maxContainerInventoryBytes)
	if runErr != nil {
		p.logInventory(ctx, request, started, len(stdout)+len(stderr), runErr)
		return nil, runErr
	}
	if truncated {
		return nil, fmt.Errorf("docker inventory output exceeded limit")
	}
	containers, err := parseDockerInventory(stdout)
	if err != nil {
		p.logInventory(ctx, request, started, len(stdout)+len(stderr), err)
		return nil, err
	}
	p.logInventory(ctx, request, started, len(stdout)+len(stderr), nil)
	return containers, nil
}

func runBoundedSSHCommand(ctx context.Context, commandName string, args []string, timeout time.Duration, limit int) (stdout, stderr string, truncated bool, runErr error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(ctx, commandName, args...)
	capture := &inspectCapture{limit: limit, abort: func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
	}}
	command.Stdout = inspectStreamWriter{capture: capture, dest: &capture.stdout}
	command.Stderr = inspectStreamWriter{capture: capture, dest: &capture.stderr}
	err := command.Run()
	if capture.truncated {
		return capture.stdout.String(), capture.stderr.String(), true, nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return "", "", false, fmt.Errorf("SSH command timed out")
		}
		return capture.stdout.String(), capture.stderr.String(), false, fmt.Errorf("SSH command failed")
	}
	return capture.stdout.String(), capture.stderr.String(), false, nil
}

func (p *ContainerProbe) logInventory(ctx context.Context, request projectapplication.ContainerProbeRequest, started time.Time, bytes int, err error) {
	// probe 失败只记录 SSH evidence 边界分类；host/user/credential/output 不进入日志。
	observability.LogSSHEvidenceRequest(ctx, p.logger, observability.SSHEvidenceRequest{
		Operation: "docker_inventory", Command: defaultContainerInventoryCommand,
		Host: request.Host, Port: request.Port, Duration: time.Since(started), Bytes: bytes,
		CredentialSecretID: request.CredentialSecretID, CredentialKind: string(projectdomain.SecretSSHPrivateKey), Err: err,
	})
}

type dockerInventoryRow struct {
	ID     string
	Name   string
	Image  string
	State  string
	Status string
}

func parseDockerInventory(raw string) ([]projectdomain.DockerContainer, error) {
	lines := splitNonEmpty(raw)
	containers := make([]projectdomain.DockerContainer, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		row, err := decodeDockerInventoryRow(line)
		if err != nil || strings.TrimSpace(row.Name) == "" || strings.TrimSpace(row.ID) == "" {
			continue
		}
		row.Name = strings.TrimPrefix(strings.TrimSpace(row.Name), "/")
		if row.Name == "" || strings.ContainsAny(row.Name, "\r\n") {
			continue
		}
		if _, ok := seen[row.Name]; ok {
			continue
		}
		seen[row.Name] = struct{}{}
		state := normalizeDockerState(row.State, row.Status)
		containers = append(containers, projectdomain.DockerContainer{
			Name: row.Name, ID: strings.TrimSpace(row.ID), Image: strings.TrimSpace(row.Image),
			State: state, Status: strings.TrimSpace(row.Status),
		})
	}
	sort.SliceStable(containers, func(i, j int) bool {
		ri, rj := dockerStateRank(containers[i].State), dockerStateRank(containers[j].State)
		if ri != rj {
			return ri < rj
		}
		return containers[i].Name < containers[j].Name
	})
	if len(containers) > maxContainerInventoryEntries {
		containers = containers[:maxContainerInventoryEntries]
	}
	if len(lines) > 0 && len(containers) == 0 {
		return nil, fmt.Errorf("docker inventory response is invalid")
	}
	return containers, nil
}

func decodeDockerInventoryRow(line string) (dockerInventoryRow, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &values); err != nil {
		return dockerInventoryRow{}, err
	}
	return dockerInventoryRow{
		ID:     dockerJSONField(values, "ID", "Id", "id"),
		Name:   dockerJSONField(values, "Names", "Name", "name", "names"),
		Image:  dockerJSONField(values, "Image", "image"),
		State:  dockerJSONField(values, "State", "state"),
		Status: dockerJSONField(values, "Status", "status"),
	}, nil
}

func dockerJSONField(values map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		for actual, raw := range values {
			if !strings.EqualFold(actual, key) {
				continue
			}
			var value string
			if json.Unmarshal(raw, &value) == nil {
				return value
			}
		}
	}
	return ""
}

func normalizeDockerState(state, status string) string {
	state = strings.ToLower(strings.TrimSpace(state))
	if state != "" {
		return state
	}
	status = strings.ToLower(strings.TrimSpace(status))
	switch {
	case strings.Contains(status, "restarting"):
		return "restarting"
	case strings.HasPrefix(status, "up"), strings.Contains(status, "running"):
		return "running"
	default:
		return "stopped"
	}
}

func dockerStateRank(state string) int {
	switch strings.ToLower(state) {
	case "running":
		return 0
	case "restarting":
		return 1
	default:
		return 2
	}
}
