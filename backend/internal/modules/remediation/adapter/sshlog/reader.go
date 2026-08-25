package sshlog

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	projectapplication "fixthe/backend/internal/modules/projects/application"
	projectdomain "fixthe/backend/internal/modules/projects/domain"
	"fixthe/backend/internal/modules/remediation/domain"
	"fixthe/backend/internal/platform/observability"
)

const (
	defaultSSHCommand   = "ssh"
	defaultTimeout      = 15 * time.Second
	defaultMaxLines     = 500
	defaultMaxBytes     = 1 << 20
	defaultWindow       = 20
	defaultInspectBytes = 64 << 10
)

// SourceConfig 是适配器读取 SSH 日志所需的无凭据配置。
type SourceConfig struct {
	ProjectID          string
	SourceID           string
	Host               string
	Port               int
	User               string
	ProjectFolder      string
	LogPath            string
	Mode               string
	Deployment         projectdomain.SSHDeployment
	CredentialSecretID string
	CredentialKind     projectdomain.SecretKind
	plaintext          []byte
}

// SourceLoader 按项目/来源解析 SSH 日志配置。
type SourceLoader interface {
	LoadSSHSource(ctx context.Context, projectID, sourceID string) (SourceConfig, error)
}

// SecretLoader 加载项目加密凭据。
type SecretLoader interface {
	GetEncryptedSecret(ctx context.Context, projectID, secretID string) (projectdomain.EncryptedSecret, error)
}

// Options 构造 SSH 日志适配器。SSHCommand 默认 "ssh"，测试可注入本地 stub。
type Options struct {
	Sources        SourceLoader
	Secrets        SecretLoader
	Cipher         projectapplication.Cipher
	SSHCommand     string
	CommandTimeout time.Duration
	Logger         *slog.Logger
}

// Reader 通过 SSH 读取有界、脱敏的日志窗口。
type Reader struct {
	sources SourceLoader
	secrets SecretLoader
	cipher  projectapplication.Cipher
	command string
	timeout time.Duration
	logger  *slog.Logger
}

// NewReader 验证依赖并构造 SSH 日志适配器。
func NewReader(options Options) (*Reader, error) {
	if options.Sources == nil || options.Secrets == nil || options.Cipher == nil {
		return nil, fmt.Errorf("ssh log reader dependencies are required")
	}
	command := strings.TrimSpace(options.SSHCommand)
	if command == "" {
		command = defaultSSHCommand
	}
	timeout := options.CommandTimeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Reader{
		sources: options.Sources,
		secrets: options.Secrets,
		cipher:  options.Cipher,
		command: command,
		timeout: timeout,
		logger:  options.Logger,
	}, nil
}

var (
	_ domain.EvidenceLogPort = (*Reader)(nil)
	_ domain.SSHInspectPort  = (*Reader)(nil)
)

// Search 读取有界日志尾部并按关键字过滤。
func (r *Reader) Search(ctx context.Context, scope domain.EvidenceScope, query domain.LogQuery) (domain.EvidencePage, error) {
	started := time.Now()
	cfg, closer, err := r.open(ctx, scope)
	if err != nil {
		r.logRequest(ctx, "search", cfg, "", started, 0, err)
		return domain.EvidencePage{}, err
	}
	defer closer()

	maxLines := query.MaxLines
	if maxLines <= 0 {
		maxLines = defaultMaxLines
	}
	maxBytes := query.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	command := remoteReadCommand(cfg.LogPath, maxLines)
	raw, err := r.exec(ctx, cfg, command)
	if err != nil {
		r.logRequest(ctx, "search", cfg, command, started, 0, err)
		return domain.EvidencePage{}, err
	}
	page := pageFromRaw(scope, raw, query, maxLines, maxBytes, "")
	r.logRequest(ctx, "search", cfg, command, started, len(raw), nil)
	return page, nil
}

// GetContext 返回锚点附近的有界窗口；未知 ID 返回空页。
func (r *Reader) GetContext(ctx context.Context, scope domain.EvidenceScope, anchor domain.EvidenceAnchor) (domain.EvidencePage, error) {
	if strings.TrimSpace(anchor.EvidenceID) == "" {
		return domain.EvidencePage{}, nil
	}
	started := time.Now()
	cfg, closer, err := r.open(ctx, scope)
	if err != nil {
		r.logRequest(ctx, "get_context", cfg, "", started, 0, err)
		return domain.EvidencePage{}, err
	}
	defer closer()

	before := anchor.LinesBefore
	after := anchor.LinesAfter
	if before <= 0 {
		before = defaultWindow
	}
	if after <= 0 {
		after = defaultWindow
	}
	window := before + after + 1
	command := remoteReadCommand(cfg.LogPath, defaultMaxLines)
	raw, err := r.exec(ctx, cfg, command)
	if err != nil {
		r.logRequest(ctx, "get_context", cfg, command, started, 0, err)
		return domain.EvidencePage{}, err
	}
	page := pageFromRaw(scope, raw, domain.LogQuery{}, defaultMaxLines, defaultMaxBytes, "")
	index := -1
	for i, line := range page.Lines {
		if line.EvidenceID == anchor.EvidenceID {
			index = i
			break
		}
	}
	if index < 0 {
		r.logRequest(ctx, "get_context", cfg, command, started, len(raw), nil)
		return domain.EvidencePage{}, nil
	}
	start := index - before
	if start < 0 {
		start = 0
	}
	end := index + after + 1
	if end > len(page.Lines) {
		end = len(page.Lines)
	}
	if end-start > window {
		end = start + window
	}
	r.logRequest(ctx, "get_context", cfg, command, started, len(raw), nil)
	return domain.EvidencePage{Lines: page.Lines[start:end]}, nil
}

func (r *Reader) open(ctx context.Context, scope domain.EvidenceScope) (SourceConfig, func(), error) {
	if strings.TrimSpace(scope.ProjectID) == "" {
		return SourceConfig{}, nil, fmt.Errorf("project id is required")
	}
	cfg, err := r.sources.LoadSSHSource(ctx, scope.ProjectID, scope.SourceID)
	if err != nil {
		return SourceConfig{}, nil, fmt.Errorf("load ssh source: %w", err)
	}
	if strings.TrimSpace(cfg.CredentialSecretID) == "" {
		return cfg, nil, fmt.Errorf("ssh source credential is required")
	}
	encrypted, err := r.secrets.GetEncryptedSecret(ctx, scope.ProjectID, cfg.CredentialSecretID)
	if err != nil {
		return cfg, nil, fmt.Errorf("load ssh credential: %w", err)
	}
	cfg.CredentialKind = encrypted.Kind
	plaintext, err := r.cipher.Decrypt(encrypted.ProjectID, encrypted.ID, encrypted.Kind, encrypted.Ciphertext, encrypted.Nonce)
	if err != nil {
		return cfg, nil, fmt.Errorf("decrypt ssh credential: %w", err)
	}
	cfg.plaintext = plaintext
	return cfg, func() { clearBytes(plaintext) }, nil
}

// Inspect 执行 gateway 已经解析并重写过的 inspect 命令。模型原始字符串不得
// 到达这里；适配器只负责凭据注入、cwd、超时和 64KiB 输出上限。
func (r *Reader) Inspect(ctx context.Context, scope domain.EvidenceScope, req domain.SSHInspectRequest) (domain.SSHInspectResult, error) {
	started := time.Now()
	if strings.TrimSpace(req.Command) == "" {
		return domain.SSHInspectResult{}, fmt.Errorf("inspect command is required")
	}
	cfg, closer, err := r.open(ctx, scope)
	if err != nil {
		r.logRequest(ctx, "inspect", cfg, "", started, 0, err)
		return domain.SSHInspectResult{}, err
	}
	defer closer()

	remoteCommand := inspectRemoteCommand(cfg.ProjectFolder, req.Command)
	result, runErr := r.execInspect(ctx, cfg, remoteCommand)
	r.logRequest(ctx, "inspect", cfg, remoteCommand, started, int(result.BytesRetrieved), inspectLogError(runErr, result))
	if runErr != nil {
		return domain.SSHInspectResult{}, runErr
	}
	return result, nil
}

func inspectRemoteCommand(projectFolder, command string) string {
	folder := strings.TrimSpace(projectFolder)
	if folder == "" {
		folder = "."
	}
	return "cd -- " + shellQuote(folder) + " && " + command
}

func inspectLogError(runErr error, result domain.SSHInspectResult) error {
	if runErr != nil {
		return runErr
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("ssh inspect completed with exit %d", result.ExitCode)
	}
	return nil
}

type inspectCapture struct {
	mu        sync.Mutex
	limit     int
	truncated bool
	stdout    bytes.Buffer
	stderr    bytes.Buffer
	abort     func()
}

func (c *inspectCapture) used() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usedLocked()
}

func (c *inspectCapture) write(dest *bytes.Buffer, p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.truncated {
		return len(p), nil
	}
	remaining := c.limit - c.usedLocked()
	if remaining <= 0 {
		c.truncated = true
		if c.abort != nil {
			c.abort()
		}
		return len(p), nil
	}
	if len(p) > remaining {
		dest.Write(p[:remaining])
		c.truncated = true
		if c.abort != nil {
			c.abort()
		}
		return len(p), nil
	}
	return dest.Write(p)
}

func (c *inspectCapture) usedLocked() int {
	return c.stdout.Len() + c.stderr.Len()
}

type inspectStreamWriter struct {
	capture *inspectCapture
	dest    *bytes.Buffer
}

func (w inspectStreamWriter) Write(p []byte) (int, error) {
	return w.capture.write(w.dest, p)
}

func (r *Reader) execInspect(ctx context.Context, cfg SourceConfig, remoteCommand string) (domain.SSHInspectResult, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	args, cleanup, err := r.sshArgs(cfg, remoteCommand)
	if err != nil {
		return domain.SSHInspectResult{}, err
	}
	defer cleanup()

	command := exec.CommandContext(ctx, r.command, args...)
	capture := &inspectCapture{limit: defaultInspectBytes, abort: func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
	}}
	command.Stdout = inspectStreamWriter{capture: capture, dest: &capture.stdout}
	command.Stderr = inspectStreamWriter{capture: capture, dest: &capture.stderr}
	runErr := command.Run()
	result := domain.SSHInspectResult{
		Command:        remoteCommand,
		Stdout:         capture.stdout.String(),
		Stderr:         capture.stderr.String(),
		Truncated:      capture.truncated,
		BytesRetrieved: int64(capture.used()),
	}
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	if runErr == nil {
		return result, nil
	}
	if ctx.Err() != nil && !capture.truncated {
		return domain.SSHInspectResult{}, fmt.Errorf("ssh inspect: %w", ctx.Err())
	}
	if capture.truncated {
		// 截断后主动杀掉进程，仍返回已经捕获的前缀给模型。
		result.ExitCode = 0
		return result, nil
	}
	if command.ProcessState != nil {
		return result, nil
	}
	return domain.SSHInspectResult{}, fmt.Errorf("ssh inspect: %w", runErr)
}

func (r *Reader) sshArgs(cfg SourceConfig, remoteCommand string) ([]string, func(), error) {
	args := []string{"-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new"}
	cleanup := func() {}
	if cfg.Port > 0 {
		args = append(args, "-p", strconv.Itoa(cfg.Port))
	}
	switch cfg.CredentialKind {
	case projectdomain.SecretSSHPrivateKey:
		keyPath, closer, err := writeTempKey(cfg.plaintext)
		if err != nil {
			return nil, nil, fmt.Errorf("prepare ssh key: %w", err)
		}
		cleanup = closer
		args = append(args, "-i", keyPath)
	case projectdomain.SecretSSHPassword:
		return nil, nil, fmt.Errorf("ssh password authentication is not supported for log reads")
	}
	target := cfg.Host
	if cfg.User != "" {
		target = cfg.User + "@" + cfg.Host
	}
	args = append(args, target, remoteCommand)
	return args, cleanup, nil
}

func (r *Reader) exec(ctx context.Context, cfg SourceConfig, remoteCommand string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	args, cleanup, err := r.sshArgs(cfg, remoteCommand)
	if err != nil {
		return "", err
	}
	defer cleanup()
	command := exec.CommandContext(ctx, r.command, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("read ssh log: %w", ctx.Err())
		}
		diagnostic := fmt.Sprintf("read ssh log: remote command failed: %v; command=%s", err, remoteCommand)
		if stderrText := strings.TrimSpace(stderr.String()); stderrText != "" {
			diagnostic += "; stderr=" + stderrText
		}
		if bounded, _ := observability.SnapshotDiagnostic(diagnostic); bounded != "" {
			return "", fmt.Errorf("%s", bounded)
		}
		return "", fmt.Errorf("read ssh log: remote command failed")
	}
	return stdout.String(), nil
}

func (r *Reader) logRequest(ctx context.Context, operation string, cfg SourceConfig, command string, started time.Time, bytes int, err error) {
	loggedErr := err
	if operation == "inspect" && err != nil && strings.Contains(err.Error(), "ssh inspect completed with exit") {
		// inspect 非零退出对模型是有界结果；INFO 完成记录只保留稳定分类，避免把 stdout dump 进日志。
		loggedErr = fmt.Errorf("ssh inspect remote command failed")
	}
	observability.LogSSHEvidenceRequest(ctx, r.logger, observability.SSHEvidenceRequest{
		Operation: operation, Command: command, Host: cfg.Host, Port: cfg.Port, Duration: time.Since(started), Bytes: bytes,
		CredentialSecretID: cfg.CredentialSecretID, CredentialKind: string(cfg.CredentialKind), Err: loggedErr,
	})
}

func remoteReadCommand(logPath string, maxLines int) string {
	return "tail -n " + strconv.Itoa(maxLines) + " -- " + shellQuote(logPath)
}

func pageFromRaw(scope domain.EvidenceScope, raw string, query domain.LogQuery, maxLines int, maxBytes int64, host string) domain.EvidencePage {
	lines := splitNonEmpty(raw)
	out := make([]domain.EvidenceLine, 0, len(lines))
	truncated := false
	used := int64(0)
	for _, rawLine := range lines {
		message, redacted := redactLine(rawLine)
		if !matchQuery(message, query) {
			continue
		}
		if len(out) >= maxLines || used+int64(len(message)) > maxBytes {
			truncated = true
			break
		}
		used += int64(len(message))
		out = append(out, domain.EvidenceLine{
			EvidenceID: evidenceID(scope, rawLine),
			Message:    message,
			Host:       host,
			Redacted:   redacted,
			Level:      inferLevel(message),
		})
	}
	if len(lines) > maxLines {
		truncated = true
	}
	return domain.EvidencePage{Lines: out, Truncated: truncated}
}

func matchQuery(message string, query domain.LogQuery) bool {
	if query.Level != "" && !strings.EqualFold(inferLevel(message), query.Level) {
		return false
	}
	if len(query.Keywords) == 0 {
		return true
	}
	lower := strings.ToLower(message)
	for _, keyword := range query.Keywords {
		if keyword != "" && !strings.Contains(lower, strings.ToLower(keyword)) {
			return false
		}
	}
	return true
}

func inferLevel(message string) string {
	upper := strings.ToUpper(message)
	for _, level := range []string{"ERROR", "WARN", "INFO", "DEBUG"} {
		if strings.Contains(upper, level) {
			return strings.ToLower(level)
		}
	}
	return ""
}

var (
	redactAuthorization = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*)\S+`)
	redactBearer        = regexp.MustCompile(`(?i)(bearer\s+)\S+`)
	redactOpenAIKey     = regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`)
	redactAssignment    = regexp.MustCompile(`(?i)((?:password|passwd|token|secret|api[_-]?key)\s*[:=]\s*)\S+`)
)

func redactLine(line string) (string, bool) {
	redacted := line
	redacted = redactAuthorization.ReplaceAllString(redacted, "${1}[redacted]")
	redacted = redactBearer.ReplaceAllString(redacted, "${1}[redacted]")
	redacted = redactOpenAIKey.ReplaceAllString(redacted, "[redacted]")
	redacted = redactAssignment.ReplaceAllString(redacted, "${1}[redacted]")
	return redacted, redacted != line
}

func evidenceID(scope domain.EvidenceScope, raw string) string {
	// 用行内容而不是窗口下标，避免 tail 窗口滑动后同一行换 ID。
	sum := sha1.Sum([]byte(scope.ProjectID + "|" + scope.SourceID + "|" + raw))
	return "ev-" + hex.EncodeToString(sum[:8])
}

func splitNonEmpty(value string) []string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func writeTempKey(credential []byte) (string, func(), error) {
	directory, err := os.MkdirTemp("", "fixthe-sshlog-")
	if err != nil {
		return "", nil, fmt.Errorf("create ssh key directory: %w", err)
	}
	pathName := filepath.Join(directory, "id")
	if err := os.WriteFile(pathName, privateKeyBytes(credential), 0o600); err != nil {
		_ = os.RemoveAll(directory)
		return "", nil, fmt.Errorf("write ssh key: %w", err)
	}
	return pathName, func() { _ = os.RemoveAll(directory) }, nil
}

func privateKeyBytes(credential []byte) []byte {
	text := string(credential)
	begin := strings.Index(text, "-----BEGIN ")
	endMarker := strings.Index(text, "-----END ")
	if begin < 0 || endMarker <= begin {
		return append([]byte(nil), credential...)
	}
	rest := text[endMarker:]
	lineEnd := strings.Index(rest, "\n")
	end := len(text)
	if lineEnd >= 0 {
		end = endMarker + lineEnd
	}
	return []byte(strings.TrimSpace(text[begin:end]) + "\n")
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

// ParseSSHSourceConfig 把已校验的 source JSON 映射为适配器配置。
func ParseSSHSourceConfig(projectID, sourceID string, credentialSecretID *string, raw json.RawMessage) (SourceConfig, error) {
	value, err := projectdomain.ParseSSHSourceConfig(raw)
	if err != nil {
		return SourceConfig{}, fmt.Errorf("decode ssh source config: %w", err)
	}
	secretID := ""
	if credentialSecretID != nil {
		secretID = *credentialSecretID
	}
	if value.Host == "" || value.LogPath == "" {
		return SourceConfig{}, fmt.Errorf("ssh source config is incomplete")
	}
	return SourceConfig{
		ProjectID:          projectID,
		SourceID:           sourceID,
		Host:               value.Host,
		Port:               value.Port,
		User:               value.User,
		ProjectFolder:      value.ProjectFolder,
		LogPath:            value.LogPath,
		Mode:               value.Mode,
		Deployment:         value.Deployment,
		CredentialSecretID: secretID,
	}, nil
}
