package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	authdomain "mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/modules/projects/domain"
)

var ErrHotfixUnavailable = errors.New("automatic hotfix setup is unavailable")

type HotfixSetupProblem struct{ Message string }

func (e *HotfixSetupProblem) Error() string { return e.Message }

// HotfixPolicyCommitter atomically rejects stale repository, credential and policy versions.
type HotfixPolicyCommitter interface {
	CommitPreparedHotfix(context.Context, string, HotfixPreparationRequest, domain.RemediationPolicy, domain.RemediationPolicy) (domain.RemediationPolicy, error)
}

type HotfixCheck struct {
	ID                string            `json:"id"`
	Status            string            `json:"status"`
	Message           string            `json:"message"`
	Candidates        []HotfixCandidate `json:"candidates"`
	Directory         string            `json:"directory"`
	Runtime           string            `json:"runtime"`
	BaselineCommit    string            `json:"baselineCommit"`
	ValidationSummary string            `json:"validationSummary"`
	BranchOnly        bool              `json:"branchOnly"`
	CredentialName    string            `json:"credentialName"`
	ExpiresAt         time.Time         `json:"expiresAt"`
}

type HotfixPreparationJob struct {
	ProjectID    string
	TargetCommit string
	Directory    string
}

type HotfixPreparationJobStore interface {
	Enqueue(context.Context, HotfixPreparationJob) error
	Recover(context.Context, int) ([]HotfixPreparationJob, error)
	Queued(context.Context, int) ([]HotfixPreparationJob, error)
	MarkRunning(context.Context, string, string) error
	MarkFinished(context.Context, string, string, bool) error
}

type hotfixCheckJob struct {
	view        HotfixCheck
	request     HotfixPreparationRequest
	original    domain.RemediationPolicy
	policy      domain.RemediationPolicy
	fingerprint [32]byte
	autoEnable  bool
}

// HotfixSetup keeps bounded preflight jobs and optionally mirrors automatic
// rebuilds to a durable store for restart recovery.
type HotfixSetup struct {
	service   *Service
	preparer  HotfixPreparationPort
	committer HotfixPolicyCommitter
	jobStore  HotfixPreparationJobStore
	lifetime  context.Context
	mu        sync.Mutex
	jobs      map[string]*hotfixCheckJob
	slots     chan struct{}
}

func NewHotfixSetup(ctx context.Context, service *Service, preparer HotfixPreparationPort, committer HotfixPolicyCommitter) *HotfixSetup {
	return &HotfixSetup{service: service, preparer: preparer, committer: committer, lifetime: ctx, jobs: make(map[string]*hotfixCheckJob), slots: make(chan struct{}, 2)}
}

func (s *HotfixSetup) SetJobStore(store HotfixPreparationJobStore) {
	if s != nil {
		s.jobStore = store
	}
}

func (s *HotfixSetup) Recover(ctx context.Context) error {
	if s == nil || s.jobStore == nil {
		return nil
	}
	jobs, err := s.jobStore.Recover(ctx, 128)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		s.scheduleAutomatic(ctx, job.ProjectID, job.TargetCommit, job.Directory)
	}
	return nil
}

func (s *HotfixSetup) RepositoryUpdated(ctx context.Context, projectID string, previous, current domain.Repository, policy domain.RemediationPolicy) {
	if s == nil || s.preparer == nil || s.committer == nil || policy.ExecutionMode != domain.RemediationExecutionAutoHotfix ||
		(policy.ValidationProfile.Enabled != nil && !*policy.ValidationProfile.Enabled) ||
		previous.DeployedCommit == current.DeployedCommit || policy.ValidationProfile.WorkingDirectory == "" {
		return
	}
	job := HotfixPreparationJob{ProjectID: projectID, TargetCommit: current.DeployedCommit, Directory: policy.ValidationProfile.WorkingDirectory}
	if s.jobStore != nil && s.jobStore.Enqueue(ctx, job) != nil {
		return
	}
	s.scheduleAutomatic(ctx, projectID, current.DeployedCommit, policy.ValidationProfile.WorkingDirectory)
}

func (s *HotfixSetup) scheduleAutomatic(ctx context.Context, projectID, targetCommit, directory string) {
	request, original, fingerprint, err := s.inputs(ctx, projectID, directory)
	if err != nil || request.Repository.DeployedCommit != targetCommit {
		if s.jobStore != nil {
			_ = s.jobStore.MarkFinished(ctx, projectID, targetCommit, false)
		}
		return
	}
	s.mu.Lock()
	if running := s.jobs[projectID]; running != nil && (running.view.Status == "checking" || running.view.Status == "enabling") {
		s.mu.Unlock()
		return
	}
	if len(s.jobs) >= 128 {
		s.mu.Unlock()
		return
	}
	id, err := s.service.newID()
	if err != nil {
		s.mu.Unlock()
		return
	}
	request.CheckID = id
	job := &hotfixCheckJob{
		view:    HotfixCheck{ID: id, Status: "checking", Message: "Rebuilding validation for the deployed revision", Candidates: []HotfixCandidate{}, CredentialName: request.GitCredential.Name, ExpiresAt: time.Now().Add(30 * time.Minute)},
		request: request, original: original, fingerprint: fingerprint, autoEnable: true,
	}
	s.jobs[projectID] = job
	s.mu.Unlock()
	go s.prepareWhenAvailable(projectID, job)
}

func (s *HotfixSetup) prepareWhenAvailable(projectID string, job *hotfixCheckJob) {
	select {
	case s.slots <- struct{}{}:
		if job.autoEnable && s.jobStore != nil {
			if err := s.jobStore.MarkRunning(s.lifetime, projectID, job.request.Repository.DeployedCommit); err != nil {
				<-s.slots
				s.mu.Lock()
				job.view.Status, job.view.Message = "blocked", "Validation rebuild could not be claimed"
				s.mu.Unlock()
				return
			}
		}
		s.prepare(projectID, job)
	case <-s.lifetime.Done():
		s.mu.Lock()
		job.view.Status, job.view.Message = "blocked", "Validation rebuild stopped before it could start"
		s.mu.Unlock()
	}
}

func (s *HotfixSetup) Check(ctx context.Context, principal authdomain.User, key, directory string) (HotfixCheck, error) {
	project, err := s.service.resolveProject(ctx, principal, key)
	if err != nil {
		return HotfixCheck{}, err
	}
	if s.preparer == nil || s.committer == nil {
		return HotfixCheck{}, ErrHotfixUnavailable
	}
	directory = strings.TrimSpace(directory)
	if len(directory) > 512 || strings.ContainsAny(directory, "\x00\\") || strings.HasPrefix(directory, "/") {
		return HotfixCheck{}, ErrInvalidInput
	}
	request, original, fingerprint, err := s.inputs(ctx, project.ID, directory)
	if err != nil {
		return HotfixCheck{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, job := range s.jobs {
		if job.view.Status != "checking" && now.After(job.view.ExpiresAt) {
			delete(s.jobs, id)
		}
	}
	if previous := s.jobs[project.ID]; previous != nil && (previous.view.Status == "checking" || previous.view.Status == "enabling") {
		return copyHotfixCheck(previous.view), nil
	}
	if len(s.jobs) >= 128 {
		return HotfixCheck{}, ErrConflict
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return HotfixCheck{}, ErrConflict
	}
	id, err := s.service.newID()
	if err != nil {
		<-s.slots
		return HotfixCheck{}, err
	}
	request.CheckID = id
	job := &hotfixCheckJob{view: HotfixCheck{ID: id, Status: "checking", Message: "Inspecting repository and preparing validation", Candidates: []HotfixCandidate{}, CredentialName: request.GitCredential.Name, ExpiresAt: now.Add(30 * time.Minute)}, request: request, original: original, fingerprint: fingerprint}
	s.jobs[project.ID] = job
	go s.prepare(project.ID, job)
	return copyHotfixCheck(job.view), nil
}

func (s *HotfixSetup) prepare(projectID string, job *hotfixCheckJob) {
	defer func() { <-s.slots }()
	if job.autoEnable && s.jobStore != nil {
		defer func() {
			s.mu.Lock()
			succeeded := job.view.Status == "enabled"
			s.mu.Unlock()
			finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer finishCancel()
			if err := s.jobStore.MarkFinished(finishCtx, projectID, job.request.Repository.DeployedCommit, succeeded); err != nil {
				if queued, listErr := s.jobStore.Queued(finishCtx, 128); listErr == nil {
					for _, next := range queued {
						s.scheduleAutomatic(finishCtx, next.ProjectID, next.TargetCommit, next.Directory)
					}
				}
			}
		}()
	}
	ctx, cancel := context.WithTimeout(s.lifetime, 15*time.Minute)
	defer cancel()
	result, err := s.preparer.Prepare(ctx, job.request, func(message string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		job.view.Message = message
	})
	s.mu.Lock()
	job.view.ExpiresAt = time.Now().Add(15 * time.Minute)
	if err != nil {
		job.view.Status = "blocked"
		job.view.Message = err.Error() // Port errors are safe actionable messages, never raw command output.
		s.mu.Unlock()
		return
	}
	job.view.Candidates = append([]HotfixCandidate{}, result.Candidates...)
	job.view.Directory, job.view.Runtime = result.Directory, result.Runtime
	job.view.BaselineCommit, job.view.ValidationSummary, job.view.BranchOnly = result.BaselineCommit, result.ValidationSummary, result.BranchOnly
	if result.Policy.ExecutionMode != domain.RemediationExecutionAutoHotfix {
		job.view.Status = "needs_selection"
		job.view.Message = "Choose the service to validate"
		s.mu.Unlock()
		return
	}
	if result.BaselineCommit != job.request.Repository.DeployedCommit {
		job.view.Status, job.view.Message = "blocked", "Validation did not match the configured deployed revision. Check the repository again."
		s.mu.Unlock()
		return
	}
	policy := result.Policy
	// Preserve configured restrictions and provider credentials. Detection may narrow
	// allowed paths, but cannot silently expand an existing custom allowed set.
	old := domain.NormalizeRemediationPolicy(job.original)
	if len(old.ChangePolicy.AllowedPaths) != 1 || old.ChangePolicy.AllowedPaths[0] != "**" {
		policy.ChangePolicy.AllowedPaths = old.ChangePolicy.AllowedPaths
	}
	policy.ChangePolicy.DeniedPaths = append(policy.ChangePolicy.DeniedPaths, old.ChangePolicy.DeniedPaths...)
	policy.ChangePolicy.DeniedPaths = uniqueHotfixPaths(policy.ChangePolicy.DeniedPaths)
	if old.ChangePolicy.MaxChangedFiles < policy.ChangePolicy.MaxChangedFiles {
		policy.ChangePolicy.MaxChangedFiles = old.ChangePolicy.MaxChangedFiles
	}
	if old.ChangePolicy.MaxChangedLines < policy.ChangePolicy.MaxChangedLines {
		policy.ChangePolicy.MaxChangedLines = old.ChangePolicy.MaxChangedLines
	}
	policy.Publication = old.Publication
	policy.Publication.GitCredentialSecretID = job.request.GitCredential.ID
	if err := domain.ValidateRemediationPolicy(policy); err != nil {
		job.view.Status, job.view.Message = "blocked", "The detected configuration conflicts with the project change policy. Review advanced settings."
		s.mu.Unlock()
		return
	}
	job.policy = policy
	if !job.autoEnable {
		job.view.Status, job.view.Message = "ready", "Baseline validation passed; ready to enable automatic repair"
		s.mu.Unlock()
		return
	}
	job.view.Status, job.view.Message = "enabling", "Baseline passed; updating the validation environment"
	s.mu.Unlock()

	_, _, fingerprint, commitErr := s.inputs(ctx, projectID, job.request.Directory)
	if commitErr == nil && fingerprint != job.fingerprint {
		commitErr = ErrConflict
	}
	if commitErr == nil {
		_, commitErr = s.committer.CommitPreparedHotfix(ctx, projectID, job.request, job.original, job.policy)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if commitErr != nil {
		job.view.Status, job.view.Message = "blocked", "The deployed revision changed again or rebuilding failed. Check the repository configuration."
		return
	}
	job.view.Status, job.view.Message = "enabled", "Validation environment rebuilt for the deployed revision"
}

func (s *HotfixSetup) Get(ctx context.Context, principal authdomain.User, key string) (HotfixCheck, error) {
	project, err := s.service.resolveProject(ctx, principal, key)
	if err != nil {
		return HotfixCheck{}, err
	}
	s.mu.Lock()
	job := s.jobs[project.ID]
	if job != nil && time.Now().Before(job.view.ExpiresAt) {
		view := copyHotfixCheck(job.view)
		s.mu.Unlock()
		return view, nil
	}
	s.mu.Unlock()
	if draft, draftErr := s.service.repository.GetConfigurationDraft(ctx, project.ID); draftErr == nil && draft.Remediation != nil && draft.Remediation.ExecutionMode == domain.RemediationExecutionAutoHotfix {
		return HotfixCheck{Status: "enabled", Message: "Automatic repair is enabled for new executions", Candidates: []HotfixCandidate{}}, nil
	}
	return HotfixCheck{Status: "idle", Message: "Check the repository to prepare automatic repair", Candidates: []HotfixCandidate{}}, nil
}

func (s *HotfixSetup) Enable(ctx context.Context, principal authdomain.User, key, checkID string) (domain.RemediationPolicy, error) {
	project, err := s.service.resolveProject(ctx, principal, key)
	if err != nil {
		return domain.RemediationPolicy{}, err
	}
	s.mu.Lock()
	job := s.jobs[project.ID]
	if job == nil || job.view.ID != checkID || job.view.Status != "ready" || time.Now().After(job.view.ExpiresAt) {
		s.mu.Unlock()
		return domain.RemediationPolicy{}, ErrConflict
	}
	job.view.Status = "enabling"
	s.mu.Unlock()
	_, _, fingerprint, err := s.inputs(ctx, project.ID, job.request.Directory)
	if err == nil && fingerprint != job.fingerprint {
		err = ErrConflict
	}
	var saved domain.RemediationPolicy
	if err == nil {
		saved, err = s.committer.CommitPreparedHotfix(ctx, project.ID, job.request, job.original, job.policy)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		job.view.Status, job.view.Message = "blocked", "Configuration changed or enabling failed. Run the check again."
		return domain.RemediationPolicy{}, err
	}
	job.view.Status, job.view.Message = "enabled", "Automatic repair is enabled for new executions"
	return saved, nil
}

func (s *HotfixSetup) inputs(ctx context.Context, projectID, directory string) (HotfixPreparationRequest, domain.RemediationPolicy, [32]byte, error) {
	var zero [32]byte
	draft, err := s.service.repository.GetConfigurationDraft(ctx, projectID)
	if err != nil {
		return HotfixPreparationRequest{}, domain.RemediationPolicy{}, zero, err
	}
	if draft.Repository == nil {
		return HotfixPreparationRequest{}, domain.RemediationPolicy{}, zero, &HotfixSetupProblem{Message: "Connect and save a repository before enabling automatic repair."}
	}
	repo := *draft.Repository
	if err := domain.ValidateRepository(repo); err != nil {
		return HotfixPreparationRequest{}, domain.RemediationPolicy{}, zero, ErrInvalidInput
	}
	policy := domain.DefaultRemediationPolicy()
	if draft.Remediation != nil {
		policy = domain.NormalizeRemediationPolicy(*draft.Remediation)
	}
	gitID := policy.Publication.GitCredentialSecretID
	if gitID == "" && repo.CredentialSecretID != nil {
		gitID = *repo.CredentialSecretID
	}
	if gitID == "" {
		return HotfixPreparationRequest{}, policy, zero, &HotfixSetupProblem{Message: "Connect a Git write credential in repository settings first."}
	}
	secret, err := s.service.repository.GetEncryptedSecret(ctx, projectID, gitID)
	if err != nil || secret.Kind != domain.SecretGitCredential || secret.ID != gitID || secret.ProjectID != projectID || secret.Version < 1 {
		return HotfixPreparationRequest{}, policy, zero, &HotfixSetupProblem{Message: "Select an HTTPS Git credential with write access in advanced publication settings."}
	}
	request := HotfixPreparationRequest{ProjectID: projectID, Repository: repo, GitCredential: secret.Secret, Directory: directory}
	if repo.CredentialSecretID != nil && *repo.CredentialSecretID != "" {
		readSecret, err := s.service.repository.GetEncryptedSecret(ctx, projectID, *repo.CredentialSecretID)
		if err != nil {
			return HotfixPreparationRequest{}, policy, zero, err
		}
		if readSecret.ID != *repo.CredentialSecretID || readSecret.ProjectID != projectID || readSecret.Version < 1 {
			return HotfixPreparationRequest{}, policy, zero, ErrInvalidInput
		}
		request.RepositoryCredential = readSecret.Secret
	}
	payload, err := json.Marshal(struct {
		Request HotfixPreparationRequest
		Policy  domain.RemediationPolicy
	}{request, policy})
	if err != nil {
		return HotfixPreparationRequest{}, policy, zero, err
	}
	return request, policy, sha256.Sum256(payload), nil
}

func copyHotfixCheck(view HotfixCheck) HotfixCheck {
	view.Candidates = append([]HotfixCandidate{}, view.Candidates...)
	return view
}
func uniqueHotfixPaths(paths []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, path := range paths {
		if !seen[path] {
			seen[path] = true
			out = append(out, path)
		}
	}
	return out
}
