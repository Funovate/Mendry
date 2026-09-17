package preparation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	projectapplication "mendry/backend/internal/modules/projects/application"
	projectdomain "mendry/backend/internal/modules/projects/domain"
	remediationgit "mendry/backend/internal/modules/remediation/adapter/git"
	remediationdomain "mendry/backend/internal/modules/remediation/domain"
)

const (
	maxDiscoveryDepth = 4
	maxDiscoveryFiles = 20000
	maxManifestBytes  = 2 << 20
	defaultBuildLimit = 15 * time.Minute
	buildPlanVersion  = "dependency-image-v2"
)

var (
	imageIDPattern        = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	immutableImagePattern = regexp.MustCompile(`^(?:[^@\s]+@)?sha256:[0-9a-f]{64}$`)
	goVersionPattern      = regexp.MustCompile(`^1\.(21|22|23)(?:\.0)?$`)
)

// Options wires the platform-owned workspace and validation runner. The runner
// remains the only component allowed to execute repository tests.
type Options struct {
	Workspace        *remediationgit.GitWorkspace
	Validation       remediationdomain.ValidationPort
	DockerCommand    string
	BuildTimeout     time.Duration
	GoBuilderImage   string
	NodeBuilderImage string
	Toolchains       *ToolchainCatalog
}

// Preparer discovers a supported project, builds a platform-generated image,
// and runs a baseline check in the existing network-disabled validation runner.
type Preparer struct {
	workspace     *remediationgit.GitWorkspace
	validation    remediationdomain.ValidationPort
	dockerCommand string
	buildTimeout  time.Duration
	toolchains    ToolchainCatalog
}

func NewPreparer(options Options) (*Preparer, error) {
	if options.Workspace == nil || options.Validation == nil {
		return nil, fmt.Errorf("hotfix preparation workspace and validation runner are required")
	}
	command := strings.TrimSpace(options.DockerCommand)
	if command == "" {
		command = "docker"
	}
	timeout := options.BuildTimeout
	if timeout <= 0 {
		timeout = defaultBuildLimit
	}
	catalog := ToolchainCatalog{}
	if options.Toolchains != nil {
		catalog = *options.Toolchains
	} else {
		var err error
		catalog, err = legacyToolchainCatalog(options.GoBuilderImage, options.NodeBuilderImage)
		if err != nil {
			return nil, err
		}
	}
	return &Preparer{
		workspace: options.Workspace, validation: options.Validation, dockerCommand: command, buildTimeout: timeout,
		toolchains: catalog,
	}, nil
}

var _ projectapplication.HotfixPreparationPort = (*Preparer)(nil)

func (p *Preparer) Prepare(ctx context.Context, request projectapplication.HotfixPreparationRequest, progress func(string)) (projectapplication.HotfixPreparationResult, error) {
	if p == nil || p.workspace == nil || p.validation == nil {
		return projectapplication.HotfixPreparationResult{}, projectapplication.ErrHotfixUnavailable
	}
	if err := ctx.Err(); err != nil {
		return projectapplication.HotfixPreparationResult{}, err
	}
	if request.ProjectID == "" || request.CheckID == "" || request.Repository.DeployedCommit == "" {
		return projectapplication.HotfixPreparationResult{}, preparationProblem("repository baseline is invalid")
	}
	if !fullObjectID(request.Repository.DeployedCommit) {
		return projectapplication.HotfixPreparationResult{}, preparationProblem("the deployed commit must be a full commit id")
	}
	if progress != nil {
		progress("Fetching the configured deployed revision")
	}
	profile := remediationdomain.ExecutionProfileSnapshot{
		ImageDigest:       "sha256:" + strings.Repeat("0", 64),
		WorkingDirectory:  ".",
		CPULimit:          2,
		MemoryLimitMiB:    4096,
		WorkspaceLimitMiB: 10240,
	}
	workspaceRequest := remediationdomain.WorkspaceRequest{
		RunID:          "hotfix-check-" + request.CheckID,
		ProjectID:      request.ProjectID,
		BaselineCommit: request.Repository.DeployedCommit,
		IdempotencyKey: "hotfix-check-" + request.CheckID,
		Profile:        profile,
		Repository: remediationdomain.PublicationSnapshot{
			RemoteURL:                    request.Repository.RemoteURL,
			SCMProvider:                  request.Repository.SCMProvider,
			Transport:                    request.Repository.Transport,
			ProductionBranch:             request.Repository.ProductionBranch,
			RepositoryCredentialSecretID: request.RepositoryCredential.ID,
			RepositoryCredentialVersion:  request.RepositoryCredential.Version,
			GitCredentialSecretID:        request.GitCredential.ID,
			GitCredentialVersion:         request.GitCredential.Version,
		},
		ChangePolicy: remediationdomain.ChangePolicySnapshot{
			AllowedPaths: []string{"**"}, MaxChangedFiles: 10, MaxChangedLines: 400,
		},
	}
	identity, err := p.workspace.Ensure(ctx, workspaceRequest)
	if err != nil {
		return projectapplication.HotfixPreparationResult{}, workspacePreparationProblem(err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = p.workspace.Destroy(cleanupCtx, identity)
	}()
	mount, err := p.workspace.ResolveWorkspaceMount(ctx, workspaceRequest.RunID, identity.WorkspaceID, identity.CurrentTreeHash)
	if err != nil {
		return projectapplication.HotfixPreparationResult{}, preparationProblem("the isolated workspace could not be opened")
	}
	if err := p.workspace.CheckPublicationAccess(ctx, workspaceRequest, identity); err != nil {
		return projectapplication.HotfixPreparationResult{}, preparationProblem("the configured Git credential could not push a hotfix branch")
	}
	candidates, err := discoverCandidates(mount)
	if err != nil {
		return projectapplication.HotfixPreparationResult{}, preparationProblem("the repository layout could not be inspected")
	}
	if len(candidates) == 0 {
		return projectapplication.HotfixPreparationResult{}, preparationProblem("no supported Go or locked npm test project was detected")
	}
	if request.Directory == "" && len(candidates) != 1 {
		return projectapplication.HotfixPreparationResult{
			Candidates:     candidates,
			BaselineCommit: request.Repository.DeployedCommit,
		}, nil
	}
	selected, err := chooseCandidate(candidates, request.Directory)
	if err != nil {
		return projectapplication.HotfixPreparationResult{
			Candidates:     candidates,
			BaselineCommit: request.Repository.DeployedCommit,
		}, err
	}
	if progress != nil {
		progress("Building the platform validation image")
	}
	kind, manifest, err := readCandidateManifest(mount, selected)
	if err != nil {
		return projectapplication.HotfixPreparationResult{}, preparationProblem("the selected project manifest could not be read")
	}
	if err := validateIndependentService(mount, selected.Directory, kind, manifest); err != nil {
		return projectapplication.HotfixPreparationResult{}, err
	}
	toolchain, command := p.buildInputs(kind, manifest)
	if toolchain.ImageDigest == "" || len(command) == 0 {
		return projectapplication.HotfixPreparationResult{}, preparationProblem("the selected project does not have a supported test command or approved toolchain")
	}
	imageID, lockHash, err := p.buildImage(ctx, mount, selected.Directory, request.Repository.DeployedCommit, toolchain, kind)
	if err != nil {
		return projectapplication.HotfixPreparationResult{}, err
	}
	profile.ImageDigest = imageID
	profile.WorkingDirectory = selected.Directory
	profile.RequiredCommands = []remediationdomain.ValidationCommandSnapshot{{
		ID: "baseline", Version: 1, Argv: command, TimeoutSeconds: 900,
	}}
	if progress != nil {
		progress("Running the baseline validation")
	}
	result, err := p.validation.Run(ctx, remediationdomain.ValidationRequest{
		RunID:       "hotfix-check-" + request.CheckID,
		WorkspaceID: identity.WorkspaceID,
		CommandID:   "baseline", CommandVersion: 1,
		ExpectedTreeHash: identity.CurrentTreeHash,
		IdempotencyKey:   "hotfix-baseline-" + request.CheckID,
		Profile:          profile,
	})
	if err != nil || !result.Passed {
		return projectapplication.HotfixPreparationResult{}, preparationProblem("baseline validation did not pass")
	}
	policy := projectdomain.DefaultRemediationPolicy()
	policy.AgentLoopMode = projectdomain.AgentLoopModeResilientV1
	policy.ExecutionMode = projectdomain.RemediationExecutionAutoHotfix
	policy.ChangePolicy.DeniedPaths = protectedDependencyPaths(selected.Directory, kind)
	enhancedValidation := true
	policy.ValidationProfile = projectdomain.ValidationProfile{
		Enabled: &enhancedValidation, ImageDigest: imageID, WorkingDirectory: selected.Directory,
		PreparedCommit: request.Repository.DeployedCommit, ToolchainID: toolchain.ID,
		BuildPlanVersion: buildPlanVersion, DependencyHash: lockHash,
		Preparation:      []projectdomain.ValidationCommand{},
		RequiredCommands: []projectdomain.ValidationCommand{{ID: "baseline", Version: 1, Argv: command, TimeoutSeconds: 900}},
		CPULimit:         profile.CPULimit, MemoryLimitMiB: profile.MemoryLimitMiB, WorkspaceLimitMiB: profile.WorkspaceLimitMiB,
	}
	return projectapplication.HotfixPreparationResult{
		Candidates: candidates, Directory: selected.Directory, Runtime: selected.Runtime,
		BaselineCommit:    request.Repository.DeployedCommit,
		ValidationSummary: "Baseline validation passed in the generated isolated image.",
		BranchOnly:        request.Repository.SCMProvider == "yunxiao" || request.Repository.SCMProvider == "generic",
		Policy:            policy,
	}, nil
}

type candidate struct {
	Directory string
	Runtime   string
	Manifest  string
}

func discoverCandidates(root string) ([]projectapplication.HotfixCandidate, error) {
	found, err := discoverCandidateDetails(root)
	if err != nil {
		return nil, err
	}
	out := make([]projectapplication.HotfixCandidate, 0, len(found))
	for _, item := range found {
		out = append(out, projectapplication.HotfixCandidate{Directory: item.Directory, Runtime: item.Runtime})
	}
	return out, nil
}

func discoverCandidateDetails(root string) ([]candidate, error) {
	var found []candidate
	files := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			rel, _ := filepath.Rel(root, path)
			depth := 0
			if rel != "." {
				depth = strings.Count(filepath.ToSlash(rel), "/") + 1
			}
			if depth > maxDiscoveryDepth || (rel != "." && isIgnoredDirectory(entry.Name())) {
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		files++
		if files > maxDiscoveryFiles {
			return errors.New("repository file count exceeds discovery limit")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, path := range []string{root} {
		dirs = append(dirs, path)
	}
	walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		if path != root && isIgnoredDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		if path == root {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if strings.Count(filepath.ToSlash(rel), "/")+1 > maxDiscoveryDepth {
			return filepath.SkipDir
		}
		dirs = append(dirs, path)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Strings(dirs)
	seen := map[string]bool{}
	for _, dir := range dirs {
		rel, _ := filepath.Rel(root, dir)
		display := filepath.ToSlash(rel)
		if display == "." {
			display = "."
		}
		if !safeDirectory(display) {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, sumErr := os.Stat(filepath.Join(dir, "go.sum")); sumErr == nil && hasTestFiles(dir) && hasSupportedGoModule(filepath.Join(dir, "go.mod")) {
				if !seen[display] {
					found = append(found, candidate{Directory: display, Runtime: "go", Manifest: "go.mod"})
					seen[display] = true
				}
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
			lock := firstExisting(dir, "package-lock.json", "pnpm-lock.yaml", "yarn.lock")
			if lock == "package-lock.json" && hasRealNPMScript(filepath.Join(dir, "package.json")) && !seen[display] {
				found = append(found, candidate{Directory: display, Runtime: "node", Manifest: lock})
				seen[display] = true
			}
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Directory < found[j].Directory })
	return found, nil
}

func isIgnoredDirectory(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "build", "target", ".next", ".cache":
		return true
	default:
		return false
	}
}

func hasSupportedGoModule(path string) bool {
	content, err := os.ReadFile(path)
	if err != nil || len(content) > maxManifestBytes {
		return false
	}
	return supportedGoVersion(content)
}

func supportedGoVersion(manifest []byte) bool {
	return goVersion(manifest) != ""
}

func goVersion(manifest []byte) string {
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "go" && goVersionPattern.MatchString(fields[1]) {
			return strings.TrimSuffix(fields[1], ".0")
		}
	}
	return ""
}

func nodeVersion(manifest []byte) (string, bool) {
	var value struct {
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
	}
	if err := json.Unmarshal(manifest, &value); err != nil {
		return "", false
	}
	return strings.TrimSpace(value.Engines.Node), true
}

func hasTestFiles(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if entry.IsDir() && path != root && isIgnoredDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), "_test.go") {
			found = true
		}
		return nil
	})
	return found
}

func hasRealNPMScript(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	var value struct {
		Scripts map[string]string `json:"scripts"`
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxManifestBytes))
	if decoder.Decode(&value) != nil {
		return false
	}
	test := strings.TrimSpace(value.Scripts["test"])
	if test == "" {
		return false
	}
	lower := strings.ToLower(test)
	return !strings.Contains(lower, "no test specified") && lower != "echo test" && lower != "exit 0" && lower != "true" && lower != "/bin/true"
}

func firstExisting(root string, names ...string) string {
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			return name
		}
	}
	return ""
}

func safeDirectory(directory string) bool {
	if directory == "." {
		return true
	}
	if directory == "" || filepath.IsAbs(directory) || filepath.ToSlash(filepath.Clean(directory)) != directory || strings.HasPrefix(directory, "../") || strings.ContainsAny(directory, "\\\"\r\n\t #") {
		return false
	}
	for _, r := range directory {
		if r < 0x20 {
			return false
		}
	}
	return true
}

func chooseCandidate(candidates []projectapplication.HotfixCandidate, directory string) (candidate, error) {
	if directory != "" {
		for _, item := range candidates {
			if item.Directory == filepath.ToSlash(filepath.Clean(directory)) {
				return candidate{Directory: item.Directory, Runtime: item.Runtime, Manifest: ""}, nil
			}
		}
		return candidate{}, preparationProblem("the selected service directory was not detected")
	}
	if len(candidates) != 1 {
		return candidate{}, nil
	}
	return candidate{Directory: candidates[0].Directory, Runtime: candidates[0].Runtime}, nil
}

func readCandidateManifest(root string, item candidate) (string, []byte, error) {
	name := "go.mod"
	if item.Runtime == "node" {
		name = "package.json"
	}
	path := filepath.Join(root, filepath.FromSlash(item.Directory), name)
	file, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	if err != nil || len(content) > maxManifestBytes {
		return "", nil, errors.New("manifest is too large")
	}
	return item.Runtime, content, nil
}

func validateIndependentService(root, directory, kind string, manifest []byte) error {
	serviceRoot := filepath.Join(root, filepath.FromSlash(directory))
	switch kind {
	case "go":
		if _, err := os.Stat(filepath.Join(serviceRoot, "go.sum")); err != nil {
			return preparationProblem("the selected Go service must have its own go.sum")
		}
		for current := serviceRoot; ; current = filepath.Dir(current) {
			if _, err := os.Stat(filepath.Join(current, "go.work")); err == nil {
				return preparationProblem("shared Go workspaces are not supported for automatic repair yet")
			}
			if current == root || filepath.Dir(current) == current {
				break
			}
		}
		for _, line := range strings.Split(string(manifest), "\n") {
			fields := strings.Fields(line)
			for index, field := range fields {
				if field == "=>" && index+1 < len(fields) && (fields[index+1] == ".." || strings.HasPrefix(fields[index+1], "../")) {
					return preparationProblem("Go replacements outside the selected service are not supported")
				}
			}
		}
	case "node":
		var value struct {
			Workspaces json.RawMessage `json:"workspaces"`
		}
		if json.Unmarshal(manifest, &value) != nil {
			return preparationProblem("the selected Node manifest is invalid")
		}
		if len(value.Workspaces) > 0 && string(value.Workspaces) != "null" {
			return preparationProblem("npm workspaces are not supported for automatic repair yet")
		}
		lock, err := os.ReadFile(filepath.Join(serviceRoot, "package-lock.json"))
		if err != nil {
			return preparationProblem("the selected Node service must have its own package-lock.json")
		}
		if strings.Contains(string(lock), `"workspace:`) || strings.Contains(string(lock), `"file:..`) {
			return preparationProblem("Node dependencies outside the selected service are not supported")
		}
	default:
		return preparationProblem("the selected project runtime is not supported")
	}
	return nil
}

func protectedDependencyPaths(directory, kind string) []string {
	prefix := ""
	if directory != "." {
		prefix = strings.Trim(directory, "/") + "/"
	}
	if kind == "go" {
		return []string{prefix + "go.mod", prefix + "go.sum", prefix + "go.work", prefix + "go.work.sum"}
	}
	return []string{
		prefix + "package.json", prefix + "package-lock.json", prefix + ".nvmrc",
		prefix + ".node-version", prefix + "volta.json",
	}
}

func (p *Preparer) buildInputs(kind string, manifest []byte) (Toolchain, []string) {
	switch kind {
	case "go":
		version := goVersion(manifest)
		if version == "" {
			return Toolchain{}, nil
		}
		toolchain, ok := p.toolchains.resolve(kind, version, "default")
		if !ok {
			return Toolchain{}, nil
		}
		return toolchain, []string{"go", "test", "./..."}
	case "node":
		version, ok := nodeVersion(manifest)
		if !ok {
			return Toolchain{}, nil
		}
		toolchain, found := p.toolchains.resolve(kind, version, "default")
		if !found {
			return Toolchain{}, nil
		}
		return toolchain, []string{"npm", "test"}
	default:
		return Toolchain{}, nil
	}
}

func (p *Preparer) buildImage(ctx context.Context, root, directory, deployedCommit string, toolchain Toolchain, kind string) (string, string, error) {
	baseImage := toolchain.ImageDigest
	if !imageBaseAllowed(baseImage, kind) {
		return "", "", preparationProblem("the detected runtime is not supported")
	}
	lockHash, err := dependencyHash(root, directory, kind)
	if err != nil {
		return "", "", preparationProblem("the selected project dependency files could not be read")
	}
	tag := buildCacheTag(toolchain, directory, deployedCommit, lockHash)
	buildCtx, cancel := context.WithTimeout(ctx, p.buildTimeout)
	defer cancel()
	if imageID, ok := p.inspectImage(buildCtx, tag); ok {
		return imageID, lockHash, nil
	}
	buildRoot, err := copyBuildContext(root)
	if err != nil {
		return "", "", preparationProblem("the platform build context is unavailable")
	}
	defer os.RemoveAll(buildRoot)
	temporary, err := os.CreateTemp("", "mendry-hotfix-dockerfile-*")
	if err != nil {
		return "", "", preparationProblem("the platform build context is unavailable")
	}
	path := temporary.Name()
	defer os.Remove(path)
	content := generatedDockerfile(baseImage, directory, kind)
	if _, err := temporary.WriteString(content); err != nil || temporary.Close() != nil {
		return "", "", preparationProblem("the platform Dockerfile could not be created")
	}
	cmd := exec.CommandContext(buildCtx, p.dockerCommand, "build", "--pull", "--file", path,
		"--label", "mendry.managed=validation-image",
		"--label", "mendry.build-plan="+buildPlanVersion,
		"--label", "mendry.toolchain="+toolchain.ID,
		"--tag", tag, buildRoot)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", "", preparationProblem("the generated validation image could not be built")
	}
	imageID, ok := p.inspectImage(buildCtx, tag)
	if !ok {
		return "", "", preparationProblem("the generated validation image did not have an immutable id")
	}
	return imageID, lockHash, nil
}

func buildCacheTag(toolchain Toolchain, directory, deployedCommit, lockHash string) string {
	identity := sha256.Sum256([]byte(strings.Join([]string{
		buildPlanVersion, toolchain.ID, toolchain.ImageDigest, directory, deployedCommit, lockHash,
	}, "\x00")))
	return "mendry-validation-cache:" + hex.EncodeToString(identity[:16])
}

func (p *Preparer) inspectImage(ctx context.Context, reference string) (string, bool) {
	inspect := exec.CommandContext(ctx, p.dockerCommand, "image", "inspect", "--format", "{{.Id}}", reference)
	output, err := inspect.Output()
	if err != nil {
		return "", false
	}
	imageID := strings.TrimSpace(string(output))
	return imageID, imageIDPattern.MatchString(imageID)
}

func dependencyHash(root, directory, kind string) (string, error) {
	names := []string{"go.mod", "go.sum"}
	if kind == "node" {
		names = []string{"package.json", "package-lock.json"}
	}
	hash := sha256.New()
	for _, name := range names {
		path := filepath.Join(root, filepath.FromSlash(directory), name)
		content, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) && name == "go.sum" {
				continue
			}
			return "", err
		}
		_, _ = hash.Write([]byte(name + "\x00"))
		_, _ = hash.Write(content)
		_, _ = hash.Write([]byte("\x00"))
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyBuildContext(source string) (string, error) {
	target, err := os.MkdirTemp("", "mendry-hotfix-build-context-*")
	if err != nil {
		return "", err
	}
	cleanup := func(copyErr error) (string, error) {
		if copyErr != nil {
			_ = os.RemoveAll(target)
			return "", copyErr
		}
		return target, nil
	}
	err = filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.Name() == ".git" || entry.Name() == ".dockerignore" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		destination := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			link, linkErr := os.Readlink(path)
			cleanTarget := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(rel), link)))
			if linkErr != nil || filepath.IsAbs(link) || cleanTarget == ".." || strings.HasPrefix(cleanTarget, "../") {
				return fmt.Errorf("build context contains an unsafe symlink")
			}
			if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
				return err
			}
			return os.Symlink(link, destination)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("build context contains a non-regular file")
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeInputErr := input.Close()
		closeOutputErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeInputErr != nil {
			return closeInputErr
		}
		return closeOutputErr
	})
	return cleanup(err)
}

func imageBaseAllowed(base, kind string) bool {
	return (kind == "go" || kind == "node") && immutableImagePattern.MatchString(base)
}

func generatedDockerfile(base, directory, kind string) string {
	workdir := "/workspace"
	if directory != "." {
		workdir += "/" + strings.Trim(directory, "/")
	}
	prefix := ""
	if directory != "." {
		prefix = strings.Trim(directory, "/") + "/"
	}
	lines := []string{"FROM " + base}
	switch kind {
	case "go":
		lines = append(lines,
			"WORKDIR /opt/mendry/dependencies",
			"COPY "+prefix+"go.mod "+prefix+"go.sum ./",
			"RUN go mod download",
		)
	case "node":
		lines = append(lines,
			"WORKDIR /opt/mendry",
			"COPY "+prefix+"package.json "+prefix+"package-lock.json ./",
			"RUN npm ci --ignore-scripts --no-audit --no-fund",
			"ENV PATH=/opt/mendry/node_modules/.bin:$PATH",
			"ENV NODE_PATH=/opt/mendry/node_modules",
			"RUN ln -s /opt/mendry/node_modules /node_modules",
		)
	}
	lines = append(lines, "WORKDIR "+workdir)
	return strings.Join(lines, "\n") + "\n"
}

func workspacePreparationProblem(err error) error {
	var runtimeError *remediationdomain.LifecycleRuntimeError
	if !errors.As(err, &runtimeError) {
		return preparationProblem("the deployed revision could not be isolated")
	}
	switch runtimeError.Code {
	case "repository_credential_unavailable":
		return preparationProblem("the repository credential could not be loaded")
	case "workspace_git_unavailable":
		return preparationProblem("the repository could not be cloned with the configured credential")
	case "workspace_repository_invalid":
		return preparationProblem("the configured repository is not a valid Git repository")
	case "workspace_baseline_unavailable", "workspace_baseline_mismatch":
		return preparationProblem("the configured deployed commit was not found in the repository")
	case "workspace_baseline_unsafe":
		if runtimeError.Cause != nil {
			switch runtimeError.Cause.Error() {
			case "baseline symlink, submodule, or special file is prohibited":
				return preparationProblem("the deployed revision contains a symlink, submodule, or special file")
			case "baseline exceeds the workspace limit", "baseline leaves insufficient workspace headroom":
				return preparationProblem("the deployed revision exceeds the automatic repair workspace limit")
			case "baseline tree exceeds its metadata limit":
				return preparationProblem("the deployed revision contains too much tree metadata")
			}
		}
		return preparationProblem("the configured deployed commit did not pass repository safety checks")
	case "workspace_size_limit":
		return preparationProblem("the repository exceeds the automatic repair workspace limit")
	default:
		return preparationProblem("the deployed revision could not be isolated")
	}
}

func preparationProblem(message string) error {
	return &projectapplication.HotfixSetupProblem{Message: message}
}

func fullObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

// Keep this check close to image selection so a new runtime cannot silently
// expand the set of executable build bases.
