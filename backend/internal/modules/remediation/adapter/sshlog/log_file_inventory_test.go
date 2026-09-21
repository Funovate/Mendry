package sshlog

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"mendry/backend/internal/modules/projects/application"
)

func runLogInventoryPython(t *testing.T, input string) []byte {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 required")
	}
	out, err := exec.Command(python, "-c", logFileInventoryPython, base64.StdEncoding.EncodeToString([]byte(input))).CombinedOutput()
	if err != nil {
		t.Fatalf("inventory failed: %v: %s", err, out)
	}
	return out
}

func TestLogFileInventoryListsDirectoryAndFileParent(t *testing.T) {
	dir := t.TempDir()
	filename := "app '$(touch injected)'.log"
	file := filepath.Join(dir, filename)
	if err := os.WriteFile(file, []byte("private log content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "archive"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, filepath.Join(dir, "current.log")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "broken.log")); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{dir, file} {
		var listing application.LogFileListing
		if err := json.Unmarshal(runLogInventoryPython(t, input), &listing); err != nil {
			t.Fatal(err)
		}
		if listing.Directory != dir || listing.Truncated || len(listing.Entries) != 3 {
			t.Fatalf("unexpected inventory: %+v", listing)
		}
		if listing.Entries[0].Kind != "directory" {
			t.Fatal("directories must sort first")
		}
		found := false
		for _, entry := range listing.Entries {
			if entry.Name == filename {
				found = true
				if entry.Path != file || !entry.Readable || entry.Kind != "file" {
					t.Fatalf("invalid file: %+v", entry)
				}
			}
		}
		if !found {
			t.Fatal("filename was lost")
		}
	}
}

func TestLogFileInventoryEmptyMissingAndBounded(t *testing.T) {
	dir := t.TempDir()
	var listing application.LogFileListing
	if err := json.Unmarshal(runLogInventoryPython(t, dir), &listing); err != nil || listing.Entries == nil || len(listing.Entries) != 0 {
		t.Fatalf("empty result: %+v, %v", listing, err)
	}
	var failure struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(runLogInventoryPython(t, filepath.Join(dir, "missing")), &failure); err != nil || failure.Error != "not_found" {
		t.Fatalf("missing result: %+v %v", failure, err)
	}
	for i := 0; i < 101; i++ {
		f, err := os.CreateTemp(dir, "app-*.log")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := json.Unmarshal(runLogInventoryPython(t, dir), &listing); err != nil || !listing.Truncated || len(listing.Entries) != 100 {
		t.Fatalf("bounded result: %d truncated=%v err=%v", len(listing.Entries), listing.Truncated, err)
	}
}
