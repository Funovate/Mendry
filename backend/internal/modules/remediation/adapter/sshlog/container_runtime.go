package sshlog

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	projectdomain "mendry/backend/internal/modules/projects/domain"
	"mendry/backend/internal/modules/remediation/domain"
)

const (
	maxDockerRuntimeBytes         = 1 << 20
	maxDockerRuntimeLines         = 2000
	dockerCommandTimeout          = 2 * time.Minute
	maxDockerRuntimeWindowPadding = 15 * time.Minute
	maxDockerRuntimeWindow        = 30 * time.Minute
)

var dockerContainerIDPattern = regexp.MustCompile(`^[A-Fa-f0-9]{12,64}$`)

var _ domain.DockerEvidencePort = (*Reader)(nil)

// ResolveDockerContainer 通过保存的 exact name 重新解析当前容器 ID；不使用旧 ID 或相似名称。
func (r *Reader) ResolveDockerContainer(ctx context.Context, scope domain.EvidenceScope) (domain.DockerContainerIdentity, error) {
	cfg, closer, err := r.openDocker(ctx, scope)
	if err != nil {
		return domain.DockerContainerIdentity{}, err
	}
	defer closer()
	return r.resolveDockerContainer(ctx, cfg)
}

// ReadDockerLogs 先重新解析 exact name，再读取有界 stdout/stderr。
// Docker CLI 只通过固定的 ps/inspect/logs 命令到达远端，不能执行任意容器操作。
func (r *Reader) ReadDockerLogs(ctx context.Context, scope domain.EvidenceScope, query domain.DockerLogQuery) (domain.DockerLogResult, error) {
	if err := validateDockerLogQuery(scope, query); err != nil {
		return domain.DockerLogResult{}, err
	}
	cfg, closer, err := r.openDocker(ctx, scope)
	if err != nil {
		return domain.DockerLogResult{}, err
	}
	defer closer()
	container, err := r.resolveDockerContainer(ctx, cfg)
	if err != nil {
		return domain.DockerLogResult{}, err
	}
	if result, ok := r.readDockerJSONFileLogs(ctx, cfg, container, query); ok {
		return result, nil
	}
	remoteCommand := dockerLogsCommand(container.ID, query)
	stdout, stderr, truncated, err := r.runDockerCommand(ctx, cfg, remoteCommand, int(query.MaxBytes))
	if err != nil {
		return domain.DockerLogResult{}, err
	}
	returnedLines := countRuntimeLogLines(stdout) + countRuntimeLogLines(stderr)
	coverageLimited := truncated || returnedLines >= int64(query.Tail)
	return domain.DockerLogResult{
		Container: container, Stdout: stdout, Stderr: stderr,
		Truncated: truncated, CoverageLimited: coverageLimited,
		RefinementRequired: coverageLimited, CoverageReason: dockerCoverageReason(truncated, coverageLimited),
		BytesRetrieved: int64(len(stdout) + len(stderr)), WindowLines: -1,
		FilteredLines: countRuntimePatternMatches(stdout+stderr, query.Pattern),
		Query: domain.DockerLogQueryMeta{
			Since: query.Since, Until: query.Until, Tail: query.Tail, Pattern: query.Pattern,
		},
	}, nil
}

type dockerJSONLogLocation struct {
	ID     string `json:"id"`
	Path   string `json:"logPath"`
	Driver string `json:"logDriver"`
}

type dockerJSONLogLine struct {
	Log    string    `json:"log"`
	Stream string    `json:"stream"`
	Time   time.Time `json:"time"`
}

// readDockerJSONFileLogs 对 json-file 使用一次固定 grep 扫描，绕过 Docker
// daemon 对超大日志执行的多次顺序读取。路径只接受当前容器 inspect 返回且与
// exact container ID 匹配的绝对 json-file 路径。
func (r *Reader) readDockerJSONFileLogs(
	ctx context.Context,
	cfg SourceConfig,
	container domain.DockerContainerIdentity,
	query domain.DockerLogQuery,
) (domain.DockerLogResult, bool) {
	location, err := r.resolveDockerJSONLogLocation(ctx, cfg, container.ID)
	if err != nil || location.Driver != "json-file" || !validDockerJSONLogPath(location.Path, container.ID) {
		return domain.DockerLogResult{}, false
	}
	raw, diagnostics, truncated, err := r.runDockerCommand(
		ctx, cfg, dockerJSONLogsCommand(location.Path, query), int(query.MaxBytes),
	)
	if err != nil || strings.TrimSpace(diagnostics) != "" {
		return domain.DockerLogResult{}, false
	}
	stdout, stderr, _, filteredLines, tailLimited, err := decodeDockerJSONLogOutput(raw, query, truncated)
	if err != nil {
		return domain.DockerLogResult{}, false
	}
	coverageLimited := truncated || tailLimited
	return domain.DockerLogResult{
		Container: container, Stdout: stdout, Stderr: stderr,
		Truncated: truncated, CoverageLimited: coverageLimited,
		RefinementRequired: coverageLimited, CoverageReason: dockerCoverageReason(truncated, tailLimited),
		BytesRetrieved: int64(len(stdout) + len(stderr)), WindowLines: -1, FilteredLines: filteredLines,
		Query: domain.DockerLogQueryMeta{
			Since: query.Since, Until: query.Until, Tail: query.Tail, Pattern: query.Pattern,
		},
	}, true
}

func (r *Reader) resolveDockerJSONLogLocation(ctx context.Context, cfg SourceConfig, containerID string) (dockerJSONLogLocation, error) {
	format := `{"id":{{json .Id}},"logPath":{{json .LogPath}},"logDriver":{{json .HostConfig.LogConfig.Type}}}`
	remoteCommand := "docker inspect --type=container --format " + shellQuote(format) + " " + shellQuote(containerID)
	stdout, _, truncated, err := r.runDockerCommand(ctx, cfg, remoteCommand, maxDockerRuntimeBytes)
	if err != nil {
		return dockerJSONLogLocation{}, err
	}
	if truncated {
		return dockerJSONLogLocation{}, fmt.Errorf("Docker log location output exceeded limit")
	}
	var location dockerJSONLogLocation
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &location); err != nil || location.ID != containerID {
		return dockerJSONLogLocation{}, fmt.Errorf("Docker log location is invalid")
	}
	return location, nil
}

func validDockerJSONLogPath(value, containerID string) bool {
	clean := path.Clean(strings.TrimSpace(value))
	if clean == "." || !path.IsAbs(clean) || strings.ContainsAny(clean, "\r\n") {
		return false
	}
	return path.Base(clean) == containerID+"-json.log" && path.Base(path.Dir(clean)) == containerID
}

func dockerJSONLogsCommand(logPath string, query domain.DockerLogQuery) string {
	command := "grep -F"
	for _, prefix := range dockerLogMinutePrefixes(query.Since, query.Until) {
		command += " -e " + shellQuote(`"time":"`+prefix)
	}
	command += " -- " + shellQuote(logPath)
	if query.Pattern != "" {
		command += " | " + dockerGrepCommand(query)
	}
	return command + " | tail -" + strconv.Itoa(query.Tail+1)
}

func dockerLogMinutePrefixes(since, until time.Time) []string {
	cursor := since.UTC().Truncate(time.Minute)
	last := until.UTC().Truncate(time.Minute)
	prefixes := make([]string, 0, int(last.Sub(cursor)/time.Minute)+1)
	for !cursor.After(last) {
		prefixes = append(prefixes, cursor.Format("2006-01-02T15:04:"))
		cursor = cursor.Add(time.Minute)
	}
	return prefixes
}

func decodeDockerJSONLogOutput(raw string, query domain.DockerLogQuery, truncated bool) (string, string, int64, int64, bool, error) {
	rows := make([]dockerJSONLogLine, 0, query.Tail+1)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "--" {
			continue
		}
		var row dockerJSONLogLine
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			if truncated {
				continue
			}
			return "", "", 0, 0, false, fmt.Errorf("Docker JSON log output is invalid")
		}
		if row.Time.Before(query.Since) || !row.Time.Before(query.Until) {
			continue
		}
		rows = append(rows, row)
	}
	tailLimited := len(rows) > query.Tail
	if tailLimited {
		rows = rows[len(rows)-query.Tail:]
	}
	var stdout, stderr strings.Builder
	filteredLines := int64(0)
	matcher, _ := regexp.Compile(query.Pattern)
	for _, row := range rows {
		if matcher != nil && matcher.MatchString(row.Log) {
			filteredLines++
		}
		if row.Stream == "stderr" {
			stderr.WriteString(row.Log)
		} else {
			stdout.WriteString(row.Log)
		}
	}
	return stdout.String(), stderr.String(), int64(len(rows)), filteredLines, tailLimited, nil
}

func dockerCoverageReason(byteLimited, tailLimited bool) string {
	switch {
	case byteLimited:
		return "byte_limit"
	case tailLimited:
		return "tail_limit"
	default:
		return ""
	}
}

func countRuntimeLogLines(output string) int64 {
	if output == "" {
		return 0
	}
	count := int64(strings.Count(output, "\n"))
	if !strings.HasSuffix(output, "\n") {
		count++
	}
	return count
}

func countRuntimePatternMatches(output, pattern string) int64 {
	if pattern == "" {
		return 0
	}
	matcher, err := regexp.Compile(pattern)
	if err != nil {
		return 0
	}
	var count int64
	for _, line := range strings.Split(output, "\n") {
		if matcher.MatchString(line) {
			count++
		}
	}
	return count
}

func dockerLogsCommand(containerID string, query domain.DockerLogQuery) string {
	base := "docker logs --since " + shellQuote(query.Since.UTC().Format(time.RFC3339Nano)) +
		" --until " + shellQuote(query.Until.UTC().Format(time.RFC3339Nano))
	if query.Pattern == "" {
		// 无 filter 时保持旧命令的字节级兼容。
		return base + " --tail " + fmt.Sprintf("%d", query.Tail) + " " + shellQuote(containerID)
	}
	return base + " " + shellQuote(containerID) + " 2>&1 | " + dockerGrepCommand(query) +
		" | tail -" + strconv.Itoa(query.Tail)
}

func dockerGrepCommand(query domain.DockerLogQuery) string {
	grepCommand := "grep -E"
	if query.ContextAfter > 0 {
		grepCommand += " -A " + strconv.Itoa(query.ContextAfter)
	}
	if query.ContextBefore > 0 {
		grepCommand += " -B " + strconv.Itoa(query.ContextBefore)
	}
	return grepCommand + " -- " + shellQuote(query.Pattern)
}

func (r *Reader) openDocker(ctx context.Context, scope domain.EvidenceScope) (SourceConfig, func(), error) {
	cfg, closer, err := r.open(ctx, scope)
	if err != nil {
		return SourceConfig{}, nil, err
	}
	if cfg.Deployment.Kind != projectdomain.SSHDeploymentDocker || strings.TrimSpace(cfg.Deployment.ContainerName) == "" {
		closer()
		return SourceConfig{}, nil, fmt.Errorf("SSH source is not a configured Docker deployment")
	}
	return cfg, closer, nil
}

func (r *Reader) resolveDockerContainer(ctx context.Context, cfg SourceConfig) (domain.DockerContainerIdentity, error) {
	configuredName := strings.TrimSpace(cfg.Deployment.ContainerName)
	filter := "^/" + regexp.QuoteMeta(configuredName) + "$"
	remoteCommand := "docker ps -a --no-trunc --filter name=" + shellQuote(filter) + " --format '{{json .}}'"
	stdout, _, truncated, err := r.runDockerCommand(ctx, cfg, remoteCommand, maxDockerRuntimeBytes)
	if err != nil {
		return domain.DockerContainerIdentity{}, err
	}
	if truncated {
		return domain.DockerContainerIdentity{}, fmt.Errorf("Docker container identity output exceeded limit")
	}
	rows := splitNonEmpty(stdout)
	matches := make([]projectdomain.DockerContainer, 0, len(rows))
	for _, line := range rows {
		row, decodeErr := decodeDockerInventoryRow(line)
		if decodeErr != nil {
			continue
		}
		name := strings.TrimPrefix(strings.TrimSpace(row.Name), "/")
		if name != configuredName || strings.TrimSpace(row.ID) == "" {
			continue
		}
		matches = append(matches, projectdomain.DockerContainer{
			Name: name, ID: strings.TrimSpace(row.ID), Image: strings.TrimSpace(row.Image),
			State: normalizeDockerState(row.State, row.Status), Status: strings.TrimSpace(row.Status),
		})
	}
	if len(matches) == 0 {
		return domain.DockerContainerIdentity{}, fmt.Errorf("configured Docker container is unavailable")
	}
	if len(matches) != 1 || !dockerContainerIDPattern.MatchString(matches[0].ID) {
		return domain.DockerContainerIdentity{}, fmt.Errorf("configured Docker container identity is ambiguous")
	}
	return r.inspectDockerContainer(ctx, cfg, matches[0])
}

func (r *Reader) inspectDockerContainer(ctx context.Context, cfg SourceConfig, listed projectdomain.DockerContainer) (domain.DockerContainerIdentity, error) {
	format := `{"id":{{json .Id}},"name":{{json .Name}},"image":{{json .Config.Image}},"state":{{json .State.Status}},"running":{{json .State.Running}},"restarting":{{json .State.Restarting}}}`
	remoteCommand := "docker inspect --type=container --format " + shellQuote(format) + " " + shellQuote(listed.ID)
	stdout, _, truncated, err := r.runDockerCommand(ctx, cfg, remoteCommand, maxDockerRuntimeBytes)
	if err != nil {
		return domain.DockerContainerIdentity{}, err
	}
	if truncated {
		return domain.DockerContainerIdentity{}, fmt.Errorf("Docker container inspect output exceeded limit")
	}
	identity, err := decodeDockerInspect(stdout, listed)
	if err != nil {
		return domain.DockerContainerIdentity{}, err
	}
	if identity.Name != listed.Name || identity.ID != listed.ID {
		return domain.DockerContainerIdentity{}, fmt.Errorf("Docker container identity changed during resolution")
	}
	return identity, nil
}

func (r *Reader) runDockerCommand(ctx context.Context, cfg SourceConfig, remoteCommand string, limit int) (string, string, bool, error) {
	if limit <= 0 || limit > maxDockerRuntimeBytes {
		limit = maxDockerRuntimeBytes
	}
	args, cleanup, err := r.sshArgs(cfg, remoteCommand)
	if err != nil {
		return "", "", false, fmt.Errorf("prepare Docker SSH command: %w", err)
	}
	defer cleanup()
	stdout, stderr, truncated, err := runBoundedSSHCommand(ctx, r.command, args, dockerCommandTimeout, limit)
	if err != nil {
		return "", "", false, fmt.Errorf("Docker read failed: %w", err)
	}
	return stdout, stderr, truncated, nil
}

func validateDockerLogQuery(scope domain.EvidenceScope, query domain.DockerLogQuery) error {
	if query.Since.IsZero() || query.Until.IsZero() || !query.Since.Before(query.Until) {
		return fmt.Errorf("Docker log time range is invalid")
	}
	if query.Tail < 1 || query.Tail > maxDockerRuntimeLines {
		return fmt.Errorf("Docker log tail is out of bounds")
	}
	if query.MaxBytes < 1 || query.MaxBytes > maxDockerRuntimeBytes {
		return fmt.Errorf("Docker log byte bound is invalid")
	}
	if !scope.TimeRange.Start.IsZero() && !scope.TimeRange.End.IsZero() {
		if !scope.TimeRange.Start.Before(scope.TimeRange.End) || query.Since.Before(scope.TimeRange.Start.Add(-maxDockerRuntimeWindowPadding)) || query.Until.After(scope.TimeRange.End.Add(maxDockerRuntimeWindowPadding)) {
			return fmt.Errorf("Docker log time range exceeds incident bound")
		}
	}
	if query.Until.Sub(query.Since) > maxDockerRuntimeWindow {
		return fmt.Errorf("Docker log time range is too wide")
	}
	return nil
}

func decodeDockerInspect(raw string, listed projectdomain.DockerContainer) (domain.DockerContainerIdentity, error) {
	line := strings.TrimSpace(strings.SplitN(raw, "\n", 2)[0])
	var value map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &value); err != nil {
		return domain.DockerContainerIdentity{}, fmt.Errorf("Docker inspect response is invalid")
	}
	id := dockerJSONField(value, "id", "Id", "ID")
	name := strings.TrimPrefix(strings.TrimSpace(dockerJSONField(value, "name", "Name", "Names")), "/")
	image := dockerJSONField(value, "image", "Image")
	state := dockerJSONField(value, "state", "State")
	status := dockerJSONField(value, "status", "Status")
	if state == "" {
		if nested, ok := value["State"]; ok {
			var stateObject map[string]json.RawMessage
			if json.Unmarshal(nested, &stateObject) == nil {
				state = dockerJSONField(stateObject, "Status", "status")
				if state == "" && dockerJSONBoolField(stateObject, "Restarting", "restarting") {
					state = "restarting"
				} else if state == "" && dockerJSONBoolField(stateObject, "Running", "running") {
					state = "running"
				}
			}
		}
	}
	if image == "" {
		if nested, ok := value["Config"]; ok {
			var config map[string]json.RawMessage
			if json.Unmarshal(nested, &config) == nil {
				image = dockerJSONField(config, "Image", "image")
			}
		}
	}
	if id == "" {
		id = listed.ID
	}
	if name == "" {
		name = listed.Name
	}
	if image == "" {
		image = listed.Image
	}
	if state == "" {
		state = listed.State
	}
	if status == "" {
		status = listed.Status
	}
	if !dockerContainerIDPattern.MatchString(id) || name == "" {
		return domain.DockerContainerIdentity{}, fmt.Errorf("Docker inspect identity is invalid")
	}
	return domain.DockerContainerIdentity{
		Name: name, ID: id, Image: image, State: normalizeDockerState(state, status), Status: status,
	}, nil
}

func dockerJSONBoolField(values map[string]json.RawMessage, keys ...string) bool {
	for _, key := range keys {
		for actual, raw := range values {
			if strings.EqualFold(actual, key) {
				var value bool
				if json.Unmarshal(raw, &value) == nil {
					return value
				}
			}
		}
	}
	return false
}
