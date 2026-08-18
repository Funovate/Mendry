// Package git 用 git ls-remote 探测仓库引用，供项目配置向导选择分支。
// 凭据只在命令执行期间注入，不得写入日志或返回值。
package git

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"fixthe/backend/internal/modules/projects/application"
	"fixthe/backend/internal/modules/projects/domain"
	"fixthe/backend/internal/platform/observability"
)

const (
	probeTimeout = 15 * time.Second
	opLSRemote   = "ls-remote"
)

// Lister 执行 git ls-remote 探测远程引用。
type Lister struct {
	command string
	logger  *slog.Logger
}

// NewLister 构造仓库引用探测适配器。logger 可选；缺省时不写观测记录。
func NewLister(logger *slog.Logger) *Lister {
	return &Lister{command: "git", logger: logger}
}

// ListRefs 执行 git ls-remote。命令失败对调用方只返回 ErrGitUnreachable；
// 出站日志使用公开 remote 身份，并保留原始 exec 错误做分类，不回传凭据或认证后的 URL。
func (l *Lister) ListRefs(ctx context.Context, remoteURL, transport string, kind domain.SecretKind, credential []byte) (string, error) {
	if l == nil || l.command == "" {
		return "", application.ErrGitUnreachable
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	args := []string{"ls-remote", "--symref", "--heads"}
	env := os.Environ()
	cleanup := func() {}
	target := remoteURL
	if transport == "https" {
		authenticated, err := application.AuthenticatedHTTPSRemote(remoteURL, string(credential))
		if err != nil {
			return "", err
		}
		target = authenticated
		env = append(env, "GIT_TERMINAL_PROMPT=0")
	} else {
		keyPath, closer, err := writeTempKey(credential)
		if err != nil {
			return "", err
		}
		cleanup = closer
		env = append(env, "GIT_SSH_COMMAND=ssh -i "+keyPath+" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o BatchMode=yes")
	}
	defer cleanup()

	command := exec.CommandContext(ctx, l.command, append(args, target)...)
	command.Env = env
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	started := time.Now()
	err := command.Run()
	// 只在真实 exec 之后写一条出站记录；身份用公开 remote，分类用原始 exec 错误。
	l.logRequest(ctx, remoteURL, time.Since(started), probeOutput(stdout.Bytes(), stderr.Bytes(), err), err)
	if err != nil {
		return "", application.ErrGitUnreachable
	}
	return stdout.String(), nil
}

func (l *Lister) logRequest(ctx context.Context, remoteURL string, duration time.Duration, response []byte, err error) {
	host, path := observability.HTTPIdentity(remoteURL)
	observability.LogGitRequest(ctx, l.logger, observability.GitRequest{
		Operation: opLSRemote,
		Host:      host,
		Path:      path,
		Duration:  duration,
		Request:   []byte("git ls-remote --symref --heads " + remoteURL),
		Response:  response,
		Err:       err,
	})
}

// probeOutput 成功只保留 stdout；失败在捕获到内容时附带 stderr，便于对照拒绝原因。
func probeOutput(stdout, stderr []byte, err error) []byte {
	if err == nil {
		return stdout
	}
	switch {
	case len(stdout) == 0:
		return stderr
	case len(stderr) == 0:
		return stdout
	default:
		combined := make([]byte, 0, len(stdout)+1+len(stderr))
		combined = append(combined, stdout...)
		if stdout[len(stdout)-1] != '\n' {
			combined = append(combined, '\n')
		}
		return append(combined, stderr...)
	}
}

func writeTempKey(credential []byte) (string, func(), error) {
	directory, err := os.MkdirTemp("", "fixthe-git-")
	if err != nil {
		return "", nil, fmt.Errorf("create git key directory: %w", err)
	}
	path := filepath.Join(directory, "id")
	if err := os.WriteFile(path, privateKeyBytes(credential), 0o600); err != nil {
		_ = os.RemoveAll(directory)
		return "", nil, fmt.Errorf("write git key: %w", err)
	}
	return path, func() { _ = os.RemoveAll(directory) }, nil
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
