// Command mcp-workspace is a deliberately small MCP stdio server used by the
// agentcore-local examples. The root is supplied by the operator; it is not
// read from model or event input.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxPathBytes    = 1024
	maxContentBytes = 64 << 10
)

type workspaceServer struct {
	root string
}

type readInput struct {
	Path string `json:"path"`
}

type readOutput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Bytes   int    `json:"bytes"`
}

type writeInput struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	ActionID string `json:"actionId"`
}

type writeOutput struct {
	Path       string `json:"path"`
	ActionID   string `json:"actionId"`
	Bytes      int    `json:"bytes"`
	ContentSHA string `json:"contentSha256"`
	Changed    bool   `json:"changed"`
}

type verifyInput struct {
	Path     string `json:"path"`
	Expected string `json:"expected"`
	ActionID string `json:"actionId"`
}

type verifyOutput struct {
	Path     string `json:"path"`
	ActionID string `json:"actionId"`
	Status   string `json:"status"`
	Verified bool   `json:"verified"`
	Bytes    int    `json:"bytes"`
}

func (s *workspaceServer) read(ctx context.Context, _ *mcp.CallToolRequest, input readInput) (*mcp.CallToolResult, readOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, readOutput{}, err
	}
	path, err := s.resolve(input.Path, false)
	if err != nil {
		return nil, readOutput{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, readOutput{}, fmt.Errorf("read workspace file: %w", err)
	}
	if len(data) > maxContentBytes {
		return nil, readOutput{}, errors.New("workspace file is too large")
	}
	return nil, readOutput{Path: input.Path, Content: string(data), Bytes: len(data)}, nil
}

func (s *workspaceServer) write(ctx context.Context, _ *mcp.CallToolRequest, input writeInput) (*mcp.CallToolResult, writeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, writeOutput{}, err
	}
	if strings.TrimSpace(input.ActionID) == "" || len(input.ActionID) > 256 {
		return nil, writeOutput{}, errors.New("actionId is required and bounded")
	}
	if len(input.Content) > maxContentBytes {
		return nil, writeOutput{}, errors.New("workspace content is too large")
	}
	if err := s.ensureParent(input.Path); err != nil {
		return nil, writeOutput{}, err
	}
	path, err := s.resolve(input.Path, true)
	if err != nil {
		return nil, writeOutput{}, err
	}
	before, readErr := os.ReadFile(path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return nil, writeOutput{}, fmt.Errorf("read workspace target: %w", readErr)
	}
	if len(before) > maxContentBytes {
		return nil, writeOutput{}, errors.New("existing workspace file is too large")
	}
	if err := atomicWrite(path, []byte(input.Content)); err != nil {
		return nil, writeOutput{}, fmt.Errorf("write workspace file: %w", err)
	}
	sum := sha256.Sum256([]byte(input.Content))
	return nil, writeOutput{Path: input.Path, ActionID: input.ActionID, Bytes: len(input.Content), ContentSHA: hex.EncodeToString(sum[:]), Changed: string(before) != input.Content}, nil
}

func (s *workspaceServer) verify(ctx context.Context, _ *mcp.CallToolRequest, input verifyInput) (*mcp.CallToolResult, verifyOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, verifyOutput{}, err
	}
	if strings.TrimSpace(input.ActionID) == "" || len(input.ActionID) > 256 {
		return nil, verifyOutput{}, errors.New("actionId is required and bounded")
	}
	if len(input.Expected) > maxContentBytes {
		return nil, verifyOutput{}, errors.New("expected workspace content is too large")
	}
	path, err := s.resolve(input.Path, false)
	if err != nil {
		return nil, verifyOutput{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, verifyOutput{}, fmt.Errorf("read workspace verification target: %w", err)
	}
	if len(data) > maxContentBytes {
		return nil, verifyOutput{}, errors.New("workspace verification target is too large")
	}
	passed := string(data) == input.Expected
	status := "failed"
	if passed {
		status = "passed"
	}
	return nil, verifyOutput{Path: input.Path, ActionID: input.ActionID, Status: status, Verified: passed, Bytes: len(data)}, nil
}

func (s *workspaceServer) ensureParent(requested string) error {
	if len(requested) == 0 || len(requested) > maxPathBytes || filepath.IsAbs(requested) || strings.ContainsRune(requested, 0) {
		return errors.New("workspace path must be a bounded relative path")
	}
	cleaned := filepath.Clean(requested)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return errors.New("workspace path escapes the configured root")
	}
	parent := filepath.Dir(cleaned)
	if parent == "." {
		return nil
	}
	current := s.root
	for _, part := range strings.Split(parent, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return fmt.Errorf("create workspace directory: %w", err)
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("workspace parent is not a directory")
		}
	}
	return nil
}

func (s *workspaceServer) resolve(requested string, forWrite bool) (string, error) {
	if len(requested) == 0 || len(requested) > maxPathBytes || filepath.IsAbs(requested) || strings.ContainsRune(requested, 0) {
		return "", errors.New("workspace path must be a bounded relative path")
	}
	cleaned := filepath.Clean(requested)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", errors.New("workspace path escapes the configured root")
	}
	candidate := filepath.Join(s.root, cleaned)
	if err := checkNoSymlinks(s.root, cleaned); err != nil {
		return "", err
	}
	if !forWrite {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", err
		}
		if !withinRoot(s.root, resolved) {
			return "", errors.New("workspace path escapes the configured root")
		}
		return resolved, nil
	}
	parent := filepath.Dir(candidate)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	if !withinRoot(s.root, resolvedParent) {
		return "", errors.New("workspace path escapes the configured root")
	}
	return filepath.Join(resolvedParent, filepath.Base(candidate)), nil
}

func checkNoSymlinks(root, relative string) error {
	current := root
	for _, part := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			// A write may create the final component. Parent components must exist.
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("workspace path cannot traverse a symlink")
		}
	}
	return nil
}

func withinRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func atomicWrite(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".agentcore-workspace-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryName)
	}
	defer cleanup()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return err
	}
	return directory.Close()
}

func main() {
	rootFlag := flag.String("root", "", "operator-selected workspace root")
	flag.Parse()
	if strings.TrimSpace(*rootFlag) == "" {
		fmt.Fprintln(os.Stderr, "-root is required")
		os.Exit(2)
	}
	root, err := filepath.Abs(*rootFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid workspace root")
		os.Exit(2)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "create workspace root:", err)
		os.Exit(2)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "resolve workspace root:", err)
		os.Exit(2)
	}

	workspace := &workspaceServer{root: root}
	server := mcp.NewServer(&mcp.Implementation{Name: "mendry-mcp-workspace", Version: "v1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "read_file", Description: "Read a bounded file below the configured workspace root."}, workspace.read)
	mcp.AddTool(server, &mcp.Tool{Name: "write_file", Description: "Atomically write a bounded file below the configured workspace root."}, workspace.write)
	mcp.AddTool(server, &mcp.Tool{Name: "verify_file", Description: "Independently compare a file with expected content."}, workspace.verify)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, "MCP server stopped:", err)
		os.Exit(1)
	}
}
