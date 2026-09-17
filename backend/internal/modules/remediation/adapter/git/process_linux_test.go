//go:build linux

package git

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPublisherTimeoutKillsChildProcess(t *testing.T) {
	root := t.TempDir()
	pidFile := filepath.Join(root, "child.pid")
	wrapper := filepath.Join(root, "git-wrapper")
	script := "#!/bin/sh\nsleep 30 &\necho $! > " + strconv.Quote(pidFile) + "\nwait\n"
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	p := &Publisher{command: wrapper, localCommandTimeout: 200 * time.Millisecond}
	_, err := p.runGit(context.Background(), root, os.Environ(), nil, "ls-tree")
	if err == nil {
		t.Fatal("expected timeout")
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	// A killed child can remain a zombie until the system reaps it.
	status, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	_, tail, ok := strings.Cut(string(status), ") ")
	if !ok || !strings.HasPrefix(tail, "Z ") {
		t.Fatalf("child remains running: %s", status)
	}
}
