package git

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	projectapplication "mendry/backend/internal/modules/projects/application"
	projectdomain "mendry/backend/internal/modules/projects/domain"
	"mendry/backend/internal/modules/remediation/domain"
	"mendry/backend/internal/platform/observability"
)

const (
	defaultGitCommand = "git"
	defaultTimeout    = 3 * time.Minute
	defaultMaxDepth   = 2
	defaultMaxEntries = 500
	defaultMaxBytes   = 1 << 20
	defaultMaxResults = 100
	defaultMaxCommits = 50
	reasonBinary      = "binary"
	reasonOversized   = "oversized"
)

// RepositoryConfig 是适配器读取项目仓库所需的无凭据配置。ProductionBranch
// 是每次读取前 fetch 后使用的当前生产分支；deployed commit 只保留在
// remediation run 的历史身份中，不作为本适配器的读取 ref。
type RepositoryConfig struct {
	RemoteURL          string
	Transport          string
	CredentialSecretID string
	ProductionBranch   string
}

// ConfigLoader 按项目 UUID 加载仓库配置，不含用户主体。
type ConfigLoader interface {
	LoadRepository(ctx context.Context, projectID string) (RepositoryConfig, error)
}

// SecretLoader 按项目和 secret ID 加载密文，供适配器本地解密。
type SecretLoader interface {
	GetEncryptedSecret(ctx context.Context, projectID, secretID string) (projectdomain.EncryptedSecret, error)
}

// Options 构造只读 Git 适配器。GitCommand 默认 "git"，测试可注入 stub。
type Options struct {
	Configs        ConfigLoader
	Secrets        SecretLoader
	Cipher         projectapplication.Cipher
	GitCommand     string
	CacheDir       string
	CommandTimeout time.Duration
	Logger         *slog.Logger
}

// Reader 在每次 fetch 后读取配置的 production branch 最新代码，不向调用方暴露凭据或裸 git 客户端。
type Reader struct {
	configs  ConfigLoader
	secrets  SecretLoader
	cipher   projectapplication.Cipher
	command  string
	cacheDir string
	timeout  time.Duration
	logger   *slog.Logger

	mu     sync.Mutex
	clones map[string]string
}

// NewReader 验证依赖并构造仓库只读适配器。
func NewReader(options Options) (*Reader, error) {
	if options.Configs == nil || options.Secrets == nil || options.Cipher == nil {
		return nil, fmt.Errorf("git reader dependencies are required")
	}
	command := strings.TrimSpace(options.GitCommand)
	if command == "" {
		command = defaultGitCommand
	}
	timeout := options.CommandTimeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Reader{
		configs:  options.Configs,
		secrets:  options.Secrets,
		cipher:   options.Cipher,
		command:  command,
		cacheDir: options.CacheDir,
		timeout:  timeout,
		logger:   options.Logger,
		clones:   make(map[string]string),
	}, nil
}

var _ domain.RepositoryReadPort = (*Reader)(nil)

// CredentialFreeRemoteURL 实现 application.RepositoryRemoteResolver。
// 返回值去掉 userinfo，避免 coordinator 把凭据带进 RepoRef。
func (r *Reader) CredentialFreeRemoteURL(ctx context.Context, projectID string) (string, error) {
	cfg, err := r.configs.LoadRepository(ctx, projectID)
	if err != nil {
		return "", err
	}
	return sanitizeRemoteURL(cfg.RemoteURL)
}

// ListTree 列出配置的 production branch 最新代码下的有界树条目。
func (r *Reader) ListTree(ctx context.Context, ref domain.RepoRef, repoPath string, opts domain.TreeOptions) (domain.TreeListing, error) {
	if err := validateRepoPath(repoPath); err != nil {
		return domain.TreeListing{}, err
	}
	session, err := r.open(ctx, ref)
	if err != nil {
		return domain.TreeListing{}, err
	}
	defer session.close()

	maxEntries := opts.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}
	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = defaultMaxDepth
	}
	args := []string{"ls-tree", "-r", "--long", session.commit}
	if repoPath != "" {
		args = append(args, "--", repoPath)
	}
	output, err := session.run(ctx, args...)
	if err != nil {
		return domain.TreeListing{}, err
	}
	entries := make([]domain.TreeEntry, 0)
	truncated := false
	prefix := strings.Trim(repoPath, "/")
	for _, line := range splitNonEmpty(output) {
		entry, ok := parseLsTreeLine(line)
		if !ok {
			continue
		}
		if prefix != "" && entry.Path != prefix && !strings.HasPrefix(entry.Path, prefix+"/") {
			continue
		}
		relative := entry.Path
		if prefix != "" {
			relative = strings.TrimPrefix(entry.Path, prefix+"/")
		}
		if pathDepth(relative) > maxDepth {
			continue
		}
		if len(entries) >= maxEntries {
			truncated = true
			break
		}
		entries = append(entries, entry)
	}
	return domain.TreeListing{Entries: entries, Truncated: truncated}, nil
}

// ReadFile 读取配置的 production branch 最新代码；二进制拒绝，超限截断。
func (r *Reader) ReadFile(ctx context.Context, ref domain.RepoRef, repoPath string, opts domain.ReadOptions) (domain.FileContent, error) {
	if err := validateRepoPath(repoPath); err != nil {
		return domain.FileContent{}, err
	}
	if strings.TrimSpace(repoPath) == "" {
		return domain.FileContent{}, fmt.Errorf("file path is required")
	}
	session, err := r.open(ctx, ref)
	if err != nil {
		return domain.FileContent{}, err
	}
	defer session.close()

	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	kind, err := session.run(ctx, "cat-file", "-t", session.commit+":"+repoPath)
	if err != nil {
		return domain.FileContent{}, err
	}
	if strings.TrimSpace(kind) != "blob" {
		return domain.FileContent{Path: repoPath, Truncated: true, Reason: reasonBinary}, nil
	}
	content, err := session.runBytes(ctx, "cat-file", "-p", session.commit+":"+repoPath)
	if err != nil {
		return domain.FileContent{}, err
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return domain.FileContent{Path: repoPath, Truncated: true, Reason: reasonBinary}, nil
	}
	if int64(len(content)) > maxBytes {
		return domain.FileContent{
			Path:      repoPath,
			Content:   content[:maxBytes],
			Truncated: true,
			Reason:    reasonOversized,
		}, nil
	}
	return domain.FileContent{Path: repoPath, Content: content}, nil
}

// Search 在配置的 production branch 最新代码上做有界文本搜索。
func (r *Reader) Search(ctx context.Context, ref domain.RepoRef, query domain.SearchQuery) (domain.SearchResult, error) {
	if strings.TrimSpace(query.Pattern) == "" {
		return domain.SearchResult{}, fmt.Errorf("search pattern is required")
	}
	if err := validateRepoPath(query.PathGlob); err != nil {
		return domain.SearchResult{}, err
	}
	session, err := r.open(ctx, ref)
	if err != nil {
		return domain.SearchResult{}, err
	}
	defer session.close()

	maxResults := query.MaxResults
	if maxResults <= 0 {
		maxResults = defaultMaxResults
	}
	args := []string{"grep", "-n", "-I", "-e", query.Pattern, session.commit, "--"}
	if query.PathGlob != "" {
		args = append(args, query.PathGlob)
	}
	output, err := session.runAllowExit(ctx, 1, args...)
	if err != nil {
		return domain.SearchResult{}, err
	}
	matches := make([]domain.SearchMatch, 0)
	truncated := false
	for _, line := range splitNonEmpty(output) {
		match, ok := parseGrepLine(line, session.commit)
		if !ok {
			continue
		}
		if len(matches) >= maxResults {
			truncated = true
			break
		}
		matches = append(matches, match)
	}
	return domain.SearchResult{Matches: matches, Truncated: truncated}, nil
}

// History 读取配置的 production branch 当前 tip 之前的有界提交历史。
func (r *Reader) History(ctx context.Context, ref domain.RepoRef, repoPath string, opts domain.HistoryOptions) (domain.History, error) {
	if err := validateRepoPath(repoPath); err != nil {
		return domain.History{}, err
	}
	session, err := r.open(ctx, ref)
	if err != nil {
		return domain.History{}, err
	}
	defer session.close()

	maxCommits := opts.MaxCommits
	if maxCommits <= 0 {
		maxCommits = defaultMaxCommits
	}
	args := []string{"log", "--format=%H%x1f%an%x1f%aI%x1f%s", "-n", strconv.Itoa(maxCommits + 1), session.commit}
	if repoPath != "" {
		args = append(args, "--", repoPath)
	}
	output, err := session.run(ctx, args...)
	if err != nil {
		return domain.History{}, err
	}
	commits := make([]domain.CommitInfo, 0)
	truncated := false
	for _, line := range splitNonEmpty(output) {
		commit, ok := parseLogLine(line)
		if !ok {
			continue
		}
		if len(commits) >= maxCommits {
			truncated = true
			break
		}
		commits = append(commits, commit)
	}
	return domain.History{Commits: commits, Truncated: truncated}, nil
}

type gitSession struct {
	reader  *Reader
	repoDir string
	// commit 保存配置分支的 fully-qualified ref，沿用字段名以保持 Git 命令调用集中。
	commit  string
	env     []string
	cleanup func()
}

func (r *Reader) open(ctx context.Context, ref domain.RepoRef) (*gitSession, error) {
	cfg, err := r.loadConfig(ctx, ref)
	if err != nil {
		return nil, err
	}
	branchRef, err := productionBranchRef(cfg.ProductionBranch)
	if err != nil {
		return nil, err
	}
	env := append([]string{}, os.Environ()...)
	target := cfg.RemoteURL
	sessionCleanup := func() {}
	if cfg.Transport != "file" {
		plaintext, closer, err := r.decryptCredential(ctx, ref.ProjectID, cfg)
		if err != nil {
			return nil, err
		}
		defer closer()
		switch cfg.Transport {
		case "https":
			authenticated, err := projectapplication.AuthenticatedHTTPSRemote(cfg.RemoteURL, string(plaintext))
			if err != nil {
				return nil, projectapplication.ErrGitUnreachable
			}
			target = authenticated
			env = append(env, "GIT_TERMINAL_PROMPT=0")
		case "ssh":
			keyPath, keyCloser, err := writeTempKey(plaintext)
			if err != nil {
				return nil, projectapplication.ErrGitUnreachable
			}
			sessionCleanup = keyCloser
			env = append(env, "GIT_SSH_COMMAND=ssh -i "+keyPath+" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o BatchMode=yes")
		default:
			return nil, fmt.Errorf("unsupported repository transport")
		}
	}

	repoDir, err := r.ensureClone(ctx, cfg, target, env)
	if err != nil {
		sessionCleanup()
		return nil, err
	}
	return &gitSession{reader: r, repoDir: repoDir, commit: branchRef, env: env, cleanup: sessionCleanup}, nil
}

func (s *gitSession) close() {
	if s.cleanup != nil {
		s.cleanup()
	}
}

func (s *gitSession) run(ctx context.Context, args ...string) (string, error) {
	output, err := s.runAllowExit(ctx, 0, args...)
	return output, err
}

func (s *gitSession) runAllowExit(ctx context.Context, allowed int, args ...string) (string, error) {
	output, err := s.runBytesAllowExit(ctx, allowed, args...)
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func (s *gitSession) runBytes(ctx context.Context, args ...string) ([]byte, error) {
	return s.runBytesAllowExit(ctx, 0, args...)
}

func (s *gitSession) runBytesAllowExit(ctx context.Context, allowed int, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, s.reader.timeout)
	defer cancel()
	command := exec.CommandContext(ctx, s.reader.command, args...)
	command.Dir = s.repoDir
	command.Env = s.env
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == allowed {
			return stdout.Bytes(), nil
		}
		return nil, gitCommandFailure(err, stderr.Bytes(), "")
	}
	return stdout.Bytes(), nil
}

func (r *Reader) loadConfig(ctx context.Context, ref domain.RepoRef) (RepositoryConfig, error) {
	if strings.TrimSpace(ref.ProjectID) == "" {
		return RepositoryConfig{}, fmt.Errorf("project id is required")
	}
	cfg, err := r.configs.LoadRepository(ctx, ref.ProjectID)
	if err != nil {
		return RepositoryConfig{}, fmt.Errorf("load repository config: %w", err)
	}
	if strings.TrimSpace(ref.RemoteURL) != "" {
		cfg.RemoteURL = ref.RemoteURL
	}
	sanitized, err := sanitizeRemoteURL(cfg.RemoteURL)
	if err != nil {
		return RepositoryConfig{}, err
	}
	cfg.RemoteURL = sanitized
	if cfg.Transport == "" {
		parsed, parseErr := url.Parse(cfg.RemoteURL)
		if parseErr != nil {
			return RepositoryConfig{}, projectapplication.ErrGitUnreachable
		}
		cfg.Transport = parsed.Scheme
	}
	// file 仅用于本地测试仓库，生产配置只会给出 https 或 ssh。
	if cfg.Transport != "https" && cfg.Transport != "ssh" && cfg.Transport != "file" {
		return RepositoryConfig{}, fmt.Errorf("unsupported repository transport")
	}
	return cfg, nil
}

func productionBranchRef(value string) (string, error) {
	branch := strings.TrimSpace(value)
	if branch == "" || branch == "@" || strings.HasPrefix(branch, "-") ||
		strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") ||
		strings.Contains(branch, "//") || strings.Contains(branch, "..") ||
		strings.Contains(branch, "@{") || strings.HasSuffix(branch, ".") {
		return "", fmt.Errorf("production branch is invalid")
	}
	if strings.ContainsAny(branch, " ~^:?*[\\") {
		return "", fmt.Errorf("production branch is invalid")
	}
	for index := 0; index < len(branch); index++ {
		if branch[index] < 0x20 || branch[index] == 0x7f {
			return "", fmt.Errorf("production branch is invalid")
		}
	}
	for _, component := range strings.Split(branch, "/") {
		if component == "" || component == "." || component == ".." ||
			strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".") ||
			strings.HasSuffix(component, ".lock") {
			return "", fmt.Errorf("production branch is invalid")
		}
	}
	return "refs/heads/" + branch, nil
}

func (r *Reader) decryptCredential(ctx context.Context, projectID string, cfg RepositoryConfig) ([]byte, func(), error) {
	if strings.TrimSpace(cfg.CredentialSecretID) == "" {
		return nil, func() {}, fmt.Errorf("repository credential is required")
	}
	encrypted, err := r.secrets.GetEncryptedSecret(ctx, projectID, cfg.CredentialSecretID)
	if err != nil {
		return nil, func() {}, fmt.Errorf("load repository credential: %w", err)
	}
	plaintext, err := r.cipher.Decrypt(encrypted.ProjectID, encrypted.ID, encrypted.Kind, encrypted.Ciphertext, encrypted.Nonce)
	if err != nil {
		return nil, func() {}, fmt.Errorf("decrypt repository credential: %w", err)
	}
	return plaintext, func() { clearBytes(plaintext) }, nil
}

func (r *Reader) ensureClone(ctx context.Context, cfg RepositoryConfig, authenticatedURL string, env []string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if dir, ok := r.clones[cfg.RemoteURL]; ok {
		if err := r.fetch(ctx, dir, cfg, authenticatedURL, env); err != nil {
			return "", err
		}
		return dir, nil
	}
	parent := r.cacheDir
	if parent == "" {
		created, err := os.MkdirTemp("", "mendry-git-cache-")
		if err != nil {
			return "", projectapplication.ErrGitUnreachable
		}
		r.cacheDir = created
		parent = created
	}
	dir, err := os.MkdirTemp(parent, "repo-")
	if err != nil {
		return "", projectapplication.ErrGitUnreachable
	}
	if err := r.cloneBare(ctx, dir, cfg, authenticatedURL, env); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	// clone 可能把带 userinfo 的 URL 写进 origin；立刻改回公开 URL，
	// 避免凭据落在磁盘或后续 git 输出里。
	if err := r.rewriteOrigin(ctx, dir, cfg.RemoteURL, env); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	r.clones[cfg.RemoteURL] = dir
	return dir, nil
}

func (r *Reader) rewriteOrigin(ctx context.Context, dir, publicURL string, env []string) error {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	command := exec.CommandContext(ctx, r.command, "remote", "set-url", "origin", publicURL)
	command.Dir = dir
	command.Env = env
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return gitCommandFailure(err, stderr.Bytes(), publicURL)
	}
	return nil
}

func (r *Reader) cloneBare(ctx context.Context, dir string, cfg RepositoryConfig, authenticatedURL string, env []string) error {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	started := time.Now()
	command := exec.CommandContext(ctx, r.command, "clone", "--bare", authenticatedURL, dir)
	command.Env = env
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		r.logNetworkOperation(ctx, "clone", cfg, started, err)
		return gitCommandFailure(err, stderr.Bytes(), authenticatedURL)
	}
	r.logNetworkOperation(ctx, "clone", cfg, started, nil)
	return nil
}

func (r *Reader) fetch(ctx context.Context, dir string, cfg RepositoryConfig, authenticatedURL string, env []string) error {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	started := time.Now()
	command := exec.CommandContext(ctx, r.command, "fetch", "--prune", authenticatedURL, "+refs/heads/*:refs/heads/*")
	command.Dir = dir
	command.Env = env
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		r.logNetworkOperation(ctx, "fetch", cfg, started, err)
		return gitCommandFailure(err, stderr.Bytes(), authenticatedURL)
	}
	r.logNetworkOperation(ctx, "fetch", cfg, started, nil)
	return nil
}

func (r *Reader) logNetworkOperation(ctx context.Context, operation string, cfg RepositoryConfig, started time.Time, err error) {
	host, pathName := observability.HTTPIdentity(cfg.RemoteURL)
	observability.LogGitRequest(ctx, r.logger, observability.GitRequest{
		Operation: operation, Host: host, Path: pathName,
		Duration: time.Since(started), Request: []byte("git " + operation + " " + cfg.RemoteURL),
		CredentialSecretID: cfg.CredentialSecretID, Transport: cfg.Transport, Err: err,
	})
}

const maxGitDiagnosticBytes = 512

// gitCommandFailure 保留稳定 sentinel，同时把有限且脱敏的 stderr 带给边界日志，
// 让 DNS、TLS、认证等故障可区分；认证 URL 和凭据绝不进入返回错误。
func gitCommandFailure(commandErr error, stderr []byte, sensitiveURL string) error {
	diagnostic := strings.TrimSpace(string(stderr))
	if sensitiveURL != "" {
		diagnostic = strings.ReplaceAll(diagnostic, sensitiveURL, publicRemoteURL(sensitiveURL))
		if parsed, err := url.Parse(sensitiveURL); err == nil && parsed.User != nil {
			diagnostic = strings.ReplaceAll(diagnostic, parsed.User.String(), "<credential>")
		}
	}
	if len(diagnostic) > maxGitDiagnosticBytes {
		diagnostic = diagnostic[:maxGitDiagnosticBytes]
	}
	if diagnostic == "" {
		return fmt.Errorf("%w: %v", projectapplication.ErrGitUnreachable, commandErr)
	}
	return fmt.Errorf("%w: %s", projectapplication.ErrGitUnreachable, diagnostic)
}

func publicRemoteURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parsed.User = nil
	return parsed.String()
}

func sanitizeRemoteURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User != nil {
		return "", projectapplication.ErrGitUnreachable
	}
	switch parsed.Scheme {
	case "https", "ssh":
		if parsed.Host == "" {
			return "", projectapplication.ErrGitUnreachable
		}
	case "file":
		if parsed.Path == "" {
			return "", projectapplication.ErrGitUnreachable
		}
	default:
		return "", projectapplication.ErrGitUnreachable
	}
	parsed.User = nil
	return parsed.String(), nil
}

func validateRepoPath(value string) error {
	if value == "" {
		return nil
	}
	cleaned := path.Clean("/" + strings.ReplaceAll(value, "\\", "/"))
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "\\") || cleaned == "/" || strings.Contains(cleaned, "..") {
		return fmt.Errorf("path is outside the repository")
	}
	for _, seg := range strings.Split(value, "/") {
		if seg == ".." {
			return fmt.Errorf("path is outside the repository")
		}
	}
	return nil
}

func parseLsTreeLine(line string) (domain.TreeEntry, bool) {
	// 100644 blob 4b825d...      12\tpath
	mode, rest, ok := strings.Cut(line, " ")
	if !ok {
		return domain.TreeEntry{}, false
	}
	kind, rest, ok := strings.Cut(rest, " ")
	if !ok {
		return domain.TreeEntry{}, false
	}
	_, rest, ok = strings.Cut(rest, " ")
	if !ok {
		return domain.TreeEntry{}, false
	}
	sizeText, name, ok := strings.Cut(rest, "\t")
	if !ok {
		return domain.TreeEntry{}, false
	}
	entryType := "file"
	if kind == "tree" {
		entryType = "dir"
	}
	size, _ := strconv.ParseInt(strings.TrimSpace(sizeText), 10, 64)
	return domain.TreeEntry{Path: name, Type: entryType, Size: size, Mode: mode}, true
}

func parseGrepLine(line, commit string) (domain.SearchMatch, bool) {
	// <commit>:path:line:text  or path:line:text
	text := strings.TrimPrefix(line, commit+":")
	pathName, rest, ok := strings.Cut(text, ":")
	if !ok {
		return domain.SearchMatch{}, false
	}
	lineText, content, ok := strings.Cut(rest, ":")
	if !ok {
		return domain.SearchMatch{}, false
	}
	number, err := strconv.Atoi(lineText)
	if err != nil {
		return domain.SearchMatch{}, false
	}
	return domain.SearchMatch{Path: pathName, LineNumber: number, Line: content}, true
}

func parseLogLine(line string) (domain.CommitInfo, bool) {
	parts := strings.Split(line, "\x1f")
	if len(parts) < 4 {
		return domain.CommitInfo{}, false
	}
	stamp, err := time.Parse(time.RFC3339, parts[2])
	if err != nil {
		stamp = time.Time{}
	}
	return domain.CommitInfo{Hash: parts[0], Author: parts[1], Timestamp: stamp.UTC(), Message: parts[3]}, true
}

func pathDepth(value string) int {
	trimmed := strings.Trim(value, "/")
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "/"))
}

func splitNonEmpty(value string) []string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

func writeTempKey(credential []byte) (string, func(), error) {
	directory, err := os.MkdirTemp("", "mendry-git-")
	if err != nil {
		return "", nil, fmt.Errorf("create git key directory: %w", err)
	}
	pathName := filepath.Join(directory, "id")
	if err := os.WriteFile(pathName, privateKeyBytes(credential), 0o600); err != nil {
		_ = os.RemoveAll(directory)
		return "", nil, fmt.Errorf("write git key: %w", err)
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
