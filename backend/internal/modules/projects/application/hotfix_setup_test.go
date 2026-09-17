package application

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	authdomain "mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/modules/projects/domain"
)

type setupPreparer struct {
	result HotfixPreparationResult
	err    error
	gate   <-chan struct{}
	seen   chan HotfixPreparationRequest
}

func (p *setupPreparer) Prepare(ctx context.Context, r HotfixPreparationRequest, progress func(string)) (HotfixPreparationResult, error) {
	if p.seen != nil {
		p.seen <- r
	}
	progress("Running baseline tests")
	if p.gate != nil {
		select {
		case <-p.gate:
		case <-ctx.Done():
			return HotfixPreparationResult{}, ctx.Err()
		}
	}
	return p.result, p.err
}

type setupJobStore struct {
	mu        sync.Mutex
	jobs      []HotfixPreparationJob
	enqueued  []HotfixPreparationJob
	running   int
	completed int
	blocked   int
}

func (s *setupJobStore) Enqueue(_ context.Context, job HotfixPreparationJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enqueued = append(s.enqueued, job)
	return nil
}
func (s *setupJobStore) Recover(context.Context, int) ([]HotfixPreparationJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]HotfixPreparationJob(nil), s.jobs...), nil
}
func (s *setupJobStore) Queued(context.Context, int) ([]HotfixPreparationJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]HotfixPreparationJob(nil), s.jobs...), nil
}
func (s *setupJobStore) MarkRunning(context.Context, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running++
	return nil
}
func (s *setupJobStore) MarkFinished(_ context.Context, _, _ string, succeeded bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if succeeded {
		s.completed++
	} else {
		s.blocked++
	}
	return nil
}

type setupCommitter struct {
	calls  int
	policy domain.RemediationPolicy
}

func (c *setupCommitter) CommitPreparedHotfix(_ context.Context, _ string, _ HotfixPreparationRequest, _ domain.RemediationPolicy, p domain.RemediationPolicy) (domain.RemediationPolicy, error) {
	c.calls++
	c.policy = p
	p.Version++
	return p, nil
}

func setupFixture(t *testing.T) (*HotfixSetup, *fakeRepository, *setupPreparer, *setupCommitter) {
	t.Helper()
	secretID := "019ff544-405c-7d24-9f10-cb3fc579605c"
	config := validConfiguration()
	config.Repository.ID = "019ff544-405c-7d25-9f10-cb3fc579605c"
	config.Repository.CredentialSecretID = &secretID
	repo := &fakeRepository{project: domain.Project{ID: projectID, Key: "demo"}, configuration: config, secret: domain.EncryptedSecret{Secret: domain.Secret{ID: secretID, ProjectID: projectID, Kind: domain.SecretGitCredential, Name: "existing git", Version: 1}}}
	policy := domain.DefaultRemediationPolicy()
	policy.AgentLoopMode = domain.AgentLoopModeResilientV1
	policy.ExecutionMode = domain.RemediationExecutionAutoHotfix
	policy.ValidationProfile.ImageDigest = "sha256:" + strings.Repeat("a", 64)
	policy.ValidationProfile.WorkingDirectory = "."
	policy.ValidationProfile.RequiredCommands = []domain.ValidationCommand{{ID: "test", Version: 1, Argv: []string{"go", "test", "./..."}, TimeoutSeconds: 600}}
	policy.Publication.GitCredentialSecretID = secretID
	prep := &setupPreparer{result: HotfixPreparationResult{Policy: policy, Runtime: "go", Directory: ".", BaselineCommit: config.Repository.DeployedCommit, ValidationSummary: "Tests passed"}}
	committer := &setupCommitter{}
	return NewHotfixSetup(context.Background(), newService(t, repo, &fakeCipher{}), prep, committer), repo, prep, committer
}
func setupUser() authdomain.User { return authdomain.User{ID: userID, Enabled: true} }
func awaitSetup(t *testing.T, s *HotfixSetup) HotfixCheck {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		view, err := s.Get(context.Background(), setupUser(), "demo")
		if err != nil {
			t.Fatal(err)
		}
		if view.Status != "checking" {
			return view
		}
		runtime.Gosched()
	}
	t.Fatal("setup did not finish")
	return HotfixCheck{}
}
func TestHotfixSetupReusesCredentialAndRequiresExplicitEnable(t *testing.T) {
	s, _, prep, committer := setupFixture(t)
	prep.seen = make(chan HotfixPreparationRequest, 1)
	started, err := s.Check(context.Background(), setupUser(), "demo", "")
	if err != nil {
		t.Fatal(err)
	}
	request := <-prep.seen
	if request.GitCredential.ID != request.RepositoryCredential.ID || request.GitCredential.Name != "existing git" {
		t.Fatalf("credential not reused: %+v", request)
	}
	view := awaitSetup(t, s)
	if view.Status != "ready" || committer.calls != 0 {
		t.Fatalf("view=%+v calls=%d", view, committer.calls)
	}
	if _, err = s.Enable(context.Background(), setupUser(), "demo", "wrong-id"); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong ID accepted: %v", err)
	}
	saved, err := s.Enable(context.Background(), setupUser(), "demo", started.ID)
	if err != nil || saved.ExecutionMode != domain.RemediationExecutionAutoHotfix || committer.calls != 1 {
		t.Fatalf("enable=%+v %v", saved, err)
	}
	if _, err = s.Enable(context.Background(), setupUser(), "demo", started.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate enable accepted")
	}
}
func TestHotfixSetupAutomaticallyRebuildsEnabledProject(t *testing.T) {
	s, repo, prep, committer := setupFixture(t)
	previous := repo.configuration.Repository
	current := previous
	current.DeployedCommit = strings.Repeat("d", 40)
	current.Version++
	repo.configuration.Repository = current
	policy := prep.result.Policy
	enhanced := true
	policy.ValidationProfile.Enabled = &enhanced
	policy.ValidationProfile.WorkingDirectory = "."
	repo.configuration.Remediation = policy
	prep.result.BaselineCommit = current.DeployedCommit
	prep.result.Policy.ValidationProfile.PreparedCommit = current.DeployedCommit

	s.RepositoryUpdated(context.Background(), projectID, previous, current, policy)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		job := s.jobs[projectID]
		status := ""
		if job != nil {
			status = job.view.Status
		}
		s.mu.Unlock()
		if status == "enabled" {
			if committer.calls != 1 || committer.policy.ValidationProfile.PreparedCommit != current.DeployedCommit {
				t.Fatalf("automatic commit = %+v calls=%d", committer.policy.ValidationProfile, committer.calls)
			}
			return
		}
		if status == "blocked" {
			t.Fatalf("automatic rebuild blocked")
		}
		runtime.Gosched()
	}
	t.Fatal("automatic rebuild did not finish")
}

func TestHotfixSetupPersistsAndRecoversAutomaticRebuild(t *testing.T) {
	s, repo, prep, committer := setupFixture(t)
	store := &setupJobStore{}
	s.SetJobStore(store)
	current := repo.configuration.Repository
	current.DeployedCommit = strings.Repeat("e", 40)
	current.Version++
	repo.configuration.Repository = current
	policy := prep.result.Policy
	repo.configuration.Remediation = policy
	prep.result.BaselineCommit = current.DeployedCommit
	prep.result.Policy.ValidationProfile.PreparedCommit = current.DeployedCommit
	store.jobs = []HotfixPreparationJob{{ProjectID: projectID, TargetCommit: current.DeployedCommit, Directory: "."}}

	if err := s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		completed, running, blocked := store.completed, store.running, store.blocked
		store.mu.Unlock()
		if completed == 1 {
			if running != 1 || blocked != 0 || committer.calls != 1 {
				t.Fatalf("durable lifecycle running=%d completed=%d blocked=%d commits=%d", running, completed, blocked, committer.calls)
			}
			return
		}
		runtime.Gosched()
	}
	t.Fatal("recovered rebuild did not finish")
}

func TestHotfixSetupRejectsStaleInputs(t *testing.T) {
	for _, kind := range []string{"repository", "credential", "expiry"} {
		t.Run(kind, func(t *testing.T) {
			s, repo, _, c := setupFixture(t)
			check, err := s.Check(context.Background(), setupUser(), "demo", "")
			if err != nil {
				t.Fatal(err)
			}
			awaitSetup(t, s)
			switch kind {
			case "repository":
				repo.configuration.Repository.Version++
			case "credential":
				repo.secret.Version++
			case "expiry":
				s.mu.Lock()
				s.jobs[projectID].view.ExpiresAt = time.Now().Add(-time.Second)
				s.mu.Unlock()
			}
			if _, err = s.Enable(context.Background(), setupUser(), "demo", check.ID); !errors.Is(err, ErrConflict) || c.calls != 0 {
				t.Fatalf("stale enabled: %v calls=%d", err, c.calls)
			}
		})
	}
}
func TestHotfixSetupBlocksFailedOrAmbiguousValidation(t *testing.T) {
	for _, kind := range []string{"failed", "ambiguous"} {
		t.Run(kind, func(t *testing.T) {
			s, _, prep, c := setupFixture(t)
			want := "blocked"
			if kind == "failed" {
				prep.err = errors.New("Baseline tests failed")
			} else {
				want = "needs_selection"
				prep.result.Policy = domain.RemediationPolicy{}
				prep.result.Candidates = []HotfixCandidate{{Directory: "api", Runtime: "go"}, {Directory: "web", Runtime: "node"}}
			}
			check, err := s.Check(context.Background(), setupUser(), "demo", "")
			if err != nil {
				t.Fatal(err)
			}
			if view := awaitSetup(t, s); view.Status != want {
				t.Fatalf("view=%+v", view)
			}
			if _, err = s.Enable(context.Background(), setupUser(), "demo", check.ID); !errors.Is(err, ErrConflict) || c.calls != 0 {
				t.Fatal("unready check enabled")
			}
		})
	}
}
func TestHotfixSetupCoalescesRunningChecksAndEnforcesAuth(t *testing.T) {
	s, _, prep, _ := setupFixture(t)
	gate := make(chan struct{})
	prep.gate = gate
	defer close(gate)
	if _, err := s.Check(context.Background(), authdomain.User{}, "demo", ""); !errors.Is(err, ErrForbidden) {
		t.Fatal("anonymous check accepted")
	}
	first, err := s.Check(context.Background(), setupUser(), "demo", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Check(context.Background(), setupUser(), "demo", "")
	if err != nil || first.ID != second.ID {
		t.Fatalf("duplicate check: %+v %v", second, err)
	}
}
