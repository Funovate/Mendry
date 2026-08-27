package sshlog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	projectapplication "fixthe/backend/internal/modules/projects/application"
	projectdomain "fixthe/backend/internal/modules/projects/domain"
	"fixthe/backend/internal/modules/remediation/domain"
)

const (
	dockerTestProjectID = "019ff544-405c-7d21-9f10-cb3fc579605c"
	dockerTestSourceID  = "019ff544-405c-7d23-9f10-cb3fc579605c"
	dockerTestSecretID  = "019ff544-405c-7d24-9f10-cb3fc579605c"
)

type dockerTestSource struct {
	cfg SourceConfig
}

func (s dockerTestSource) LoadSSHSource(context.Context, string, string) (SourceConfig, error) {
	return s.cfg, nil
}

type dockerTestSecrets struct {
	kind projectdomain.SecretKind
}

func (s dockerTestSecrets) GetEncryptedSecret(context.Context, string, string) (projectdomain.EncryptedSecret, error) {
	kind := s.kind
	if kind == "" {
		kind = projectdomain.SecretSSHPrivateKey
	}
	return projectdomain.EncryptedSecret{
		Secret: projectdomain.Secret{ID: dockerTestSecretID, ProjectID: dockerTestProjectID, Kind: kind},
	}, nil
}

type dockerTestCipher struct{}

func (dockerTestCipher) Encrypt(string, string, projectdomain.SecretKind, []byte) ([]byte, []byte, int32, error) {
	return nil, nil, 0, errors.New("unused")
}

func (dockerTestCipher) Decrypt(string, string, projectdomain.SecretKind, []byte, []byte) ([]byte, error) {
	return []byte("-----BEGIN OPENSSH PRIVATE KEY-----\ntest\n-----END OPENSSH PRIVATE KEY-----\n"), nil
}

func (dockerTestCipher) EncryptWebhookToken(string, string, []byte) ([]byte, []byte, error) {
	return nil, nil, errors.New("unused")
}

func (dockerTestCipher) DecryptWebhookToken(string, string, []byte, []byte) ([]byte, error) {
	return nil, errors.New("unused")
}

func TestParseDockerInventorySortsRunningBeforeStoppedAndBoundsAfterSorting(t *testing.T) {
	rows := make([]string, 0, 105)
	for i := 0; i < 100; i++ {
		rows = append(rows, dockerInventoryFixture(fmt.Sprintf("stopped-%03d", i), fmt.Sprintf("%064x", i+1), "stopped", "Exited (0)"))
	}
	rows = append(rows,
		dockerInventoryFixture("running-z", strings.Repeat("a", 64), "running", "Up 2 minutes"),
		dockerInventoryFixture("running-a", strings.Repeat("b", 64), "running", "Up 1 minute"),
		dockerInventoryFixture("restarting-a", strings.Repeat("c", 64), "restarting", "Restarting (1)"),
		`{"ID":"bad","Names":"running-a","Image":"ignored","State":"running"}`,
		"not json",
	)

	containers, err := parseDockerInventory(strings.Join(rows, "\n"))
	if err != nil {
		t.Fatalf("parseDockerInventory() error = %v", err)
	}
	if len(containers) != maxContainerInventoryEntries {
		t.Fatalf("container count = %d, want %d", len(containers), maxContainerInventoryEntries)
	}
	for index, want := range []string{"running-a", "running-z", "restarting-a"} {
		if containers[index].Name != want {
			t.Fatalf("container[%d].Name = %q, want %q", index, containers[index].Name, want)
		}
	}
	for _, container := range containers[3:] {
		if container.State != "stopped" {
			t.Fatalf("container after active entries = %#v, want stopped", container)
		}
	}

	duplicate, err := parseDockerInventory(strings.Join([]string{
		dockerInventoryFixture("/running-a", strings.Repeat("b", 64), "running", "Up 1 minute"),
		dockerInventoryFixture("running-a", strings.Repeat("b", 64), "running", "Up 1 minute"),
	}, "\n"))
	if err != nil || len(duplicate) != 1 || duplicate[0].Name != "running-a" {
		t.Fatalf("duplicate inventory = %#v, err=%v", duplicate, err)
	}
}

func TestContainerProbeUsesFixedInventoryCommand(t *testing.T) {
	fakeSSH := writeDockerSSH(t)
	commands := filepath.Join(t.TempDir(), "commands.txt")
	rows := make([]string, 0, 2)
	rows = append(rows,
		dockerInventoryFixture("stopped-api", strings.Repeat("d", 64), "stopped", "Exited (0)"),
		dockerInventoryFixture("running-api", strings.Repeat("e", 64), "running", "Up 1 minute"),
	)
	t.Setenv("FAKE_DOCKER_INVENTORY", strings.Join(rows, "\n"))
	t.Setenv("FAKE_DOCKER_COMMANDS", commands)

	probe, err := NewContainerProbe(ContainerProbeOptions{
		Secrets: dockerTestSecrets{}, Cipher: dockerTestCipher{}, SSHCommand: fakeSSH,
	})
	if err != nil {
		t.Fatalf("NewContainerProbe() error = %v", err)
	}
	containers, err := probe.ListContainers(context.Background(), projectapplication.ContainerProbeRequest{
		ProjectID: dockerTestProjectID, Host: "logs.example.invalid", Port: 22,
		User: "app", CredentialSecretID: dockerTestSecretID,
	})
	if err != nil {
		t.Fatalf("ListContainers() error = %v", err)
	}
	if len(containers) != 2 || containers[0].Name != "running-api" {
		t.Fatalf("containers = %#v", containers)
	}
	commandBytes, err := os.ReadFile(commands)
	if err != nil {
		t.Fatalf("read command log: %v", err)
	}
	if strings.TrimSpace(string(commandBytes)) != defaultContainerInventoryCommand {
		t.Fatalf("remote inventory command = %q, want %q", strings.TrimSpace(string(commandBytes)), defaultContainerInventoryCommand)
	}
	if strings.Contains(string(commandBytes), "exec") || strings.Contains(string(commandBytes), "--privileged") {
		t.Fatalf("inventory command contains a prohibited operation: %q", commandBytes)
	}
}

func TestReaderResolveDockerContainerUsesExactNameAndCurrentID(t *testing.T) {
	fakeSSH := writeDockerSSH(t)
	commands := filepath.Join(t.TempDir(), "commands.txt")
	t.Setenv("FAKE_DOCKER_COMMANDS", commands)
	reader := newDockerReader(t, fakeSSH, "checkout-api")

	firstID := strings.Repeat("1", 64)
	secondID := strings.Repeat("2", 64)
	t.Setenv("FAKE_DOCKER_PS", dockerInventoryFixture("checkout-api", firstID, "running", "Up 1 minute"))
	t.Setenv("FAKE_DOCKER_INSPECT", dockerInspectFixture(firstID, "checkout-api", "registry.example/checkout:v1", "running"))
	first, err := reader.ResolveDockerContainer(context.Background(), domain.EvidenceScope{
		ProjectID: dockerTestProjectID, SourceID: dockerTestSourceID,
	})
	if err != nil {
		t.Fatalf("first ResolveDockerContainer() error = %v", err)
	}
	if first.ID != firstID || first.Name != "checkout-api" {
		t.Fatalf("first identity = %#v", first)
	}

	t.Setenv("FAKE_DOCKER_PS", dockerInventoryFixture("checkout-api", secondID, "running", "Up 1 minute"))
	t.Setenv("FAKE_DOCKER_INSPECT", dockerInspectFixture(secondID, "checkout-api", "registry.example/checkout:v2", "running"))
	second, err := reader.ResolveDockerContainer(context.Background(), domain.EvidenceScope{
		ProjectID: dockerTestProjectID, SourceID: dockerTestSourceID,
	})
	if err != nil {
		t.Fatalf("recreated ResolveDockerContainer() error = %v", err)
	}
	if second.ID != secondID || second.Image != "registry.example/checkout:v2" {
		t.Fatalf("recreated identity = %#v", second)
	}

	commandBytes, err := os.ReadFile(commands)
	if err != nil {
		t.Fatalf("read command log: %v", err)
	}
	commandText := string(commandBytes)
	if strings.Count(commandText, "--filter name='^/checkout-api$'") != 2 {
		t.Fatalf("exact-name filter count in commands = %q", commandText)
	}
	if strings.Contains(commandText, "checkout-api-other") || strings.Contains(commandText, "docker exec") {
		t.Fatalf("unsafe or broad Docker command = %q", commandText)
	}
}

func TestReaderDockerMissingOrAmbiguousNameNeverFallsBack(t *testing.T) {
	tests := []struct {
		name       string
		ps         string
		want       string
		inspectHit bool
	}{
		{
			name: "missing exact name", ps: dockerInventoryFixture("checkout-api-old", strings.Repeat("3", 64), "running", "Up 1 minute"),
			want: "unavailable",
		},
		{
			name: "ambiguous exact name", ps: strings.Join([]string{
				dockerInventoryFixture("checkout-api", strings.Repeat("4", 64), "running", "Up 1 minute"),
				dockerInventoryFixture("checkout-api", strings.Repeat("5", 64), "stopped", "Exited (0)"),
			}, "\n"), want: "ambiguous",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commands := filepath.Join(t.TempDir(), "commands.txt")
			t.Setenv("FAKE_DOCKER_COMMANDS", commands)
			t.Setenv("FAKE_DOCKER_PS", tt.ps)
			t.Setenv("FAKE_DOCKER_INSPECT", dockerInspectFixture(strings.Repeat("6", 64), "checkout-api", "ignored", "running"))
			reader := newDockerReader(t, writeDockerSSH(t), "checkout-api")
			_, err := reader.ResolveDockerContainer(context.Background(), domain.EvidenceScope{
				ProjectID: dockerTestProjectID, SourceID: dockerTestSourceID,
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ResolveDockerContainer() error = %v, want %q", err, tt.want)
			}
			commandBytes, readErr := os.ReadFile(commands)
			if readErr != nil {
				t.Fatalf("read command log: %v", readErr)
			}
			commandText := string(commandBytes)
			if strings.Contains(commandText, "docker inspect") || strings.Contains(commandText, "docker logs") {
				t.Fatalf("missing/ambiguous container triggered fallback: %q", commandText)
			}
		})
	}
}

func TestReaderDockerLogsUseBoundedTimeTailAndBytes(t *testing.T) {
	fakeSSH := writeDockerSSH(t)
	commands := filepath.Join(t.TempDir(), "commands.txt")
	t.Setenv("FAKE_DOCKER_COMMANDS", commands)
	id := strings.Repeat("7", 64)
	t.Setenv("FAKE_DOCKER_PS", dockerInventoryFixture("checkout-api", id, "running", "Up 1 minute"))
	t.Setenv("FAKE_DOCKER_INSPECT", dockerInspectFixture(id, "checkout-api", "registry.example/checkout:v1", "running"))
	t.Setenv("FAKE_DOCKER_STDOUT", "panic: nil pointer\n")
	t.Setenv("FAKE_DOCKER_STDERR", "stack frame\n")
	t.Setenv("FAKE_DOCKER_WINDOW_LINES", "386130")
	reader := newDockerReader(t, fakeSSH, "checkout-api")
	start := time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC)
	end := start.Add(20 * time.Minute)
	query := domain.DockerLogQuery{
		Since: start.Add(-5 * time.Minute), Until: end.Add(5 * time.Minute), Tail: 42, MaxBytes: 1 << 20,
	}
	result, err := reader.ReadDockerLogs(context.Background(), domain.EvidenceScope{
		ProjectID: dockerTestProjectID, SourceID: dockerTestSourceID,
		TimeRange: domain.TimeRange{Start: start, End: end},
	}, query)
	if err != nil {
		t.Fatalf("ReadDockerLogs() error = %v", err)
	}
	if result.Container.ID != id || result.Stdout != "panic: nil pointer\n" || result.Stderr != "stack frame\n" || result.Truncated || result.WindowLines != 386130 || result.FilteredLines != 0 {
		t.Fatalf("Docker logs result = %#v", result)
	}
	commandBytes, err := os.ReadFile(commands)
	if err != nil {
		t.Fatalf("read command log: %v", err)
	}
	commandText := string(commandBytes)
	wantLogCommand := "docker logs --since '2026-08-24T06:55:00Z' --until '2026-08-24T07:25:00Z' --tail 42 '" + id + "'"
	if !strings.Contains(commandText, wantLogCommand) {
		t.Fatalf("bounded Docker logs command missing: %q", commandText)
	}
	for _, line := range strings.Split(strings.TrimSpace(commandText), "\n") {
		if strings.HasPrefix(line, "docker logs ") && strings.Contains(line, "'checkout-api'") {
			t.Fatalf("Docker logs used durable name instead of resolved ID: %q", line)
		}
	}
	t.Setenv("FAKE_DOCKER_STDOUT", strings.Repeat("x", 128))
	t.Setenv("FAKE_DOCKER_STDERR", "")
	bounded, err := reader.ReadDockerLogs(context.Background(), domain.EvidenceScope{
		ProjectID: dockerTestProjectID, SourceID: dockerTestSourceID,
		TimeRange: domain.TimeRange{Start: start, End: end},
	}, domain.DockerLogQuery{
		Since: query.Since, Until: query.Until, Tail: query.Tail, MaxBytes: 32,
	})
	if err != nil {
		t.Fatalf("bounded ReadDockerLogs() error = %v", err)
	}
	if !bounded.Truncated || bounded.BytesRetrieved != 32 || len(bounded.Stdout) != 32 {
		t.Fatalf("bounded Docker logs result = %#v", bounded)
	}
}

func TestReaderDockerLogsPatternFiltersBeforeTailAndReportsCoverage(t *testing.T) {
	fakeSSH := writeDockerSSH(t)
	commands := filepath.Join(t.TempDir(), "commands.txt")
	t.Setenv("FAKE_DOCKER_COMMANDS", commands)
	t.Setenv("FAKE_DOCKER_WINDOW_LINES", "386130")
	t.Setenv("FAKE_DOCKER_FILTERED_LINES", "1")
	t.Setenv("FAKE_DOCKER_STDOUT", "panic: nil pointer\ngoroutine 1 [running]:\nframe\n")
	t.Setenv("FAKE_DOCKER_STDERR", "")
	id := strings.Repeat("8", 64)
	t.Setenv("FAKE_DOCKER_PS", dockerInventoryFixture("checkout-api", id, "running", "Up 1 minute"))
	t.Setenv("FAKE_DOCKER_INSPECT", dockerInspectFixture(id, "checkout-api", "registry.example/checkout:v1", "running"))
	reader := newDockerReader(t, fakeSSH, "checkout-api")
	start := time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC)
	end := start.Add(20 * time.Minute)
	result, err := reader.ReadDockerLogs(context.Background(), domain.EvidenceScope{
		ProjectID: dockerTestProjectID, SourceID: dockerTestSourceID,
		TimeRange: domain.TimeRange{Start: start, End: end},
	}, domain.DockerLogQuery{
		Since: start.Add(-5 * time.Minute), Until: end.Add(5 * time.Minute), Tail: 42,
		MaxBytes: 1 << 20, Pattern: "panic.*nil pointer", ContextBefore: 1, ContextAfter: 2,
	})
	if err != nil {
		t.Fatalf("filtered ReadDockerLogs() error = %v", err)
	}
	if result.WindowLines != 386130 || result.FilteredLines != 1 || !strings.Contains(result.Stdout, "goroutine 1") {
		t.Fatalf("filtered Docker logs result = %#v", result)
	}
	commandBytes, err := os.ReadFile(commands)
	if err != nil {
		t.Fatalf("read command log: %v", err)
	}
	var filteredCommands []string
	for _, line := range strings.Split(strings.TrimSpace(string(commandBytes)), "\n") {
		if strings.Contains(line, "grep -E") {
			filteredCommands = append(filteredCommands, line)
		}
	}
	if len(filteredCommands) != 2 {
		t.Fatalf("filtered command count = %d, commands=%q", len(filteredCommands), commandBytes)
	}
	if !strings.Contains(filteredCommands[0], "grep -E -A 2 -B 1 -- 'panic.*nil pointer' | tail -42") {
		t.Fatalf("filtered read command = %q", filteredCommands[0])
	}
	for _, command := range filteredCommands {
		if strings.Contains(command, "--tail 42") || strings.Count(command, "'panic.*nil pointer'") != 1 {
			t.Fatalf("filtered command applied tail before grep or quoted pattern incorrectly: %q", command)
		}
	}
}

func TestReaderDockerLogsUsesFilterForLargeEarlyWindow(t *testing.T) {
	const fixtureLines = 386130

	fixturePath := filepath.Join(t.TempDir(), "docker.log")
	fixture, err := os.Create(fixturePath)
	if err != nil {
		t.Fatalf("create Docker fixture: %v", err)
	}
	for index := 0; index < fixtureLines; index++ {
		switch index {
		case 100:
			_, err = fmt.Fprintln(fixture, "panic TriggerNilPointerFault")
		case 101:
			_, err = fmt.Fprintln(fixture, "goroutine 1 [running]:")
		case 102:
			_, err = fmt.Fprintln(fixture, "runtime frame")
		default:
			_, err = fmt.Fprintf(fixture, "noise-%d\n", index)
		}
		if err != nil {
			_ = fixture.Close()
			t.Fatalf("write Docker fixture line %d: %v", index, err)
		}
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("close Docker fixture: %v", err)
	}

	fakeSSH, dockerBin := writeExecutingDockerSSH(t)
	id := strings.Repeat("a", 64)
	t.Setenv("FAKE_DOCKER_BIN_DIR", dockerBin)
	t.Setenv("FAKE_DOCKER_FIXTURE", fixturePath)
	t.Setenv("FAKE_DOCKER_ID", id)
	reader := newDockerReader(t, fakeSSH, "checkout-api")
	start := time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC)
	end := start.Add(20 * time.Minute)
	scope := domain.EvidenceScope{
		ProjectID: dockerTestProjectID, SourceID: dockerTestSourceID,
		TimeRange: domain.TimeRange{Start: start, End: end},
	}
	query := domain.DockerLogQuery{
		Since: start.Add(-5 * time.Minute), Until: end.Add(5 * time.Minute), Tail: 10, MaxBytes: 1 << 20,
	}

	unfiltered, err := reader.ReadDockerLogs(context.Background(), scope, query)
	if err != nil {
		t.Fatalf("unfiltered ReadDockerLogs() error = %v", err)
	}
	if strings.Contains(unfiltered.Stdout, "TriggerNilPointerFault") || unfiltered.WindowLines != fixtureLines || unfiltered.Truncated {
		t.Fatalf("unfiltered Docker logs = %#v, want only the fixture tail", unfiltered)
	}

	filtered, err := reader.ReadDockerLogs(context.Background(), scope, domain.DockerLogQuery{
		Since: start.Add(-5 * time.Minute), Until: end.Add(5 * time.Minute), Tail: 10, MaxBytes: 1 << 20,
		Pattern: "TriggerNilPointerFault", ContextAfter: 2,
	})
	if err != nil {
		t.Fatalf("filtered ReadDockerLogs() error = %v", err)
	}
	if filtered.Truncated || len(filtered.Stdout) > 1<<20 || filtered.WindowLines != fixtureLines || filtered.FilteredLines != 1 ||
		!strings.Contains(filtered.Stdout, "panic TriggerNilPointerFault") ||
		!strings.Contains(filtered.Stdout, "goroutine 1 [running]:") ||
		!strings.Contains(filtered.Stdout, "runtime frame") {
		t.Fatalf("filtered Docker logs = %#v, want early panic and context within the byte cap", filtered)
	}
}

func TestDockerLogsCommandOmitsZeroContextFlags(t *testing.T) {
	start := time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC)
	query := domain.DockerLogQuery{
		Since: start, Until: start.Add(time.Minute), Tail: 2000, Pattern: "panic|fatal",
	}
	command := dockerLogsCommand(strings.Repeat("9", 64), query)
	if strings.Contains(command, "-A ") || strings.Contains(command, "-B ") {
		t.Fatalf("zero context command contains context flags: %q", command)
	}
	if !strings.Contains(command, "grep -E -- 'panic|fatal' | tail -2000") {
		t.Fatalf("zero context command = %q", command)
	}
}

func TestValidateDockerLogQueryRejectsOutOfBoundsRequests(t *testing.T) {
	start := time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC)
	end := start.Add(20 * time.Minute)
	scope := domain.EvidenceScope{TimeRange: domain.TimeRange{Start: start, End: end}}
	cases := []struct {
		name  string
		query domain.DockerLogQuery
	}{
		{name: "tail", query: domain.DockerLogQuery{Since: start, Until: end, Tail: maxDockerRuntimeLines + 1, MaxBytes: 1}},
		{name: "bytes", query: domain.DockerLogQuery{Since: start, Until: end, Tail: 1, MaxBytes: maxDockerRuntimeBytes + 1}},
		{name: "window padding", query: domain.DockerLogQuery{Since: start.Add(-16 * time.Minute), Until: end, Tail: 1, MaxBytes: 1}},
		{name: "window width", query: domain.DockerLogQuery{Since: start.Add(-15 * time.Minute), Until: end.Add(16 * time.Minute), Tail: 1, MaxBytes: 1}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateDockerLogQuery(scope, tt.query); err == nil {
				t.Fatal("validateDockerLogQuery() error = nil")
			}
		})
	}
}

func newDockerReader(t *testing.T, command, containerName string) *Reader {
	t.Helper()
	reader, err := NewReader(Options{
		Sources: dockerTestSource{cfg: SourceConfig{
			ProjectID: dockerTestProjectID, SourceID: dockerTestSourceID,
			Host: "logs.example.invalid", Port: 22, User: "app",
			ProjectFolder: "/srv/app", LogPath: "/var/log/app.log", Mode: "tail",
			Deployment:         projectdomain.SSHDeployment{Kind: projectdomain.SSHDeploymentDocker, ContainerName: containerName},
			CredentialSecretID: dockerTestSecretID,
		}},
		Secrets: dockerTestSecrets{}, Cipher: dockerTestCipher{}, SSHCommand: command,
	})
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	return reader
}

func dockerInventoryFixture(name, id, state, status string) string {
	raw, err := json.Marshal(map[string]string{
		"ID": id, "Names": name, "Image": "registry.example/app:v1", "State": state, "Status": status,
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func dockerInspectFixture(id, name, image, state string) string {
	raw, err := json.Marshal(map[string]string{
		"id": id, "name": "/" + name, "image": image, "state": state, "status": "Up 1 minute",
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func writeExecutingDockerSSH(t *testing.T) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake ssh script uses a POSIX shell")
	}
	directory := t.TempDir()
	dockerPath := filepath.Join(directory, "docker")
	dockerScript := `#!/bin/sh
case "$1" in
  ps)
    printf '{"ID":"%s","Names":"checkout-api","Image":"registry.example/checkout:v1","State":"running","Status":"Up 1 minute"}\n' "$FAKE_DOCKER_ID"
    ;;
  inspect)
    printf '{"id":"%s","name":"/checkout-api","image":"registry.example/checkout:v1","state":"running","status":"Up 1 minute"}\n' "$FAKE_DOCKER_ID"
    ;;
  logs)
    tail_lines=
    previous=
    for argument in "$@"; do
      if [ "$previous" = "--tail" ]; then
        tail_lines=$argument
      fi
      previous=$argument
    done
    if [ -n "$tail_lines" ]; then
      exec tail -n "$tail_lines" "$FAKE_DOCKER_FIXTURE"
    fi
    exec cat "$FAKE_DOCKER_FIXTURE"
    ;;
  *)
    exit 97
    ;;
esac
`
	if err := os.WriteFile(dockerPath, []byte(dockerScript), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}

	sshPath := filepath.Join(directory, "fake-ssh")
	sshScript := `#!/bin/sh
remote=
for argument in "$@"; do
  remote=$argument
done
PATH="$FAKE_DOCKER_BIN_DIR:$PATH" sh -c "$remote"
`
	if err := os.WriteFile(sshPath, []byte(sshScript), 0o755); err != nil {
		t.Fatalf("write executing fake ssh: %v", err)
	}
	return sshPath, directory
}

func writeDockerSSH(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake ssh script uses a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "fake-ssh")
	script := `#!/bin/sh
eval "remote=\${$#}"
if [ -n "${FAKE_DOCKER_COMMANDS:-}" ]; then
  printf '%s\n' "$remote" >> "$FAKE_DOCKER_COMMANDS"
fi
case "$remote" in
  *"docker ps -a --no-trunc --format"*) printf '%s\n' "${FAKE_DOCKER_INVENTORY:-}" ;;
  *"docker ps -a --no-trunc --filter"*) printf '%s\n' "${FAKE_DOCKER_PS:-}" ;;
  *"docker inspect --type=container"*) printf '%s\n' "${FAKE_DOCKER_INSPECT:-}" ;;
  *"docker logs --since"*"grep -E"*"wc -l"*) printf '%s\n' "${FAKE_DOCKER_FILTERED_LINES:-0}" ;;
  *"docker logs --since"*"wc -l"*) printf '%s\n' "${FAKE_DOCKER_WINDOW_LINES:-0}" ;;
  *"docker logs --since"*) printf '%s' "${FAKE_DOCKER_STDOUT:-}"; printf '%s' "${FAKE_DOCKER_STDERR:-}" >&2 ;;
  *) printf 'unexpected remote command: %s\n' "$remote" >&2; exit 97 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
