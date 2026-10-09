package sshlog

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestTempKeyRestrictsInheritedWindowsPermissions(t *testing.T) {
	parent := t.TempDir()
	// Reproduce a TEMP directory granting another group inherited access.
	descriptor, err := windows.SecurityDescriptorFromString("D:(A;OICI;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(parent, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMP", parent)
	t.Setenv("TEMP", parent)
	key, cleanup, err := writeTempKey([]byte("test private key"))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Dir(key), key} {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		got := sd.String()
		if strings.Count(got, "(A;") != 1 || !strings.Contains(got, ";;;"+user.User.Sid.String()+")") {
			t.Fatalf("unexpected ACL on %s: %s", path, got)
		}
	}
	content, err := os.ReadFile(key)
	if err != nil || string(content) != "test private key" {
		t.Fatalf("read key: %q, %v", content, err)
	}
	cleanup()
	if _, err := os.Stat(filepath.Dir(key)); !os.IsNotExist(err) {
		t.Fatalf("temporary directory not removed: %v", err)
	}
}

func TestTempKeyAcceptedByWindowsOpenSSH(t *testing.T) {
	keygen, err := exec.LookPath("ssh-keygen.exe")
	if err != nil {
		t.Skip("Windows OpenSSH is not installed")
	}
	original := filepath.Join(t.TempDir(), "original")
	if output, err := exec.Command(keygen, "-q", "-t", "ed25519", "-N", "", "-f", original).CombinedOutput(); err != nil {
		t.Fatalf("generate test key: %v: %s", err, output)
	}
	credential, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	key, cleanup, err := writeTempKey(credential)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if output, err := exec.Command(keygen, "-y", "-P", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("OpenSSH rejected temporary private key: %v: %s", err, output)
	}
}

func TestProtectKeyDirectoryMissingPath(t *testing.T) {
	if err := protectKeyDirectory(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected permission setup failure for missing directory")
	}
}
