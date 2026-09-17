package preparation

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	remediationdomain "mendry/backend/internal/modules/remediation/domain"
)

func TestDiscoverCandidatesRequiresLockedNPMTestsAndGoTests(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\ngo 1.23\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), []byte("example.invalid/module v1.0.0 h1:test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main_test.go"), []byte("package example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := filepath.Join(root, "services", "web")
	if err := os.MkdirAll(service, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(service, "package.json"), []byte(`{"scripts":{"test":"vitest run"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(service, "package-lock.json"), []byte(`{"lockfileVersion":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	candidates, err := discoverCandidateDetails(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].Directory != "." || candidates[0].Runtime != "go" || candidates[1].Directory != "services/web" || candidates[1].Runtime != "node" {
		t.Fatalf("candidates = %#v", candidates)
	}
}

func TestDiscoverCandidatesRejectsPlaceholderNPMScript(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"echo \\\"Error: no test specified\\\" && exit 1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package-lock.json"), []byte(`{"lockfileVersion":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	candidates, err := discoverCandidateDetails(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("placeholder script candidates = %#v", candidates)
	}
}

func TestBuildInputsSupportsOnlyPinnedPlatformRuntimes(t *testing.T) {
	goImage := "registry.example/mendry/go@sha256:" + strings.Repeat("a", 64)
	catalog, err := NewToolchainCatalog([]Toolchain{{
		ID: "go-1.23-default", Runtime: "go", Version: "1.23", Profile: "default", ImageDigest: goImage, Default: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	preparer := &Preparer{toolchains: catalog}
	toolchain, argv := preparer.buildInputs("go", []byte("module example\ngo 1.23.0\n"))
	if toolchain.ImageDigest != goImage || strings.Join(argv, " ") != "go test ./..." {
		t.Fatalf("go inputs = %#v %#v", toolchain, argv)
	}
	toolchain, argv = preparer.buildInputs("go", []byte("module example\ngo 1.24\n"))
	if toolchain.ImageDigest != "" || argv != nil {
		t.Fatalf("unsupported go inputs = %#v %#v", toolchain, argv)
	}
	if imageBaseAllowed("golang:1.23-bookworm", "go") {
		t.Fatal("mutable Go image tag was accepted")
	}
	if !imageBaseAllowed(goImage, "go") {
		t.Fatal("immutable Go image was rejected")
	}
}

func TestWorkspacePreparationProblemMapsStableCodesWithoutCause(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "git unavailable",
			err:  &remediationdomain.LifecycleRuntimeError{Code: "workspace_git_unavailable", Cause: errors.New("token secret and remote output")},
			want: "the repository could not be cloned with the configured credential",
		},
		{
			name: "unsafe tree entry",
			err:  &remediationdomain.LifecycleRuntimeError{Code: "workspace_baseline_unsafe", Cause: errors.New("baseline symlink, submodule, or special file is prohibited")},
			want: "the deployed revision contains a symlink, submodule, or special file",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mapped := workspacePreparationProblem(test.err)
			if mapped.Error() != test.want || strings.Contains(mapped.Error(), "token") {
				t.Fatalf("mapped error = %q", mapped)
			}
		})
	}
}

func TestCopyBuildContextOmitsRepositoryIgnoreAndGitMetadata(t *testing.T) {
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{".dockerignore": "*\n", "Dockerfile": "FROM scratch\n", "go.mod": "module example\n"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	contextRoot, err := copyBuildContext(source)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(contextRoot)
	if _, err := os.Stat(filepath.Join(contextRoot, ".dockerignore")); !os.IsNotExist(err) {
		t.Fatalf(".dockerignore was copied, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(contextRoot, ".git")); !os.IsNotExist(err) {
		t.Fatalf(".git was copied, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(contextRoot, "Dockerfile")); err != nil {
		t.Fatalf("repository Dockerfile should remain data, err=%v", err)
	}
}

func TestGeneratedDockerfileDoesNotUseRepositoryDockerfileOrShellDirectory(t *testing.T) {
	base := "registry.example/mendry/node@sha256:" + strings.Repeat("b", 64)
	dockerfile := generatedDockerfile(base, "services/web", "node")
	for _, expected := range []string{"FROM " + base, "COPY services/web/package.json services/web/package-lock.json ./", "WORKDIR /workspace/services/web", "npm ci --ignore-scripts --no-audit --no-fund", "NODE_PATH=/opt/mendry/node_modules"} {
		if !strings.Contains(dockerfile, expected) {
			t.Fatalf("dockerfile missing %q:\n%s", expected, dockerfile)
		}
	}
	if strings.Contains(dockerfile, "COPY . .") || strings.Contains(dockerfile, "Dockerfile") || strings.Contains(dockerfile, "cd services/web") {
		t.Fatalf("dockerfile executes repository instructions:\n%s", dockerfile)
	}
}

func TestBuildCacheTagTracksEveryPreparationIdentityInput(t *testing.T) {
	base := Toolchain{ID: "node-22-default", ImageDigest: "sha256:" + strings.Repeat("a", 64)}
	original := buildCacheTag(base, "services/web", strings.Repeat("1", 40), "lock-a")
	variants := []string{
		buildCacheTag(Toolchain{ID: "node-22-chromium", ImageDigest: base.ImageDigest}, "services/web", strings.Repeat("1", 40), "lock-a"),
		buildCacheTag(Toolchain{ID: base.ID, ImageDigest: "sha256:" + strings.Repeat("b", 64)}, "services/web", strings.Repeat("1", 40), "lock-a"),
		buildCacheTag(base, "services/api", strings.Repeat("1", 40), "lock-a"),
		buildCacheTag(base, "services/web", strings.Repeat("2", 40), "lock-a"),
		buildCacheTag(base, "services/web", strings.Repeat("1", 40), "lock-b"),
	}
	for _, variant := range variants {
		if variant == original {
			t.Fatalf("cache identity collision: %q", original)
		}
	}
}

func TestValidateIndependentServiceRejectsSharedLayouts(t *testing.T) {
	root := t.TempDir()
	service := filepath.Join(root, "services", "web")
	if err := os.MkdirAll(service, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"workspaces":["packages/*"]}`)
	if err := os.WriteFile(filepath.Join(service, "package-lock.json"), []byte(`{"lockfileVersion":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateIndependentService(root, "services/web", "node", manifest); err == nil || !strings.Contains(err.Error(), "workspaces") {
		t.Fatalf("shared workspace error = %v", err)
	}
}

func TestProtectedDependencyPathsAreScopedToService(t *testing.T) {
	got := protectedDependencyPaths("services/web", "node")
	for _, expected := range []string{"services/web/package.json", "services/web/package-lock.json", "services/web/.nvmrc"} {
		found := false
		for _, path := range got {
			found = found || path == expected
		}
		if !found {
			t.Fatalf("protected paths %#v missing %q", got, expected)
		}
	}
}
