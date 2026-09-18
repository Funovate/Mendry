//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	incidentpostgres "mendry/backend/internal/modules/incidents/adapter/postgres"
	incidentapplication "mendry/backend/internal/modules/incidents/application"
	incidentdomain "mendry/backend/internal/modules/incidents/domain"
	notificationplatform "mendry/backend/internal/modules/notifications/adapter/platform"
	notificationpostgres "mendry/backend/internal/modules/notifications/adapter/postgres"
	notificationapplication "mendry/backend/internal/modules/notifications/application"
	notificationdomain "mendry/backend/internal/modules/notifications/domain"
	projectpostgres "mendry/backend/internal/modules/projects/adapter/postgres"
	projectsecret "mendry/backend/internal/modules/projects/adapter/secret"
	projectdomain "mendry/backend/internal/modules/projects/domain"
	remediationpostgres "mendry/backend/internal/modules/remediation/adapter/postgres"
	remediationdomain "mendry/backend/internal/modules/remediation/domain"
)

type rejectingEnqueuer struct{}

func (rejectingEnqueuer) Enqueue(context.Context, pgx.Tx, notificationdomain.Event) error {
	return errors.New("enqueue failed")
}

type notificationTestSender struct{}

func (notificationTestSender) Send(context.Context, string, notificationdomain.Credentials, string) error {
	return nil
}

func notificationDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	target := os.Getenv("MENDRY_TEST_POSTGRES_URL")
	name := os.Getenv("MENDRY_TEST_POSTGRES_ISOLATION")
	parsed, err := pgx.ParseConfig(target)
	if target == "" || err != nil || parsed.Database != name || strings.ContainsAny(target, "\x00\r\n") {
		t.Fatal("an explicit isolated notification test database URL and matching isolation marker are required")
	}
	// This test may rebuild only its own dedicated database, never another integration target.
	if name != "mendry_notifications_test" {
		t.Fatal("notification integration requires dedicated mendry_notifications_test database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, target)
	if err != nil {
		t.Fatal("connect test database")
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatal("test PostgreSQL not reachable")
	}
	var unlocked bool
	if err := pool.QueryRow(ctx, `SELECT pg_try_advisory_lock(6025025)`).Scan(&unlocked); err != nil || !unlocked {
		t.Fatal("dedicated notification test database is busy")
	}
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("notification integration schema: %v", err)
		}
	}
	exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public`)
	files, err := filepath.Glob("../../internal/commands/migrate/migrations/*.up.sql")
	if err != nil || len(files) != 26 {
		t.Fatalf("migration source count %d: %v", len(files), err)
	}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("apply %s: %v", filepath.Base(file), err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return pool
}
func notificationUUID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

func TestNotificationsTransactionalLifecycleAndDelivery(t *testing.T) {
	pool := notificationDatabase(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	projects, err := projectpostgres.NewRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	project, err := projects.CreateProject(ctx, projectdomain.Project{ID: notificationUUID(t), Key: "notifications-test", Name: "Notifications test"})
	if err != nil {
		t.Fatal(err)
	}
	secretID := notificationUUID(t)
	if _, err := projects.CreateSecret(ctx, projectdomain.EncryptedSecret{Secret: projectdomain.Secret{ID: secretID, ProjectID: project.ID, Name: "test-llm", Kind: projectdomain.SecretHTTPBearer, KeyVersion: 1}, Ciphertext: []byte("fixture-ciphertext-is-not-a-secret"), Nonce: []byte("123456789012")}); err != nil {
		t.Fatal(err)
	}
	config, err := projects.UpsertConfiguration(ctx, project.ID, projectdomain.Configuration{
		LLM:         &projectdomain.LLMProvider{ID: notificationUUID(t), Provider: "openai", BaseURL: "https://api.openai.com", CredentialSecretID: secretID, Model: "test-model"},
		Environment: projectdomain.Environment{ID: notificationUUID(t), Key: "production", Name: "Production"},
		Repository:  projectdomain.Repository{ID: notificationUUID(t), RemoteURL: "https://github.com/example/service.git", SCMProvider: "github", Transport: "https", ProductionBranch: "main", DeployedCommit: strings.Repeat("a", 40)},
		Source:      projectdomain.Source{ID: notificationUUID(t), Kind: "cloud", Config: []byte(`{"schemaVersion":1,"provider":"tencent-cls","region":"ap-shanghai","resource":"test"}`), Capabilities: []string{"push_ingestion"}, Enabled: true},
		Trigger:     projectdomain.Trigger{ID: notificationUUID(t), Kind: "signed_webhook", Config: []byte(`{"schemaVersion":1,"eventTypes":["error"],"deduplicationKey":"fingerprint"}`), Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := notificationpostgres.NewStore(pool, "https://console.example.test")
	cipher, err := projectsecret.NewAESGCM(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	service := notificationapplication.NewService(store, cipher, notificationTestSender{}, notificationplatform.Validate)
	inputs := []notificationdomain.ChannelInput{
		{Name: "Telegram Ops", Platform: "telegram", Enabled: true, Credentials: &notificationdomain.Credentials{BotToken: "123456:abcdefghijklmnop", ChatID: "-100123"}},
		{Name: "Feishu Ops", Platform: "feishu", Enabled: true, Credentials: &notificationdomain.Credentials{WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/abcdefghijklmnop"}},
		{Name: "WeCom Ops", Platform: "wecom", Enabled: true, Credentials: &notificationdomain.Credentials{WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=abcdefghijklmnop"}},
	}
	channels := []notificationdomain.Channel{}
	for _, input := range inputs {
		c, err := service.SaveChannel(ctx, project.ID, "", input)
		if err != nil {
			t.Fatal(err)
		}
		channels = append(channels, c)
	}
	incidents, err := incidentpostgres.NewRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	incidents.SetNotificationEnqueuer(store)
	runs, err := remediationpostgres.NewRunStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	runs.SetNotificationEnqueuer(store)
	buildIncident := func() incidentdomain.Incident {
		now := time.Now().UTC()
		return incidentdomain.Incident{InternalID: notificationUUID(t), ProjectID: project.ID, SourceID: config.Source.ID, Title: "Database timeout", Fingerprint: uuid.NewString(), Status: incidentdomain.StatusOpen, Priority: incidentdomain.PriorityP1, FirstSeen: now, LastSeen: now, OccurrenceCount: 1, HostCount: 1, NotificationSummary: "Lifecycle default", LifecycleGeneration: 1, DeployedCommit: config.Repository.DeployedCommit}
	}
	countEvents := func(id string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM notification_events WHERE incident_id=$1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	countDeliveries := func() int {
		t.Helper()
		items, err := store.ListDeliveries(ctx, project.ID)
		if err != nil {
			t.Fatal(err)
		}
		return len(items)
	}
	drain := func() {
		t.Helper()
		for {
			d, err := store.Claim(ctx)
			if errors.Is(err, notificationdomain.ErrNotFound) {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Finish(ctx, d, nil); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("business state rolls back when enqueue fails", func(t *testing.T) {
		incidents.SetNotificationEnqueuer(rejectingEnqueuer{})
		candidate := buildIncident()
		if _, err := incidents.Create(ctx, candidate, nil); err == nil {
			t.Fatal("create accepted failed enqueue")
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM incidents WHERE id=$1`, candidate.InternalID).Scan(&n); err != nil || n != 0 {
			t.Fatalf("create not rolled back n=%d err=%v", n, err)
		}
		incidents.SetNotificationEnqueuer(store)
	})
	candidate := buildIncident()
	incident, err := incidents.Create(ctx, candidate, &incidentapplication.RemediationRequest{IncidentID: candidate.InternalID, LifecycleGeneration: 1, DeployedCommit: candidate.DeployedCommit, Priority: "P1", ContextVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	var rootID string
	if err := pool.QueryRow(ctx, `SELECT r.id::text FROM remediation_run r JOIN remediation_series s ON s.id=r.series_id WHERE s.incident_id=$1`, incident.InternalID).Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	if countEvents(incident.InternalID) != 1 || countDeliveries() != 3 {
		t.Fatal("trigger and per-channel snapshots missing")
	}

	t.Run("AI enqueue rollback and first result dedup", func(t *testing.T) {
		runs.SetNotificationEnqueuer(rejectingEnqueuer{})
		if err := runs.Transition(ctx, rootID, remediationdomain.RunStateQueued, remediationdomain.RunStateFailed, remediationdomain.Effect{}); err == nil {
			t.Fatal("transition accepted failed enqueue")
		}
		var state string
		if err := pool.QueryRow(ctx, `SELECT state FROM remediation_run WHERE id=$1`, rootID).Scan(&state); err != nil || state != "queued" {
			t.Fatalf("transition not rolled back: %s %v", state, err)
		}
		runs.SetNotificationEnqueuer(store)
		if err := runs.Transition(ctx, rootID, remediationdomain.RunStateQueued, remediationdomain.RunStateFailed, remediationdomain.Effect{}); err != nil {
			t.Fatal(err)
		}
		if countEvents(incident.InternalID) != 2 || countDeliveries() != 6 {
			t.Fatal("first result missing")
		}
		child := uuid.NewString()
		exec(`INSERT INTO remediation_run(id,series_id,attempt_number,state) SELECT $1,series_id,2,'queued' FROM remediation_run WHERE id=$2`, child, rootID)
		if err := runs.Transition(ctx, child, remediationdomain.RunStateQueued, remediationdomain.RunStateCompletedNonCode, remediationdomain.Effect{}); err != nil {
			t.Fatal(err)
		}
		if countEvents(incident.InternalID) != 2 || countDeliveries() != 6 {
			t.Fatal("retry success sent second result")
		}
	})
	t.Run("lease expiry fencing retries and message order", func(t *testing.T) {
		first, err := store.Claim(ctx)
		if err != nil || first.Kind != "trigger" {
			t.Fatalf("first claim: %+v %v", first, err)
		}
		second, err := store.Claim(ctx)
		if err != nil || second.Kind != "trigger" || second.ID == first.ID {
			t.Fatalf("second claim: %+v %v", second, err)
		}
		third, err := store.Claim(ctx)
		if err != nil || third.Kind != "trigger" {
			t.Fatalf("third claim %v", err)
		}
		if _, err := store.Claim(ctx); !errors.Is(err, notificationdomain.ErrNotFound) {
			t.Fatalf("result overtook trigger: %v", err)
		}
		exec(`UPDATE notification_deliveries SET lease_until=now()-interval '1 second' WHERE id=$1`, first.ID)
		reclaimed, err := store.Claim(ctx)
		if err != nil || reclaimed.ID != first.ID || reclaimed.LeaseToken == first.LeaseToken {
			t.Fatalf("lease reclaim: %+v %v", reclaimed, err)
		}
		if err := store.Finish(ctx, first, nil); !errors.Is(err, notificationdomain.ErrConflict) {
			t.Fatalf("stale worker ack accepted: %v", err)
		}
		if err := store.Finish(ctx, reclaimed, errors.New("bot token secret")); err != nil {
			t.Fatal(err)
		}
		items, err := store.ListDeliveries(ctx, project.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range items {
			if strings.Contains(d.LastError, "secret") {
				t.Fatal("secret persisted in delivery error")
			}
		}
		exec(`UPDATE notification_deliveries SET next_attempt_at=now(),attempts=11 WHERE id=$1`, first.ID)
		final, err := store.Claim(ctx)
		if err != nil || final.ID != first.ID {
			t.Fatal("retry not claimed")
		}
		if err := store.Finish(ctx, final, errors.New("failed")); err != nil {
			t.Fatal(err)
		}
		result, err := store.Claim(ctx)
		if err != nil || result.Kind != "result" || result.ChannelID != final.ChannelID {
			t.Fatalf("failed trigger blocked result: %+v %v", result, err)
		}
		if err := store.Finish(ctx, result, nil); err != nil {
			t.Fatal(err)
		}
		if err := store.RetryDelivery(ctx, uuid.NewString(), first.ID); !errors.Is(err, notificationdomain.ErrConflict) {
			t.Fatal("cross project retry accepted")
		}
		if err := store.RetryDelivery(ctx, project.ID, first.ID); err != nil {
			t.Fatal(err)
		}
		retry, err := store.Claim(ctx)
		if err != nil || retry.Attempts != 1 {
			t.Fatalf("manual retry: %+v %v", retry, err)
		}
		if err := store.Finish(ctx, retry, nil); err != nil {
			t.Fatal(err)
		}
		if err := store.Finish(ctx, second, nil); err != nil {
			t.Fatal(err)
		}
		if err := store.Finish(ctx, third, nil); err != nil {
			t.Fatal(err)
		}
		drain()
		if !strings.Contains(first.Message, "/incidents/INC-") {
			t.Fatalf("noncanonical console link %s", first.Message)
		}
	})
	t.Run("reopen and automatic intermediate diagnosis", func(t *testing.T) {
		if _, err := incidents.UpdateStatus(ctx, project.ID, incident.Number, incidentdomain.StatusRecovered, 1, incident.DeployedCommit, nil); err != nil {
			t.Fatal(err)
		}
		if countEvents(incident.InternalID) != 2 {
			t.Fatal("recovery notified")
		}
		incidents.SetNotificationEnqueuer(rejectingEnqueuer{})
		if _, err := incidents.UpdateStatus(ctx, project.ID, incident.Number, incidentdomain.StatusOpen, 2, incident.DeployedCommit, &incidentapplication.RemediationRequest{IncidentID: incident.InternalID, LifecycleGeneration: 2, DeployedCommit: incident.DeployedCommit, Priority: "P1", ContextVersion: 2}); err == nil {
			t.Fatal("reopen accepted failed enqueue")
		}
		var state string
		var generation int64
		if err := pool.QueryRow(ctx, `SELECT status,lifecycle_generation FROM incidents WHERE id=$1`, incident.InternalID).Scan(&state, &generation); err != nil || state != "Recovered" || generation != 1 {
			t.Fatalf("reopen not rolled back: %s %d %v", state, generation, err)
		}
		incidents.SetNotificationEnqueuer(store)
		reopened, err := incidents.UpdateStatus(ctx, project.ID, incident.Number, incidentdomain.StatusOpen, 2, incident.DeployedCommit, &incidentapplication.RemediationRequest{IncidentID: incident.InternalID, LifecycleGeneration: 2, DeployedCommit: incident.DeployedCommit, Priority: "P1", ContextVersion: 2})
		if err != nil {
			t.Fatal(err)
		}
		if reopened.LifecycleGeneration != 2 || countEvents(incident.InternalID) != 3 {
			t.Fatal("reopen trigger missing")
		}
		if _, err := incidents.RecordOccurrence(ctx, project.ID, incident.Fingerprint, time.Now()); err != nil {
			t.Fatal(err)
		}
		if countEvents(incident.InternalID) != 3 {
			t.Fatal("duplicate occurrence notified")
		}
		var id string
		if err := pool.QueryRow(ctx, `SELECT r.id::text FROM remediation_run r JOIN remediation_series s ON s.id=r.series_id WHERE s.incident_id=$1 AND s.lifecycle_generation=2`, incident.InternalID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		exec(`UPDATE remediation_run SET execution_mode='auto_hotfix' WHERE id=$1`, id)
		if err := runs.Transition(ctx, id, remediationdomain.RunStateQueued, remediationdomain.RunStateDiagnosisReadyForReview, remediationdomain.Effect{}); err != nil {
			t.Fatal(err)
		}
		if countEvents(incident.InternalID) != 3 {
			t.Fatal("intermediate diagnosis notified")
		}
		if err := runs.Transition(ctx, id, remediationdomain.RunStateDiagnosisReadyForReview, remediationdomain.RunStateAwaitingHumanReview, remediationdomain.Effect{}); err != nil {
			t.Fatal(err)
		}
		if countEvents(incident.InternalID) != 4 {
			t.Fatal("hotfix stopping result missing")
		}
		drain()
	})
	t.Run("no channel no events or historical backfill", func(t *testing.T) {
		exec(`UPDATE notification_channels SET enabled=false WHERE project_id=$1`, project.ID)
		silentCandidate := buildIncident()
		silent, err := incidents.Create(ctx, silentCandidate, &incidentapplication.RemediationRequest{IncidentID: silentCandidate.InternalID, LifecycleGeneration: 1, DeployedCommit: silentCandidate.DeployedCommit, Priority: "P1", ContextVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		var id string
		if err := pool.QueryRow(ctx, `SELECT r.id::text FROM remediation_run r JOIN remediation_series s ON s.id=r.series_id WHERE s.incident_id=$1`, silent.InternalID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if err := runs.Transition(ctx, id, remediationdomain.RunStateQueued, remediationdomain.RunStateFailed, remediationdomain.Effect{}); err != nil {
			t.Fatal(err)
		}
		exec(`UPDATE notification_channels SET enabled=true WHERE project_id=$1`, project.ID)
		next := uuid.NewString()
		exec(`INSERT INTO remediation_run(id,series_id,attempt_number,state) SELECT $1,series_id,2,'queued' FROM remediation_run WHERE id=$2`, next, id)
		if err := runs.Transition(ctx, next, remediationdomain.RunStateQueued, remediationdomain.RunStateCompletedNonCode, remediationdomain.Effect{}); err != nil {
			t.Fatal(err)
		}
		if countEvents(silent.InternalID) != 0 {
			t.Fatal("historical result was backfilled")
		}
	})
	t.Run("disable cancels enqueue committed while waiting on channel lock", func(t *testing.T) {
		fresh := buildIncident()
		incidents.SetNotificationEnqueuer(nil)
		created, err := incidents.Create(ctx, fresh, nil)
		incidents.SetNotificationEnqueuer(store)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if err := store.Enqueue(ctx, tx, notificationdomain.Event{Kind: "trigger", IncidentID: created.InternalID, Generation: 1}); err != nil {
			t.Fatal(err)
		}
		c := channels[0]
		disableCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := service.SaveChannel(disableCtx, project.ID, c.ID, notificationdomain.ChannelInput{Name: c.Name, Platform: c.Platform, Enabled: false})
			done <- err
		}()
		deadline := time.Now().Add(2 * time.Second)
		for {
			var waiting bool
			if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'UPDATE notification_channels SET name=%')`).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			select {
			case err := <-done:
				t.Fatalf("disable did not coordinate with uncommitted enqueue: %v", err)
			default:
			}
			if time.Now().After(deadline) {
				t.Fatal("disable never awaited channel lock")
			}
			time.Sleep(5 * time.Millisecond)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		var cancelled int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries d JOIN notification_events e ON e.id=d.event_id WHERE e.incident_id=$1 AND d.channel_id=$2 AND d.state='cancelled'`, created.InternalID, c.ID).Scan(&cancelled); err != nil || cancelled != 1 {
			t.Fatalf("concurrently committed delivery not cancelled: %d %v", cancelled, err)
		}
		drain()
		if _, err := service.SaveChannel(ctx, project.ID, c.ID, notificationdomain.ChannelInput{Name: c.Name, Platform: c.Platform, Enabled: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Claim(ctx); !errors.Is(err, notificationdomain.ErrNotFound) {
			t.Fatalf("concurrently enqueued cancelled delivery backfilled: %v", err)
		}
	})
	t.Run("rotation uses current credentials and retains audit snapshot", func(t *testing.T) {
		fresh := buildIncident()
		newIncident, err := incidents.Create(ctx, fresh, nil)
		if err != nil {
			t.Fatal(err)
		}
		c := channels[0]
		replacement := inputs[0]
		replacement.Credentials = &notificationdomain.Credentials{BotToken: "999999:replacementabcdefghijklmnop", ChatID: "-100999"}
		updated, err := service.SaveChannel(ctx, project.ID, c.ID, replacement)
		if err != nil {
			t.Fatal(err)
		}
		after, err := store.ListDeliveries(ctx, project.ID)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, d := range after {
			if d.ChannelID == c.ID && d.IncidentNumber == newIncident.Number {
				found = true
				if string(d.Ciphertext) != string(c.Ciphertext) || string(d.Nonce) != string(c.Nonce) {
					t.Fatal("historical audit snapshot mutated by rotation")
				}
			}
		}
		if !found {
			t.Fatal("snapshot missing")
		}
		claimedCurrent := false
		for {
			d, err := store.Claim(ctx)
			if errors.Is(err, notificationdomain.ErrNotFound) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if d.ChannelID == c.ID {
				claimedCurrent = true
				if string(d.Ciphertext) != string(updated.Ciphertext) || string(d.Nonce) != string(updated.Nonce) || d.Platform != updated.Platform {
					t.Fatal("claim did not use current channel credentials")
				}
				plaintext, err := cipher.Decrypt(project.ID, c.ID, projectdomain.SecretKind("notification_channel"), d.Ciphertext, d.Nonce)
				var credentials notificationdomain.Credentials
				if err != nil || json.Unmarshal(plaintext, &credentials) != nil || credentials != *replacement.Credentials {
					t.Fatalf("current encrypted credentials not decryptable: %v", err)
				}
			}
			if err := store.Finish(ctx, d, nil); err != nil {
				t.Fatal(err)
			}
		}
		if !claimedCurrent {
			t.Fatal("rotated channel was not claimed")
		}
	})
	for _, operation := range []string{"disable", "delete"} {
		t.Run(operation+" cancels pending and sending and fences stale worker", func(t *testing.T) {
			c := channels[0]
			if operation == "delete" {
				c = channels[1]
			}
			fresh := buildIncident()
			created, err := incidents.Create(ctx, fresh, &incidentapplication.RemediationRequest{IncidentID: fresh.InternalID, LifecycleGeneration: 1, DeployedCommit: fresh.DeployedCommit, Priority: "P1", ContextVersion: 1})
			if err != nil {
				t.Fatal(err)
			}
			var id string
			if err := pool.QueryRow(ctx, `SELECT r.id::text FROM remediation_run r JOIN remediation_series s ON s.id=r.series_id WHERE s.incident_id=$1`, created.InternalID).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if err := runs.Transition(ctx, id, remediationdomain.RunStateQueued, remediationdomain.RunStateFailed, remediationdomain.Effect{}); err != nil {
				t.Fatal(err)
			}
			var active notificationdomain.Delivery
			for {
				d, err := store.Claim(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if d.ChannelID == c.ID {
					active = d
					break
				}
				if err := store.Finish(ctx, d, nil); err != nil {
					t.Fatal(err)
				}
			}
			var failedID string
			if err := pool.QueryRow(ctx, `UPDATE notification_deliveries SET state='failed',delivered_at=NULL WHERE id=(SELECT id FROM notification_deliveries WHERE channel_id=$1 AND state='delivered' ORDER BY sequence LIMIT 1) RETURNING id::text`, c.ID).Scan(&failedID); err != nil {
				t.Fatal(err)
			}
			if operation == "disable" {
				if _, err := service.SaveChannel(ctx, project.ID, c.ID, notificationdomain.ChannelInput{Name: c.Name, Platform: c.Platform, Enabled: false}); err != nil {
					t.Fatal(err)
				}
			} else if err := service.DeleteChannel(ctx, project.ID, c.ID); err != nil {
				t.Fatal(err)
			}
			items, err := store.ListDeliveries(ctx, project.ID)
			if err != nil {
				t.Fatal(err)
			}
			cancelled := 0
			for _, d := range items {
				if d.ChannelID == c.ID && d.IncidentNumber == created.Number {
					if d.State != notificationdomain.DeliveryCancelled || d.LeaseToken != "" {
						t.Fatalf("uncancelled delivery %+v", d)
					}
					var leaseCleared bool
					if err := pool.QueryRow(ctx, `SELECT lease_token IS NULL AND lease_until IS NULL FROM notification_deliveries WHERE id=$1`, d.ID).Scan(&leaseCleared); err != nil || !leaseCleared {
						t.Fatalf("cancellation left lease active: %v", err)
					}
					cancelled++
					if _, err := pool.Exec(ctx, `UPDATE notification_deliveries SET lease_token=$2,lease_until=now() WHERE id=$1`, d.ID, uuid.NewString()); err == nil {
						t.Fatal("database accepted an active lease on cancelled delivery")
					}
					if err := store.RetryDelivery(ctx, project.ID, d.ID); !errors.Is(err, notificationdomain.ErrConflict) {
						t.Fatalf("cancelled delivery retried: %v", err)
					}
				}
			}
			if cancelled != 2 {
				t.Fatalf("cancelled %d deliveries, want pending result and sending trigger", cancelled)
			}
			if err := store.Finish(ctx, active, nil); !errors.Is(err, notificationdomain.ErrConflict) {
				t.Fatalf("stale success overwrote cancelled: %v", err)
			}
			if err := store.Finish(ctx, active, errors.New("late failure")); !errors.Is(err, notificationdomain.ErrConflict) {
				t.Fatalf("stale failure overwrote cancelled: %v", err)
			}
			if err := store.RetryDelivery(ctx, project.ID, failedID); !errors.Is(err, notificationdomain.ErrConflict) {
				t.Fatalf("retry accepted unavailable channel: %v", err)
			}
			drain()
			if operation == "disable" {
				if _, err := service.SaveChannel(ctx, project.ID, c.ID, notificationdomain.ChannelInput{Name: c.Name, Platform: c.Platform, Enabled: true}); err != nil {
					t.Fatal(err)
				}
			}
			if d, err := store.Claim(ctx); !errors.Is(err, notificationdomain.ErrNotFound) {
				t.Fatalf("unavailable or re-enabled channel backfilled %+v: %v", d, err)
			}
		})
	}
	t.Log(fmt.Sprintf("validated %d durable deliveries across three platforms", countDeliveries()))
}
