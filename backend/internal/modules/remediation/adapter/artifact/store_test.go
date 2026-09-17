package artifact

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRoundTripAndDetectsTampering(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "artifacts"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("diff --git a/main.go b/main.go\n")
	reference, hash, err := store.Put(context.Background(), content)
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if reference != "sha256:"+hash {
		t.Fatalf("reference/hash = %q/%q", reference, hash)
	}
	got, err := store.Get(context.Background(), reference, 1024)
	if err != nil || string(got) != string(content) {
		t.Fatalf("Get() = %q, err=%v", got, err)
	}

	path := filepath.Join(store.root, "sha256", hash[:2], hash)
	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), reference, 1024); err == nil {
		t.Fatal("tampered artifact was returned")
	}
}

func TestStoreRejectsSymlinkedAndFilesystemRoots(t *testing.T) {
	if _, err := NewStore(string(filepath.Separator), 1024); err == nil {
		t.Fatal("filesystem root was accepted as artifact storage")
	}
	temp := t.TempDir()
	target := filepath.Join(temp, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(temp, "linked")
	if err := os.Symlink(target, root); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Put(context.Background(), []byte("artifact")); err == nil {
		t.Fatal("symlinked artifact root was accepted")
	}
}

func TestStoreEnforcesSizeAndReferenceBounds(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "artifacts"), 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Put(context.Background(), []byte("12345")); err == nil {
		t.Fatal("oversized artifact was accepted")
	}
	if _, err := store.Get(context.Background(), "sha256:../escape", 4); err == nil {
		t.Fatal("path-bearing artifact reference was accepted")
	}
}
