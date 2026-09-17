package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const defaultMaxArtifactBytes = 64 << 20

// Store persists immutable blobs beneath a private, content-addressed directory.
type Store struct {
	root    string
	maxSize int64
}

func NewStore(root string, maxSize int64) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("artifact root is required")
	}
	if maxSize < 1 {
		maxSize = defaultMaxArtifactBytes
	}
	absolute, err := filepath.Abs(root)
	if err != nil || absolute == string(filepath.Separator) {
		return nil, fmt.Errorf("artifact root is invalid")
	}
	return &Store{root: absolute, maxSize: maxSize}, nil
}

func (s *Store) Put(ctx context.Context, content []byte) (string, string, error) {
	if s == nil || s.root == "" {
		return "", "", fmt.Errorf("artifact store is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if int64(len(content)) > s.maxSize {
		return "", "", fmt.Errorf("artifact exceeds configured size limit")
	}
	if err := ensurePrivateDirectoryTree(s.root); err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])
	storageRoot := filepath.Join(s.root, "sha256")
	if err := os.MkdirAll(storageRoot, 0o700); err != nil {
		return "", "", fmt.Errorf("create artifact namespace: %w", err)
	}
	if err := ensurePrivateDirectory(storageRoot); err != nil {
		return "", "", err
	}
	dir := filepath.Join(storageRoot, hash[:2])
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("create artifact directory: %w", err)
	}
	if err := ensurePrivateDirectory(dir); err != nil {
		return "", "", err
	}
	finalPath := filepath.Join(dir, hash)
	if existing, err := os.ReadFile(finalPath); err == nil {
		if !contentMatches(existing, hash) {
			return "", "", fmt.Errorf("existing artifact content does not match its address")
		}
		return "sha256:" + hash, hash, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("inspect existing artifact: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".pending-*")
	if err != nil {
		return "", "", fmt.Errorf("create temporary artifact: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return "", "", fmt.Errorf("secure temporary artifact: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return "", "", fmt.Errorf("write artifact: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", "", fmt.Errorf("sync artifact: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", "", fmt.Errorf("close artifact: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return "", "", fmt.Errorf("publish artifact: %w", err)
	}
	return "sha256:" + hash, hash, nil
}

func (s *Store) Get(ctx context.Context, reference string, maxBytes int64) ([]byte, error) {
	if s == nil || s.root == "" {
		return nil, fmt.Errorf("artifact store is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	hash, err := parseReference(reference)
	if err != nil {
		return nil, err
	}
	if maxBytes < 1 || maxBytes > s.maxSize {
		maxBytes = s.maxSize
	}
	if err := verifyArtifactDirectory(s.root); err != nil {
		return nil, err
	}
	storageRoot := filepath.Join(s.root, "sha256")
	if err := verifyArtifactDirectory(storageRoot); err != nil {
		return nil, err
	}
	prefixDirectory := filepath.Join(storageRoot, hash[:2])
	if err := verifyArtifactDirectory(prefixDirectory); err != nil {
		return nil, err
	}
	filePath := filepath.Join(prefixDirectory, hash)
	info, err := os.Lstat(filePath)
	if err != nil {
		return nil, fmt.Errorf("read artifact: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxBytes {
		return nil, fmt.Errorf("artifact type or size is invalid")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open artifact: %w", err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read artifact content: %w", err)
	}
	if int64(len(content)) > maxBytes || !contentMatches(content, hash) {
		return nil, fmt.Errorf("artifact content failed size or hash verification")
	}
	return content, nil
}

func parseReference(reference string) (string, error) {
	if !strings.HasPrefix(reference, "sha256:") {
		return "", fmt.Errorf("artifact reference scheme is invalid")
	}
	hash := strings.TrimPrefix(reference, "sha256:")
	if len(hash) != sha256.Size*2 {
		return "", fmt.Errorf("artifact reference hash is invalid")
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return "", fmt.Errorf("artifact reference hash is invalid")
	}
	return strings.ToLower(hash), nil
}

func contentMatches(content []byte, expected string) bool {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]) == expected
}

func ensurePrivateDirectoryTree(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create artifact root: %w", err)
	}
	return ensurePrivateDirectory(directory)
}

func verifyArtifactDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("artifact path is not a real directory")
	}
	return nil
}

func ensurePrivateDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect artifact directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("artifact directory is not a regular directory")
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("secure artifact directory: %w", err)
	}
	return nil
}
