//go:build integration

package postgres

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"mendry/backend/internal/modules/overview/application"
	"mendry/backend/internal/modules/remediation/adapter/postgres/remediationdb"
)

// Each test owns a fresh database. The configured URL is used only to create
// that database; application databases are never migrated or reset by this test.
func TestOverviewPostgres(t *testing.T) {
	url := os.Getenv("OVERVIEW_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("OVERVIEW_TEST_POSTGRES_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	name := "overview_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	}()
	config, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.Database = name
	db, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, query, args...); err != nil {
			t.Fatalf("SQL failed: %v\n%s", err, query)
		}
	}
	files, err := filepath.Glob("../../../../commands/migrate/migrations/*.up.sql")
	if err != nil || len(files) != 21 {
		t.Fatalf("migration files %d: %v", len(files), err)
	}
	for _, file := range files[:20] {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		exec(string(body))
	}
	project, environment, source := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	exec(`INSERT INTO projects(id,project_key,name) VALUES ($1,'overview-test','Overview test')`, project)
	exec(`INSERT INTO project_environments(id,project_id,environment_key,name) VALUES($1,$2,'production','Production')`, environment, project)
	exec(`INSERT INTO project_sources(id,project_id,environment_id,kind) VALUES($1,$2,$3,'mcp')`, source, project, environment)
	incident := func(status string) string {
		t.Helper()
		id := uuid.Must(uuid.NewV7()).String()
		exec(`INSERT INTO incidents(id,project_id,environment_id,source_id,title,fingerprint,status,source,lifecycle_generation,deployed_commit) VALUES($1::uuid,$2,$3,$4,'Overview test incident',$1::text,$5,'mcp',1,'abc123')`, id, project, environment, source, status)
		return id
	}
	series := func(id string, generation int) string {
		t.Helper()
		var result string
		if err := db.QueryRow(ctx, `INSERT INTO remediation_series(incident_id,lifecycle_generation,deployed_commit) VALUES($1,$2,'abc123') RETURNING id::text`, id, generation).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	run := func(s string, n int, state string, tokens int64, started time.Time) string {
		t.Helper()
		var id string
		if err := db.QueryRow(ctx, `INSERT INTO remediation_run(series_id,attempt_number,state,model_tokens_in,started_at,ended_at) VALUES($1,$2,$3,$4,$5,CASE WHEN $3 IN ('failed','budget_exhausted','completed_non_code','blocked_manual_review','diagnosis_ready_for_review','awaiting_human_review') THEN $5::timestamptz + interval '10 minutes' ELSE NULL END) RETURNING id::text`, s, n, state, tokens, started).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	now := time.Now().UTC()
	first := incident("Open")
	s := series(first, 1)
	legacy := run(s, 1, "failed", 100, now.Add(-48*time.Hour))
	migration, _ := os.ReadFile(files[20])
	exec(string(migration))
	var events int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM remediation_token_usage`).Scan(&events); err != nil || events != 0 {
		t.Fatalf("historical usage backfilled: %d %v", events, err)
	}
	var phase *time.Time
	if err := db.QueryRow(ctx, `SELECT state_entered_at FROM remediation_run WHERE id=$1`, legacy).Scan(&phase); err != nil || phase != nil {
		t.Fatalf("legacy phase should be unknown: %v %v", phase, err)
	}
	success := run(s, 2, "diagnosis_ready_for_review", 40, now.Add(-time.Hour))
	active := run(series(incident("Open"), 1), 1, "diagnosing", 0, now.Add(-30*time.Minute))
	run(series(incident("Recovered"), 1), 1, "completed_non_code", 0, now.Add(-time.Hour))
	run(series(incident("Closed"), 1), 1, "failed", 0, now.Add(-time.Hour))
	run(series(incident("Open"), 1), 1, "blocked_manual_review", 0, now.Add(-time.Hour))
	run(series(incident("Open"), 1), 1, "budget_exhausted", 0, now.Add(-time.Hour))
	// Latest generation is active: old successful delivery is no longer the latest result.
	reopened := incident("Open")
	run(series(reopened, 1), 1, "completed_non_code", 0, now.Add(-48*time.Hour))
	run(series(reopened, 2), 1, "future_phase", 0, now.Add(-time.Minute))
	exec(`UPDATE remediation_run SET model_calls=1, model_tokens_in=model_tokens_in+20, model_tokens_out=10 WHERE id=$1`, active)
	exec(`UPDATE remediation_run SET model_tokens_in=model_tokens_in, model_tokens_out=model_tokens_out WHERE id=$1`, active)
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE remediation_run SET model_tokens_in=model_tokens_in+999 WHERE id=$1`, active); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE remediation_run SET model_tokens_in=0 WHERE id=$1`, active); err == nil {
		t.Fatal("decreasing counters accepted")
	}
	var before time.Time
	if err := db.QueryRow(ctx, `SELECT state_entered_at FROM remediation_run WHERE id=$1`, active).Scan(&before); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE remediation_run SET state=state WHERE id=$1`, active)
	var after time.Time
	_ = db.QueryRow(ctx, `SELECT state_entered_at FROM remediation_run WHERE id=$1`, active).Scan(&after)
	if !before.Equal(after) {
		t.Fatal("same state reset phase clock")
	}
	exec(`UPDATE remediation_run SET state='planning' WHERE id=$1`, active)
	_ = db.QueryRow(ctx, `SELECT state_entered_at FROM remediation_run WHERE id=$1`, active).Scan(&after)
	if !after.After(before) {
		t.Fatal("phase change did not update clock")
	}
	repository, _ := NewRepository(db)
	w, _ := application.NewWindow(time.Now().UTC(), "UTC", "7d")
	snapshot, err := repository.Snapshot(ctx, project, w)
	if err != nil {
		t.Fatal(err)
	}
	c := snapshot.Counts
	if c.Processed != 6 || c.ActiveIncidents != 2 || c.ActiveTasks != 2 || c.Successful != 2 || c.Failed != 2 || c.BudgetExhausted != 1 || c.Recovered != 1 || c.Waiting != 2 {
		t.Fatalf("incorrect counts: %#v", c)
	}
	if snapshot.Tokens.Input != 160 || snapshot.Tokens.Output != 10 {
		t.Fatalf("totals: %#v", snapshot.Tokens)
	}
	var trendIn, trendOut int64
	for _, b := range snapshot.Trend {
		trendIn += b.Input
		trendOut += b.Output
	}
	if trendIn != 60 || trendOut != 10 {
		t.Fatalf("trend includes legacy/duplicate/rollback: %d/%d", trendIn, trendOut)
	}
	tasks, err := repository.Tasks(ctx, project, application.TaskFilter{Scope: "all", Sort: "tokens", Page: 1, PageSize: 2})
	if err != nil || tasks.Total != 9 || len(tasks.Items) != 2 || tasks.Items[0].RunID != legacy || tasks.Items[1].RunID != success {
		t.Fatalf("page: %#v %v", tasks, err)
	}
	emptyPage, err := repository.Tasks(ctx, project, application.TaskFilter{Scope: "all", Sort: "recent", Page: 100, PageSize: 20})
	if err != nil || emptyPage.Total != 9 || len(emptyPage.Items) != 0 {
		t.Fatalf("empty page lost total: %#v %v", emptyPage, err)
	}
	other := uuid.Must(uuid.NewV7()).String()
	exec(`INSERT INTO projects(id,project_key,name) VALUES($1,'other-test','Other')`, other)
	empty, err := repository.Snapshot(ctx, other, w)
	if err != nil || empty.Counts.Processed != 0 || empty.Tokens.Input != 0 || len(empty.Stages) != 0 {
		t.Fatalf("project leak: %#v %v", empty, err)
	}
	// Explain the real parameterized aggregate, and verify all query plans execute.
	rows, err := db.Query(ctx, "EXPLAIN (ANALYZE, FORMAT TEXT) "+runsSQL+snapshotSQL, project, w.Today, w.Now, w.Start, w.BucketStarts, w.BucketEnds, w.AttentionStart)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, "Execution Time") {
			t.Log(line)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// Exercise the shared generated readers against the migrated schema, not only
	// the overview projection: adding a column must preserve scan alignment.
	queries := remediationdb.New(db)
	activeID := pgtype.UUID{Bytes: uuid.MustParse(active), Valid: true}
	readRun, err := queries.GetRemediationRun(ctx, activeID)
	if err != nil || !readRun.StateEnteredAt.Valid || readRun.ModelTokensIn != 20 {
		t.Fatalf("generated run reader: %#v %v", readRun, err)
	}
	seriesRuns, err := queries.GetRemediationRunsBySeriesID(ctx, pgtype.UUID{Bytes: uuid.MustParse(s), Valid: true})
	if err != nil || len(seriesRuns) != 2 || seriesRuns[0].StateEnteredAt.Valid {
		t.Fatalf("generated history reader: %#v %v", seriesRuns, err)
	}
	incremented, err := queries.IncrementRunCounters(ctx, remediationdb.IncrementRunCountersParams{ID: activeID})
	if err != nil || incremented.ModelTokensIn != 20 || !incremented.StateEnteredAt.Valid {
		t.Fatalf("generated counter update: %#v %v", incremented, err)
	}
	// Calendar completion counts deduplicate two attempts of the same incident.
	calendarNow := time.Date(2040, 6, 10, 12, 0, 0, 0, time.UTC)
	calendarWindow, _ := application.NewWindow(calendarNow, "UTC", "today")
	exec(`UPDATE remediation_run SET ended_at=$1 WHERE ended_at IS NOT NULL`, calendarWindow.Today.Add(-time.Second))
	exec(`UPDATE remediation_run SET ended_at=$1 WHERE id=$2 OR id=$3`, calendarWindow.Today.Add(time.Second), legacy, success)
	calendar, err := repository.Snapshot(ctx, project, calendarWindow)
	if err != nil || calendar.Counts.TodayProcessed != 1 {
		t.Fatalf("today must count distinct incidents by completion date: %#v %v", calendar.Counts, err)
	}
	// Deleting a run removes its time-series entries with the same retention boundary.
	exec(`DELETE FROM remediation_run WHERE id=$1`, success)
	_ = db.QueryRow(ctx, `SELECT count(*) FROM remediation_token_usage WHERE run_id=$1`, success).Scan(&events)
	if events != 0 {
		t.Fatal("usage was not cascade deleted")
	}
	t.Log(fmt.Sprintf("Verified %d migrations, accounting, phase clocks, aggregates and pagination", len(files)))
}
