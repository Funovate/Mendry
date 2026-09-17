package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"mendry/backend/internal/modules/projects/application"
	"mendry/backend/internal/modules/projects/domain"
)

func TestHotfixJobStorePersistsAndRecovers(t *testing.T) {
	url := os.Getenv("MENDRY_POSTGRES_URL")
	if url == "" {
		t.Skip("explicit test database required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	projectID := uuid.Must(uuid.NewV7()).String()
	if _, err := pool.Exec(ctx, `INSERT INTO projects(id,project_key,name) VALUES($1,$2,'preparation job test')`, projectID, "prep-"+strings.ReplaceAll(projectID, "-", "")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM projects WHERE id=$1`, projectID) })

	store := NewHotfixJobStore(pool)
	first := application.HotfixPreparationJob{ProjectID: projectID, TargetCommit: strings.Repeat("a", 40), Directory: "services/api"}
	if err := store.Enqueue(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkRunning(ctx, projectID, first.TargetCommit); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.Recover(ctx, 10)
	if err != nil || len(recovered) != 1 || recovered[0] != first {
		t.Fatalf("recovered=%+v err=%v", recovered, err)
	}
	second := application.HotfixPreparationJob{ProjectID: projectID, TargetCommit: strings.Repeat("b", 40), Directory: "services/api"}
	if err := store.Enqueue(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkFinished(ctx, projectID, first.TargetCommit, true); err == nil {
		t.Fatal("stale target updated the replacement job")
	}
	if err := store.MarkRunning(ctx, projectID, second.TargetCommit); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkFinished(ctx, projectID, second.TargetCommit, true); err != nil {
		t.Fatal(err)
	}
	if queued, err := store.Queued(ctx, 10); err != nil || len(queued) != 0 {
		t.Fatalf("completed job remained queued: %+v err=%v", queued, err)
	}
}

func TestPreparedHotfixCommitVersionGuards(t *testing.T) {
	url := os.Getenv("MENDRY_POSTGRES_URL")
	if url == "" {
		t.Skip("explicit test database required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, scenario := range []string{"success", "repository_changed", "credential_rotated", "policy_changed", "concurrent_enable"} {
		t.Run(scenario, func(t *testing.T) {
			id := func() string { return uuid.Must(uuid.NewV7()).String() }
			projectID, repoID, secretID := id(), id(), id()
			exec := func(sql string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(ctx, sql, args...); err != nil {
					t.Fatal(err)
				}
			}
			exec(`INSERT INTO projects(id,project_key,name) VALUES($1,$2,'hotfix test')`, projectID, "hotfix-"+strings.ReplaceAll(projectID, "-", ""))
			exec(`INSERT INTO project_secrets(id,project_id,name,kind,ciphertext,nonce) VALUES($1,$2,'git','git_credential',$3,$4)`, secretID, projectID, make([]byte, 17), make([]byte, 12))
			deployedCommit := strings.Repeat("a", 40)
			exec(`INSERT INTO project_repositories(id,project_id,remote_url,scm_provider,transport,credential_secret_id,production_branch,deployed_commit) VALUES($1,$2,'https://example.test/repo.git','generic','https',$3,'main',$4)`, repoID, projectID, secretID, deployedCommit)
			t.Cleanup(func() {
				_, _ = pool.Exec(ctx, `DELETE FROM project_repositories WHERE project_id=$1`, projectID)
				_, _ = pool.Exec(ctx, `DELETE FROM project_secrets WHERE project_id=$1`, projectID)
				_, _ = pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, projectID)
			})
			request := application.HotfixPreparationRequest{ProjectID: projectID, Repository: domain.Repository{ID: repoID, Version: 1, DeployedCommit: deployedCommit}, GitCredential: domain.Secret{ID: secretID, Version: 1}}
			original := domain.DefaultRemediationPolicy()
			policy := original
			policy.AgentLoopMode = domain.AgentLoopModeResilientV1
			policy.ExecutionMode = domain.RemediationExecutionAutoHotfix
			policy.Publication.GitCredentialSecretID = secretID
			policy.ValidationProfile.ImageDigest = "sha256:" + strings.Repeat("b", 64)
			policy.ValidationProfile.WorkingDirectory = "."
			policy.ValidationProfile.PreparedCommit = deployedCommit
			policy.ValidationProfile.ToolchainID = "go-1.23-default"
			policy.ValidationProfile.BuildPlanVersion = "dependency-image-v2"
			policy.ValidationProfile.DependencyHash = strings.Repeat("c", 64)
			policy.ValidationProfile.RequiredCommands = []domain.ValidationCommand{{ID: "test", Version: 1, Argv: []string{"go", "test", "./..."}, TimeoutSeconds: 600}}
			committer := NewHotfixCommitter(pool)
			switch scenario {
			case "repository_changed":
				exec(`UPDATE project_repositories SET version=version+1 WHERE id=$1`, repoID)
			case "credential_rotated":
				exec(`UPDATE project_secrets SET version=version+1 WHERE id=$1`, secretID)
			case "policy_changed":
				exec(`UPDATE projects SET agent_loop_policy_version=agent_loop_policy_version+1 WHERE id=$1`, projectID)
			}
			if scenario == "concurrent_enable" {
				var wg sync.WaitGroup
				results := make(chan error, 2)
				for i := 0; i < 2; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_, err := committer.CommitPreparedHotfix(ctx, projectID, request, original, policy)
						results <- err
					}()
				}
				wg.Wait()
				close(results)
				success := 0
				for err := range results {
					if err == nil {
						success++
					}
				}
				if success != 1 {
					t.Fatalf("successful commits=%d", success)
				}
			} else {
				saved, err := committer.CommitPreparedHotfix(ctx, projectID, request, original, policy)
				if scenario == "success" {
					if err != nil || saved.Version != 2 || saved.ExecutionMode != domain.RemediationExecutionAutoHotfix {
						t.Fatalf("saved=%+v err=%v", saved, err)
					}
				} else if !errors.Is(err, application.ErrConflict) {
					t.Fatalf("stale update not rejected: %v", err)
				}
			}
		})
	}
}
