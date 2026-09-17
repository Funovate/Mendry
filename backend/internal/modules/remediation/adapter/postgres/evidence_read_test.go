package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"mendry/backend/internal/modules/remediation/adapter/postgres"
	"mendry/backend/internal/modules/remediation/domain"
)

// largeEvidenceInput 构造一条 payload 超过单页上限的 run 归属证据，覆盖
// cursor 分页路径。
func largeEvidenceInput(run domain.Run, projectID, environmentID, sourceID string) domain.StoredEvidence {
	payload, _ := json.Marshal(map[string]interface{}{
		"provider": "tencent_cls",
		"body":     strings.Repeat("trusted-detail-content-", 8192),
	})
	sum := sha256.Sum256(payload)
	return domain.StoredEvidence{
		ProjectID: projectID, EnvironmentID: environmentID, SourceID: sourceID,
		IncidentID: run.IncidentID, RunID: run.RunID,
		Provider: "tencent_cls", EvidenceKind: domain.EvidenceKindProviderDetail,
		DeduplicationKey: "evidence.read:large:" + hex.EncodeToString(sum[:]),
		Classification:   domain.EvidenceDirectFault,
		Outcome:          "success", Available: true, Primary: true,
		OperationalCorrelation: true,
		ContentHash:            hex.EncodeToString(sum[:]),
		ByteCount:              int64(len(payload)),
		Provenance: json.RawMessage(`{
			"adapter": "tencent_cls",
			"detail_capability_validated": true,
			"detail_resolution": "validated_provider_detail_get_alert_detail",
			"contradictions": []
		}`),
		Payload: payload,
	}
}

// TestEvidenceRead_OwnershipAndSinglePage 证明 evidence.read 只在同
// project/incident/series 且 attempt <= 目标 run 时返回证据；返回 stored
// classification、content hash 与 provenance（sourceId + sourceAttempt），
// 单页完整时不携带 cursor。
func TestEvidenceRead_OwnershipAndSinglePage(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()

	root := makeTerminalRun(t, pool, store, domain.RunStateFailed, 1, true)
	projectID, environmentID, sourceID := fixtureScopeIDs(t, pool, root.IncidentID)
	evidence, err := store.AppendEvidence(ctx, runtimeEvidenceInput(root, projectID, environmentID, sourceID, "10.16.6.17 43.131.29.186"))
	if err != nil {
		t.Fatalf("AppendEvidence: %v", err)
	}

	t.Run("same series returns the stored page", func(t *testing.T) {
		page, err := store.ReadEvidence(ctx, domain.EvidenceReadRequest{
			RunID: root.RunID, EvidenceID: evidence.EvidenceID,
		})
		if err != nil {
			t.Fatalf("ReadEvidence: %v", err)
		}
		if page.EvidenceID != evidence.EvidenceID || page.Kind != domain.EvidenceKindRuntime {
			t.Fatalf("page identity = %s/%s, want %s/runtime", page.EvidenceID, page.Kind, evidence.EvidenceID)
		}
		if page.StoredClassification != domain.EvidenceCorrelatedSupport {
			t.Fatalf("stored classification = %s, want correlated_supporting (stored, not model text)", page.StoredClassification)
		}
		if page.ContentHash != evidence.ContentHash {
			t.Fatalf("content hash = %s, want stored %s", page.ContentHash, evidence.ContentHash)
		}
		if page.Provenance.SourceID != sourceID || page.Provenance.SourceAttempt != root.AttemptNumber {
			t.Fatalf("provenance = %#v, want source %s attempt %d", page.Provenance, sourceID, root.AttemptNumber)
		}
		if page.Truncated || page.NextCursor != "" {
			t.Fatalf("single page = truncated=%t cursor=%q, want complete", page.Truncated, page.NextCursor)
		}
		if page.Content != string(evidence.Payload) {
			t.Fatalf("page content differs from stored payload")
		}
		if page.ByteCount != int64(len(evidence.Payload)) {
			t.Fatalf("page byte count = %d, want %d", page.ByteCount, len(evidence.Payload))
		}
	})

	t.Run("unknown evidence id is not found", func(t *testing.T) {
		_, err := store.ReadEvidence(ctx, domain.EvidenceReadRequest{
			RunID: root.RunID, EvidenceID: "00000000-0000-7000-8000-000000000000",
		})
		if !errors.Is(err, domain.ErrEvidenceReadNotFound) {
			t.Fatalf("ReadEvidence error = %v, want ErrEvidenceReadNotFound", err)
		}
	})

	t.Run("non-uuid evidence id is not found", func(t *testing.T) {
		_, err := store.ReadEvidence(ctx, domain.EvidenceReadRequest{
			RunID: root.RunID, EvidenceID: "not-a-uuid",
		})
		if !errors.Is(err, domain.ErrEvidenceReadNotFound) {
			t.Fatalf("ReadEvidence error = %v, want ErrEvidenceReadNotFound", err)
		}
	})

	t.Run("cross-incident evidence is not found", func(t *testing.T) {
		otherIncident := mustIncident(t, pool)
		otherProject, otherEnv, otherSource := fixtureScopeIDs(t, pool, otherIncident.String())
		otherRun := makeTerminalRunForIncident(t, pool, store, otherIncident, domain.RunStateFailed, 1, false)
		otherEvidence, err := store.AppendEvidence(ctx, runtimeEvidenceInput(otherRun, otherProject, otherEnv, otherSource, "other-host"))
		if err != nil {
			t.Fatalf("AppendEvidence other: %v", err)
		}
		_, err = store.ReadEvidence(ctx, domain.EvidenceReadRequest{
			RunID: root.RunID, EvidenceID: otherEvidence.EvidenceID,
		})
		if !errors.Is(err, domain.ErrEvidenceReadNotFound) {
			t.Fatalf("cross-incident ReadEvidence error = %v, want ErrEvidenceReadNotFound", err)
		}
	})

	t.Run("pre-run evidence remains readable", func(t *testing.T) {
		// Pre-run evidence snapshots the incident baseline. Align the fixture's
		// incident with the immutable series before appending the evidence.
		if _, err := pool.Exec(ctx,
			`UPDATE incidents SET lifecycle_generation = $2, deployed_commit = $3 WHERE id = $1`,
			root.IncidentID, root.LifecycleGeneration, root.DeployedCommit); err != nil {
			t.Fatalf("align pre-run baseline: %v", err)
		}
		payload, _ := json.Marshal(map[string]interface{}{"normalized": "alert"})
		sum := sha256.Sum256(payload)
		preRun := domain.StoredEvidence{
			ProjectID: projectID, EnvironmentID: environmentID, SourceID: sourceID,
			IncidentID: root.IncidentID,
			Provider:   "tencent_cls", EvidenceKind: domain.EvidenceKindNormalizedAlert,
			DeduplicationKey: "evidence.read:pre-run",
			Classification:   domain.EvidenceContextual,
			Outcome:          "success", Available: true,
			ContentHash: hex.EncodeToString(sum[:]),
			ByteCount:   int64(len(payload)),
			Provenance:  json.RawMessage(`{"adapter":"tencent_cls"}`),
			Payload:     payload,
		}
		stored, err := store.AppendEvidence(ctx, preRun)
		if err != nil {
			t.Fatalf("AppendEvidence pre-run: %v", err)
		}
		page, err := store.ReadEvidence(ctx, domain.EvidenceReadRequest{
			RunID: root.RunID, EvidenceID: stored.EvidenceID,
		})
		if err != nil {
			t.Fatalf("ReadEvidence pre-run: %v", err)
		}
		if page.Provenance.SourceAttempt != 0 {
			t.Fatalf("pre-run source attempt = %d, want 0", page.Provenance.SourceAttempt)
		}
		var gotContent, wantContent any
		if err := json.Unmarshal([]byte(page.Content), &gotContent); err != nil {
			t.Fatalf("decode pre-run page content: %v", err)
		}
		if err := json.Unmarshal(payload, &wantContent); err != nil {
			t.Fatalf("decode stored pre-run payload: %v", err)
		}
		if !reflect.DeepEqual(gotContent, wantContent) {
			t.Fatalf("pre-run page content = %#v, want %#v", gotContent, wantContent)
		}
	})
}

// TestEvidenceRead_AttemptMonotonicity 证明 evidence.read 遵循 attempt 单调性：
// child 可读早期 attempt 证据，但早期 run 不能读未来 attempt 的证据；证据行
// 不复制、不重新归属。
func TestEvidenceRead_AttemptMonotonicity(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()

	root := makeTerminalRun(t, pool, store, domain.RunStateFailed, 1, true)
	projectID, environmentID, sourceID := fixtureScopeIDs(t, pool, root.IncidentID)

	// child 创建要求 incident context version 与请求一致；显式推进后创建 attempt 2。
	setIncidentContextVersion(t, pool, root.IncidentID, 2)
	child, err := store.CreateNextAttempt(ctx, nextAttemptInput(root, domain.TriggerReasonManualContinue, 2))
	if err != nil {
		t.Fatalf("CreateNextAttempt: %v", err)
	}
	if child.AttemptNumber != root.AttemptNumber+1 {
		t.Fatalf("child attempt = %d, want %d", child.AttemptNumber, root.AttemptNumber+1)
	}

	futureEvidence, err := store.AppendEvidence(ctx, runtimeEvidenceInput(child, projectID, environmentID, sourceID, "future-attempt-output"))
	if err != nil {
		t.Fatalf("AppendEvidence future: %v", err)
	}

	t.Run("earlier run cannot read future attempt evidence", func(t *testing.T) {
		_, err := store.ReadEvidence(ctx, domain.EvidenceReadRequest{
			RunID: root.RunID, EvidenceID: futureEvidence.EvidenceID,
		})
		if !errors.Is(err, domain.ErrEvidenceReadNotFound) {
			t.Fatalf("ReadEvidence error = %v, want ErrEvidenceReadNotFound", err)
		}
	})

	t.Run("owning run reads its evidence", func(t *testing.T) {
		page, err := store.ReadEvidence(ctx, domain.EvidenceReadRequest{
			RunID: child.RunID, EvidenceID: futureEvidence.EvidenceID,
		})
		if err != nil {
			t.Fatalf("ReadEvidence: %v", err)
		}
		if page.Provenance.SourceAttempt != child.AttemptNumber || page.EvidenceID != futureEvidence.EvidenceID {
			t.Fatalf("page = %#v, want attempt %d evidence %s", page, child.AttemptNumber, futureEvidence.EvidenceID)
		}
	})

	t.Run("child can read earlier attempt evidence without row reassignment", func(t *testing.T) {
		rootEvidence, err := store.AppendEvidence(ctx, runtimeEvidenceInput(root, projectID, environmentID, sourceID, "root-attempt-output"))
		if err != nil {
			t.Fatalf("AppendEvidence root: %v", err)
		}
		page, err := store.ReadEvidence(ctx, domain.EvidenceReadRequest{
			RunID: child.RunID, EvidenceID: rootEvidence.EvidenceID,
		})
		if err != nil {
			t.Fatalf("ReadEvidence: %v", err)
		}
		if page.Provenance.SourceAttempt != root.AttemptNumber {
			t.Fatalf("source attempt = %d, want %d", page.Provenance.SourceAttempt, root.AttemptNumber)
		}
		// 行仍归属根 attempt，未复制到 child。
		var owner uuid.UUID
		if err := pool.QueryRow(ctx,
			`SELECT run_id FROM remediation_evidence WHERE id = $1`, rootEvidence.EvidenceID).Scan(&owner); err != nil {
			t.Fatalf("read evidence owner: %v", err)
		}
		if owner.String() != root.RunID {
			t.Fatalf("evidence owner = %s, want root run %s", owner, root.RunID)
		}
	})
}

func TestEvidenceRead_PreRunBaselineFailsClosedAcrossLifecycle(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	store := mustStore(t, pool)
	incidentID := mustIncident(t, pool)
	root := mustCreateRun(t, pool, incidentID, 1, "")
	projectID, environmentID, sourceID := fixtureScopeIDs(t, pool, root.IncidentID)
	payload := json.RawMessage(`{"normalized":"alert"}`)
	sum := sha256.Sum256(payload)
	stored, err := store.AppendEvidence(ctx, domain.StoredEvidence{
		ProjectID: projectID, EnvironmentID: environmentID, SourceID: sourceID,
		IncidentID: root.IncidentID, Provider: "tencent_cls", EvidenceKind: domain.EvidenceKindNormalizedAlert,
		DeduplicationKey: "baseline:pre-run", Classification: domain.EvidenceContextual,
		Outcome: "success", Available: true, Primary: true, TemporalCorrelation: true, OperationalCorrelation: true,
		ContentHash: hex.EncodeToString(sum[:]), ByteCount: int64(len(payload)),
		Provenance: json.RawMessage(`{"adapter":"tencent_cls"}`), Payload: payload,
	})
	if err != nil {
		t.Fatalf("append pre-run evidence: %v", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE remediation_evidence SET baseline_lifecycle_generation = NULL, baseline_deployed_commit = NULL WHERE id = $1`, stored.EvidenceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadEvidence(ctx, domain.EvidenceReadRequest{RunID: root.RunID, EvidenceID: stored.EvidenceID}); !errors.Is(err, domain.ErrEvidenceReadNotFound) {
		t.Fatalf("legacy-unbound read error = %v, want not found", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE remediation_evidence SET baseline_lifecycle_generation = 1, baseline_deployed_commit = '' WHERE id = $1`, stored.EvidenceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE incidents SET lifecycle_generation = 2, deployed_commit = 'def456' WHERE id = $1`, root.IncidentID); err != nil {
		t.Fatal(err)
	}
	newRoot := mustCreateRun(t, pool, incidentID, 2, "def456")
	if _, err := store.ReadEvidence(ctx, domain.EvidenceReadRequest{RunID: newRoot.RunID, EvidenceID: stored.EvidenceID}); !errors.Is(err, domain.ErrEvidenceReadNotFound) {
		t.Fatalf("cross-baseline read error = %v, want not found", err)
	}
	resolution, err := store.ResolveEvidence(ctx, newRoot.RunID, []domain.EvidenceCitation{{EvidenceID: stored.EvidenceID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolution.Records) != 0 || len(resolution.Sources) != 0 {
		t.Fatalf("stale baseline leaked into evidence resolution: records=%+v sources=%+v", resolution.Records, resolution.Sources)
	}
	if resolution.Correlation == nil || resolution.Correlation.Temporal || resolution.Correlation.Operational || resolution.Correlation.DirectBridge {
		t.Fatalf("stale baseline leaked into correlation: %+v", resolution.Correlation)
	}
	entries, err := store.ListContinuationEvidenceIndex(ctx, domain.ContinuationEvidenceQuery{SeriesID: newRoot.SeriesID, ThroughAttemptNumber: 1, Limit: 64})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.EvidenceID == stored.EvidenceID {
			t.Fatalf("cross-baseline evidence leaked into continuation index: %+v", entry)
		}
	}
}

// TestEvidenceRead_PagesLargePayload 证明超过单页上限的证据按 opaque cursor
// 分页：各页拼接后与存储 payload 一致，cursor 只对同一证据行有效。
func TestEvidenceRead_PagesLargePayload(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()

	root := makeTerminalRun(t, pool, store, domain.RunStateFailed, 1, true)
	projectID, environmentID, sourceID := fixtureScopeIDs(t, pool, root.IncidentID)
	evidence, err := store.AppendEvidence(ctx, largeEvidenceInput(root, projectID, environmentID, sourceID))
	if err != nil {
		t.Fatalf("AppendEvidence: %v", err)
	}
	if int64(len(evidence.Payload)) <= domain.MaxEvidenceReadPageBytes {
		t.Fatalf("fixture payload must exceed the page bound")
	}

	first, err := store.ReadEvidence(ctx, domain.EvidenceReadRequest{
		RunID: root.RunID, EvidenceID: evidence.EvidenceID,
	})
	if err != nil {
		t.Fatalf("ReadEvidence first page: %v", err)
	}
	if !first.Truncated || first.NextCursor == "" {
		t.Fatalf("first page = truncated=%t cursor=%q, want truncated with next cursor", first.Truncated, first.NextCursor)
	}
	if first.ByteCount > domain.MaxEvidenceReadPageBytes || int64(len(first.Content)) != first.ByteCount {
		t.Fatalf("first page bytes = %d content=%d, want within bound and equal", first.ByteCount, len(first.Content))
	}

	pages := []domain.EvidenceReadPage{first}
	seenCursors := map[string]struct{}{first.NextCursor: {}}
	cursor := first.NextCursor
	maxPages := len(evidence.Payload)/int(domain.MaxEvidenceReadPageBytes) + 2
	for cursor != "" {
		if len(pages) >= maxPages {
			t.Fatalf("evidence pagination exceeded %d pages", maxPages)
		}
		page, err := store.ReadEvidence(ctx, domain.EvidenceReadRequest{
			RunID: root.RunID, EvidenceID: evidence.EvidenceID, Cursor: cursor,
		})
		if err != nil {
			t.Fatalf("ReadEvidence page %d: %v", len(pages)+1, err)
		}
		if page.ByteCount > domain.MaxEvidenceReadPageBytes || int64(len(page.Content)) != page.ByteCount {
			t.Fatalf("page %d bytes = %d content=%d, want within bound and equal", len(pages)+1, page.ByteCount, len(page.Content))
		}
		pages = append(pages, page)
		cursor = page.NextCursor
		if cursor != "" {
			if _, duplicate := seenCursors[cursor]; duplicate {
				t.Fatalf("page %d repeated an earlier cursor", len(pages))
			}
			seenCursors[cursor] = struct{}{}
		}
	}
	last := pages[len(pages)-1]
	if last.Truncated || last.NextCursor != "" {
		t.Fatalf("last page = truncated=%t cursor=%q, want complete final page", last.Truncated, last.NextCursor)
	}
	var joined strings.Builder
	for _, page := range pages {
		joined.WriteString(page.Content)
	}
	if joined.String() != string(evidence.Payload) {
		t.Fatalf("joined pages differ from stored payload: %d vs %d bytes", joined.Len(), len(evidence.Payload))
	}
	second := pages[1]

	t.Run("replayed cursor returns the same next page", func(t *testing.T) {
		again, err := store.ReadEvidence(ctx, domain.EvidenceReadRequest{
			RunID: root.RunID, EvidenceID: evidence.EvidenceID, Cursor: first.NextCursor,
		})
		if err != nil {
			t.Fatalf("ReadEvidence replayed cursor: %v", err)
		}
		if again.Content != second.Content {
			t.Fatalf("replayed page content differs")
		}
	})

	t.Run("cursor is invalid for another evidence row", func(t *testing.T) {
		other, err := store.AppendEvidence(ctx, runtimeEvidenceInput(root, projectID, environmentID, sourceID, "other-output"))
		if err != nil {
			t.Fatalf("AppendEvidence other: %v", err)
		}
		_, err = store.ReadEvidence(ctx, domain.EvidenceReadRequest{
			RunID: root.RunID, EvidenceID: other.EvidenceID, Cursor: first.NextCursor,
		})
		if !errors.Is(err, domain.ErrEvidenceReadCursorInvalid) {
			t.Fatalf("cross-evidence cursor error = %v, want ErrEvidenceReadCursorInvalid", err)
		}
	})

	t.Run("tampered cursor is rejected", func(t *testing.T) {
		_, err := store.ReadEvidence(ctx, domain.EvidenceReadRequest{
			RunID: root.RunID, EvidenceID: evidence.EvidenceID, Cursor: first.NextCursor + "x",
		})
		if !errors.Is(err, domain.ErrEvidenceReadCursorInvalid) {
			t.Fatalf("tampered cursor error = %v, want ErrEvidenceReadCursorInvalid", err)
		}
	})

	t.Run("visible evidence fields cannot forge offset or expiry", func(t *testing.T) {
		forged := base64.RawURLEncoding.EncodeToString([]byte(`{"evidenceId":"` + evidence.EvidenceID + `","contentHash":"` + evidence.ContentHash + `","offset":1,"expiresAt":"2099-01-01T00:00:00Z"}`))
		_, err := store.ReadEvidence(ctx, domain.EvidenceReadRequest{
			RunID: root.RunID, EvidenceID: evidence.EvidenceID, Cursor: forged,
		})
		if !errors.Is(err, domain.ErrEvidenceReadCursorInvalid) {
			t.Fatalf("forged model-visible cursor error = %v, want ErrEvidenceReadCursorInvalid", err)
		}
	})

	t.Run("oversized cursor is rejected before the query", func(t *testing.T) {
		_, err := store.ReadEvidence(ctx, domain.EvidenceReadRequest{
			RunID: root.RunID, EvidenceID: evidence.EvidenceID,
			Cursor: strings.Repeat("x", domain.MaxEvidenceReadCursorBytes+1),
		})
		// 越界 cursor 在请求校验层被拒绝（gateway 参数校验先行，这里只是纵深防御）。
		if err == nil {
			t.Fatal("oversized cursor error = nil, want rejection")
		}
	})
}

// TestEvidenceRead_StoreContract 编译期断言 RunStore 满足 EvidenceReadPort；
// 不需要数据库，也不依赖 skip。
func TestEvidenceRead_StoreContract(t *testing.T) {
	var port domain.EvidenceReadPort = (*postgres.RunStore)(nil)
	if port == nil {
		t.Fatal("RunStore must satisfy EvidenceReadPort")
	}
}
