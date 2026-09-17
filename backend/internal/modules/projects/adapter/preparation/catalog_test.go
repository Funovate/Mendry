package preparation

import (
	"strings"
	"testing"
)

func TestToolchainCatalogResolvesDeclaredAndDefaultVersions(t *testing.T) {
	image := "registry.example/mendry/node@sha256:" + strings.Repeat("a", 64)
	catalog, err := NewToolchainCatalog([]Toolchain{{
		ID: "node-22-default", Runtime: "node", Version: "22", ImageDigest: image, Default: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, requested := range []string{"", "22", "^22.1.0", ">=20 <23"} {
		if entry, ok := catalog.resolve("node", requested, "default"); !ok || entry.ID != "node-22-default" {
			t.Fatalf("resolve node %q = %#v, %v", requested, entry, ok)
		}
	}
	if _, ok := catalog.resolve("node", "20", "default"); ok {
		t.Fatal("unsupported Node version resolved")
	}
}

func TestToolchainCatalogUsesNewerCompatibleGoCompiler(t *testing.T) {
	image := "registry.example/mendry/go@sha256:" + strings.Repeat("b", 64)
	catalog, err := NewToolchainCatalog([]Toolchain{{
		ID: "go-1.23-default", Runtime: "go", Version: "1.23", ImageDigest: image, Default: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if entry, ok := catalog.resolve("go", "1.22", "default"); !ok || entry.ID != "go-1.23-default" {
		t.Fatalf("compatible Go toolchain = %#v, %v", entry, ok)
	}
	if _, ok := catalog.resolve("go", "1.24", "default"); ok {
		t.Fatal("older Go compiler accepted newer language version")
	}
}

func TestToolchainCatalogRejectsMutableImagesAndDuplicateDefaults(t *testing.T) {
	if _, err := NewToolchainCatalog([]Toolchain{{ID: "node", Runtime: "node", Version: "22", ImageDigest: "node:22", Default: true}}); err == nil {
		t.Fatal("mutable image was accepted")
	}
	imageA := "sha256:" + strings.Repeat("a", 64)
	imageB := "sha256:" + strings.Repeat("b", 64)
	if _, err := NewToolchainCatalog([]Toolchain{
		{ID: "node-20", Runtime: "node", Version: "20", ImageDigest: imageA, Default: true},
		{ID: "node-22", Runtime: "node", Version: "22", ImageDigest: imageB, Default: true},
	}); err == nil {
		t.Fatal("duplicate defaults were accepted")
	}
}
