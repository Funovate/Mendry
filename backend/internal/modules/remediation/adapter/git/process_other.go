//go:build !linux

package git

import "os/exec"

func configureGitProcess(command *exec.Cmd) {}
