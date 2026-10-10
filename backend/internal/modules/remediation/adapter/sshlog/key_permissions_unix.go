//go:build !windows

package sshlog

// os.MkdirTemp already restricts the directory to its owner on Unix.
func protectKeyDirectory(directory string) error {
	return nil
}
