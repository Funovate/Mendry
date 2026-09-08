# Journal - yarns (Part 1)

> AI development session journal
> Started: 2026-08-10

---



## Session 1: 补齐本地配置字段注释

**Date**: 2026-08-13
**Task**: 补齐本地配置字段注释
**Branch**: `main`

### Summary

为全部本地运行与集成测试配置补充用途、格式、范围、关联约束和敏感信息说明；增加配置示例契约测试，并通过后端完整检查与 sqlc 生成一致性检查。

### Main Changes

- Moved `08-20-remediation-harness-observability` to the August archive.
- Kept all shared dirty-worktree business changes uncommitted.

### Git Commits

| Hash | Message |
|------|---------|
| `742d3ef` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 2: 补齐数据库和结构体字段注释

**Date**: 2026-08-13
**Task**: 补齐数据库和结构体字段注释
**Branch**: `main`

### Summary

为三张 PostgreSQL 表及 28 个字段补充 schema comment，通过 sqlc 生成带注释的 Go 数据模型，并增加 migration 与 catalog 覆盖检查。

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `43429c9` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 3: Enable credential and project name editing

**Date**: 2026-08-14
**Task**: Enable credential and project name editing
**Branch**: `main`

### Summary

Admins can rename a project and update existing credentials in place. Project key, routes, secret ID, and kind stay unchanged. Name-only credential edits keep ciphertext; a supplied replacement re-encrypts atomically. Environment name syncs only when it still equals the old project name. Specs now document the PATCH contracts and omitted-versus-empty value rule.

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `88e5128` | (see git log) |
| `f0e8238` | (see git log) |
| `2f0094e` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 4: PostgreSQL query debug SQL

**Date**: 2026-08-18
**Task**: PostgreSQL query debug SQL
**Branch**: `main`

### Summary

Added FIXTHE_POSTGRES_QUERY_DEBUG so query logs can emit one interpolated, copy-pasteable SQL statement. Console timestamps now include the date, and debug SQL is written on following physical lines instead of a quoted sql= field.

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `34fde54` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 5: SSH PEM credential import

**Date**: 2026-08-19
**Task**: SSH PEM credential import
**Branch**: `main`

### Summary

Added client-side PEM/OpenSSH file import and paste inspection on Git SSH and SSH log-source credentials; recorded frontend spec rules. Did not change backend secrets or convert PuTTY keys.

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `6aba5a5` | (see git log) |
| `7fa94ab` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 6: Signed inbound webhook URL and ingress

**Date**: 2026-08-19
**Task**: Signed inbound webhook URL and ingress
**Branch**: `main`

### Summary

Added a server-generated path token and public POST /hooks/{token} so alert systems can open or bump a P2 incident from opaque notification text. Configuration shows an admin-only copyable URL; HMAC is no longer required. Log search stays in the existing remediation harness.

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `2142337` | (see git log) |
| `4de0001` | (see git log) |
| `537ee64` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 7: Remove redundant configuration names

**Date**: 2026-08-20
**Task**: Remove redundant configuration names
**Branch**: `main`

### Summary

Closed the source/trigger alias-removal task after backend/frontend quality gates and a scoped commit. Also audited the leftover MVP task tree: archived completed or expired contracts (bootstrap, console API/prototype, connector framework, LLM providers, remediation-changes, 08-12 foundation, HTTP request debug) and left notes on the remaining foundation/domain/ingestion/evidence/harness work.

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `b8d7508` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 8: Archive remediation harness observability

**Date**: 2026-08-21
**Task**: Archive remediation harness observability
**Branch**: `main`

### Summary

Archived 08-20-remediation-harness-observability as explicitly requested. Business-code changes remain uncommitted in the shared dirty worktree; archive metadata was committed separately.

### Main Changes

(Add details)

### Git Commits

(No work commits; the archive-only commit is intentionally excluded.)

### Testing

- [OK] Confirmed the task is `completed` and absent from the active task list.

### Status

[OK] **Completed**

### Next Steps

- Commit the shared business-code changes later under their correct task boundaries.


## Session 9: Event stream date timestamps

**Date**: 2026-08-21
**Task**: Event stream date timestamps
**Branch**: `main`

### Summary

Updated Event stream timestamps to include localized year, month, day, and time through seconds; added semantic datetime metadata and Playwright regression coverage. Frontend lint, type-check, unit tests, build, full E2E, diff checks, and staged GitNexus change detection passed.

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `fcd7bfd` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 10: Archive remediation error observability

**Date**: 2026-08-21
**Task**: Archive remediation error observability
**Branch**: `main`

### Summary

Committed remediation harness error diagnostics and bounded SSH/tool/model failure details as 918480a; archived remediation-error-observability as 2f68cd6. Existing unrelated worktree changes were left untouched.

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `918480a` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 11: Reduce remediation prompt cost and expose cache metrics

**Date**: 2026-08-24
**Task**: Reduce remediation prompt cost and expose cache metrics
**Branch**: `main`

### Summary

Implemented deferred phase-aware MCP tool activation, incremental provider-native conversation, exact request/tool/cache observability, operation-admission budget semantics, and a full code-fixable harness proof; all Go tests, race, vet, build, and generation consistency checks passed.

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `56ae60d` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 12: Adjust remediation timeout model

**Date**: 2026-08-24
**Task**: Adjust remediation timeout model
**Branch**: `main`

### Summary

Added a five-minute shared logical model-turn timeout, raised the remediation run work ceiling to 20 minutes, enforced run-bounded model and tool contexts, and preserved terminal persistence after elapsed exhaustion.

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `ea85854` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 13: Archive remediation protocol correction

**Date**: 2026-08-25
**Task**: Archive remediation protocol correction
**Branch**: `main`

### Summary

Committed the confirmed remediation batch, including strict evidenceRef rejection, safe evidenceId protocol correction, post-validation provider history commits, phase-specific correction fallbacks, bounded three-failure recovery, regression tests, and remediation specs; archived task 08-25-remediation-protocol-correction. make check passed; generate-check remained blocked by sum.golang.org network access, while local sqlc generation was idempotent.

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `de41e83` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 14: Archive docker.logs pattern filtering

**Date**: 2026-08-27
**Task**: Archive docker.logs pattern filtering
**Branch**: `main`

### Summary

Committed docker.logs pattern filtering and coverage awareness (f8c6194): optional bounded pattern whitelist [a-zA-Z0-9 ._\-|()*?], context_before/after 0-100, tail max 2000 applied after filter, coverage summary fields, planning wire contract shared between prompt and protocol corrections with strict planCandidates validation (rationale/evidenceRefs/affectedFiles/intendedBehavior/rollbackStrategy required), git repository reads switched to configured production branch. go build ./..., go test -count=1 ./..., go vet all green; GitNexus impact LOW. Only task-scoped files committed; the repo's historical uncommitted baseline (auth/migrate/CLS/webhook fingerprint etc., ~170 files) intentionally left untouched. Also discovered earlier that 08-26-tencent-cls-detail-response-parser was archived without its code committed.

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `f8c6194` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 15: blocked_manual_review manual fix suggestion + stop handoff contract

**Date**: 2026-08-28
**Task**: blocked_manual_review manual fix suggestion + stop handoff contract
**Branch**: `main`

### Summary

Investigated why INC-2267 concludes blocked_manual_review: model diagnosis insufficient_evidence (nil-pointer panic in common.go:64 from fault-injection endpoint, but no trace/auth/host linkage), persisted terminal reason, retryable=false so the 11:20 repeated webhook did not auto-continue. Implemented: review top-level manualSuggestion (latest decision recommendedNextAction via sanitizeReviewText); prominent '人工修复建议 / Manual fix suggestion' block with missingEvidence list in RemediationPanel for blocked_manual_review; stop envelopes now require non-empty recommendedNextAction (schema minLength 1 + validateStop + required_stop_suggestion bounded correction, never direct terminalize), legal stop persists unsafe_to_automate decision before blocked_manual_review. Full backend suite + race, frontend lint/typecheck/unit/build, 22 Playwright E2E green. Committed whole remediation-continue-retry task (91 files, migrations 000005-000015) as feat(remediation); unrelated worktree changes left uncommitted. Spec updated (remediation-adapter-guidelines: stop handoff protocol, manualSuggestion contract, error matrix rows).

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `01f0544` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete

## Session 16: 08-31 Remediation Lifecycle Resilience — Phase 1 durable kernel

### Summary

Activated `.trellis/tasks/08-31-remediation-lifecycle-resilience` (was planning;
Now in_progress). Archived `08-28-expand-ssh-readonly-diagnostics` (AC1-AC11 all
[x], remediation tests green) before switching — per user decision. Scoped this
pass to Phase 1 "Durable Kernel" slice 1 only (durable working-memory checkpoint
kernel + provider-neutral recovery/run-mode contracts), per user choice to push
Phase 1 first. Coordinator-loop wiring, evidence.read, and phase-soft budgets are
explicitly deferred to later Phase 1 slices.

### Main Changes

- Migration `000016_working_memory_checkpoint.up.sql`: `remediation_run.agent_loop_mode`
  (`legacy`|`resilient_v1`, default `legacy`, snapshotted at run creation),
  `remediation_checkpoint_event` (append-only, sha256, monotonic run+sequence,
  immutability trigger that blocks UPDATE but allows DELETE for cascade lifecycle),
  `remediation_working_memory` latest snapshot with sequence forced event-backed.
- `domain/checkpoint.go`: `WorkingMemoryCheckpointV1` + nested types, `CheckpointSnapshot`,
  `CheckpointEvent`, `CheckpointStore` port, `AgentLoopMode`, bounded Validate/canonical
  encode/hash, evidence IDs required on verified facts (R15).
- `domain/recovery.go` + `application/recovery.go`: `RecoveryChallengeV1` (D5),
  `FailureFingerprint`, `NewRecoveryChallenge` builder reusing existing redaction.
- `adapter/postgres/checkpoint_store.go`: transactional append+snapshot upsert, optimistic
  sequence, fail-closed load (identity/hash/unbacked sequence).
- Additive `Run.AgentLoopMode` on domain Run + store mapping line; appended 8 checkpoint
  queries to `queries/remediation.sql`; sqlc regenerated (idempotent, v1.31.1).

### Git Commits

None yet — working tree still uncommitted (180 pre-existing changes from earlier
tasks + this slice). Commit deferred to Phase 3 (per workflow, do not commit mid-Phase).

### Testing

- `go test ./...` repo-wide green; remediation application + domain `-race` green.
- `go vet ./...` clean; `go build ./cmd/...` clean; `git diff --check` clean; `gofmt` clean.
- sqlc regenerate byte-identical vs committed output (make generate-check equivalent).
- 3 findings fixed by trellis-check: RunID identity check in AppendCheckpoint,
  ListCheckpointEvents identity verification, NewRecoveryChallenge budget-map clone.

### Status

Phase 1 slice 1 complete and reviewed. `implement.md` Phase 1 updated with progress
markers.

### Next Steps

- Later Phase 1 slices: hybrid checkpoint triggers + provider-neutral reconstruction;
  same-series evidence index bootstrap; `evidence.read` in ToolGateway/catalog; recovery
  journal + safe active-run projection; phase-soft budget/reserves.
- Run DB integration tests against a reachable test PostgreSQL before Phase 2 enablement
  (currently skip cleanly).
- Then Phase 2 diagnosis vertical slice (INC-2270 regressions).

## Session 17: 08-31 phase 1 slice 2 — evidence.read rehydration tool

### Summary

Continued Phase 1 of `08-31-remediation-lifecycle-resilience` with slice 2:
the `evidence.read` persisted-evidence rehydration backend + gateway tool
(PRD R9/R10, design D3; supports AC2 continuation rehydration and AC4.

### Main Changes

- `domain/evidence_read.go`: `EvidenceReadPort`, `EvidenceReadRequest{RunID,
  EvidenceID, Cursor}`, `EvidenceReadPage` (evidenceId/kind/storedClassification/
  provenance/contentHash/bounded content/nextCursor/truncated/byteCount),
  sentinels, 64 KiB page bound, 512B cursor bound.
- `adapter/postgres/evidence_read.go`: `RunStore.ReadEvidence` with opaque
  HMAC-SHA256 cursor (evidence-bound by evidenceId+contentHash, 5-min TTL,
  constant-time compare, rune-aligned slicing); `GetRemediationEvidencePage`
  keyset-paged, ownership via project/incident join + series + attempt<=target
  EXISTS + pre-run rows; no row copy/reassign.
- `application/tool_gateway.go`: `ToolEvidenceRead` registered/advertised where
  evidence.search/context are; schema evidenceId+cursor only; fail-closed via
  run catalog (model cannot supply run identity); mapping unknown->tool_unavailable,
  cursor->invalid_arguments. `tool_gateway_dynamic.go`, `tool_catalog.go`,
  `coordinator.go`, `internal/bootstrap/api.go` wiring.
- `queries/remediation.sql` + regenerated `remediationdb` (sqlc, idempotent).

### Git Commits

None yet — working tree uncommitted (deferred to Phase 3; many pre-existing
in-flight task changes present).

### Testing

- `go test ./...` repo-wide green; remediation 11 packages + `-race` green;
  `go vet ./...`; `go build ./cmd/...`; `git diff --check`; `gofmt` clean;
  sqlc byte-idempotent. trellis-check found no in-scope findings for this slice.

### Status

Phase 1 slice 2 complete and reviewed. `implement.md` updated.

### Next Steps

- Slice 3: hybrid checkpoint triggers + provider-neutral conversation
  reconstruction, and coordinator-loop feeding of gate/tool/protocol/budget
  challenges back into the same loop (highest-risk Phase 1 wiring).
- Slice 4: same-series evidence index bootstrap replacing runtime-only context;
  extend evidence.read advertisement to SSH/Docker sources.
- Slice 5: phase soft budget/reserves/forward-transfer (D7).
- Remaining Phase 2: goal-oriented prompt + EvidenceGate fact/challenge split +
  exhaustion proposal validation + INC-2270 regressions.
- Note: sibling continuation task `TestRunStore_ContinuationEvidenceSameSeriesReuse`
  needs a context-version bump (beyond this task's scope but will fail under a
  real DB in CI).

## Session 18: 08-31 phase 1 slice 3 — same-series evidence index + evidence.read for SSH/Docker

### Summary

Phase 1 slice 3 of `08-31-remediation-lifecycle-resilience`: bounded same-series
evidence index (PRD R10 / design D3) + extended `evidence.read` advertisement to
SSH/Docker sources (AC2 rehydration path).

### Main Changes

- `domain/evidence_persistence.go`: `EvidenceIndexEntry` (identity/metadata only,
  never payload) + additive `ListContinuationEvidenceIndex` on
  `ContinuationEvidenceStore`; `EvidenceIndexEntry` carries evidenceId/kind/
  provider/classification/sourceAttempt/contentHash.
- `adapter/postgres`: `ListContinuationEvidenceIndex` query (non-runtime kinds
  provider_detail/normalized_alert/repository/artifact + pre-run rows, same
  project/incident/series + attempt<=through, no row copy/reassign) +
  `store.go` `ListContinuationEvidenceIndex`/`mapEvidenceIndexEntry`; sqlc regen.
- `application/continuation.go`: `renderContinuationEvidenceIndex` compact
  `prior_evidence_index` (<=32 entries, shared 32 KiB budget, sanitized, no payload;
  R15 re-readable by ID). Runtime keeps existing `prior_runtime_evidence` inline.
- `application/coordinator.go`: diagnosis continuation loads runtime records
  (unchanged) + compact index; planning path stays compact (calls==0).
- `application/tool_catalog.go`: SSH/Docker branch advertises `ToolEvidenceRead`;
  `evidence.search/context` stay cloud-only; planning + analysis-only exclusions
  preserved.

### Git Commits

None yet — working tree uncommitted (deferred to Phase 3).

### Testing

- `go test ./...` repo-wide green; remediation packages + `-race` green;
  `go vet ./...`; `go build ./cmd/...`; `git diff --check`; `gofmt` clean;
  sqlc byte-idempotent. trellis-check found no findings for slice 3.
- Existing continuation contract tests preserved:
  `TestCoordinatorContinueLoadsPriorRuntimeEvidence`,
  `TestCoordinatorContinuePlanningKeepsCompactBrief` (continuationCalls==0 AND
  continuationIndexCalls==0), planning-checkpoint tests.

### Status

Phase 1 slices 1-3 complete and reviewed. `implement.md` markers updated.

### Next Steps

- Phase 1 remaining: (a) hybrid checkpoint triggers + provider-neutral conversation
  reconstruction + coordinator-loop feeding of gate/tool/protocol/budget challenges
  back into the loop (highest-risk wiring); (b) phase soft budget/reserves/forward
  transfer (D7, pure increment).
- `evidence.read` checkpoint evidence-index update lands with coordinator-loop slice.
- Run DB integration tests against a reachable test PostgreSQL before Phase 2
  (all integration-path tests currently skip).
- Phase 2: goal-oriented prompt + EvidenceGate fact/challenge split + exhaustion
  proposal validation + INC-2270 regressions.

## Session 19: 08-31 phase 1 slice 4 — phase soft-budget / reserves model (D7)

### Summary

Phase 1 slice 4 of `08-31-remediation-lifecycle-resilience`: the phase
soft-budget / reserves model (PRD R22/R23/AC10, design D7). Pure-additive
domain+application model + unit tests, NOT wired into the coordinator.

### Main Changes

- `domain/budget_plan.go`: `BudgetPlanV1`, `BudgetAmount` (6 hard-limit
  dimensions, no token counter), `PhaseBudgetAllocation` (diagnosing/planning/
  patching/validating/publishing), `PhaseReserve` + `RecoveryReserve`,
  `BudgetPlanProjection`/`PhaseSoftRemaining` (string-safe D8/D23 review),
  phase helpers, validation (non-negative, phase allowlist, per-dimension
  soft+reserves+recovery <= ceiling, positive ceiling); `Effect.BudgetAmount()`
  and `BudgetLimits.BudgetAmount()` conversions.
- `application/budget_plan.go`: `NewBudgetPlan` (zero-filled via
  `normalizeBudgetLimits` + `DefaultBudgetLimits`: 15% soft / 5% later-reserve /
  5% recovery defaults) and `phaseBudgetPlan` allocator: `AdmitPhase`
  (forward-only), `Consume` -> recoverable `softBudgetResult`
  (within_allocation|soft_crossed|reserve_touched — never a hard reason),
  `ClosePhase` (releases unused soft + closed-phase reserve into pool),
  `Available`, `Projection`.
- 18 unit tests covering the AC10/validation matrix.

### Key invariants (D7/AC10)

Early phases (diagnosis) can only borrow unreserved capacity; later-phase and
recovery reserves always stand. Crossing a soft allocation is a RECOVERABLE soft
signal, never `budget_exhausted` — only the global hard ceiling
(`runBudget`/`consume`) yields the existing hard terminal, and that code is
untouched. Unused capacity + closed-phase reserve forward-transfer after
`ClosePhase`.

### Git Commits

None yet — working tree uncommitted (deferred to Phase 3).

### Testing

`go test ./...` repo-wide green; domain+application `-race` green; `go vet ./...`;
`go build ./cmd/...`; `git diff --check`; `gofmt` clean. trellis-check found no
findings for slice 4.

### Status

Phase 1 slices 1-4 complete and reviewed. `implement.md` Phase 1 soft-budget
item now `[x]`.

### Next Steps

- Phase 1 slice 5 (last and highest-risk): hybrid checkpoint triggers +
  provider-neutral conversation reconstruction + coordinator-loop feeding of
  gate/tool/protocol/budget challenges back into the loop; wire the soft-budget
  allocator (`AdmitPhase`/`Consume`/`ClosePhase` + checkpoint pairing) and
  `evidence.read` checkpoint evidence-index updates into the coordinator; pair
  soft signals with forced checkpoints per D7.
- Run DB integration tests against a reachable test PostgreSQL before Phase 2.
- Phase 2: goal-oriented prompt + EvidenceGate fact/challenge split + exhaustion
  proposal validation + INC-2270 regressions.

## Session 20: 08-31 phase 1 slice 5a — feature-gated coordinator wiring

### Summary

Phase 1 slice 5a of `08-31-remediation-lifecycle-resilience`: the first,
feature-gated coordinator wiring increment. Connects the durable checkpoint
kernel (slice 1), evidence.read + index (slices 2-3), and soft-budget allocator
(slice 4) into the coordinator loop, strictly behind `agentLoopMode=resilient_v1`.
Legacy (default) mode is byte-for-byte identical.

### Main Changes

- `application/resilience.go` (NEW): per-run `resilientRunState` via context;
  `checkpointRun` (forced checkpoint helper), `buildCheckpoint`, `softBudgetRecovery`,
  `recordEvidenceRead`, `admitSoftBudget`, `renderCheckpointReconstruction`.
- `application/coordinator.go`: nilable `SetCheckpointStore`; `prepareContinuation`
  returns `preparedContinuation`; reconstruction load gated on child
  `AgentLoopMode==resilient_v1` + series/context match (best-effort); forced
  checkpoint after preparing_context→diagnosing boundary, before `plan`, and a
  uniform pre-terminal hook in `transitionWithReason`; soft mirror in
  `transitionBudgeted`; evidence.read index hook in `runTool`; `observedVersion`.
- `application/conversation.go`: `AppendRecoveryChallenge`.
- `application/budget.go`: `runBudget.remaining()`.
- `domain/recovery.go`: `RecoveryChallengeKindBudget`.
- `internal/bootstrap/api.go`: `NewCheckpointStore` + `SetCheckpointStore`
  (default project mode legacy). No migration/sqlc.

### Bug found by trellis-check (fixed)

`softBudgetRecovery` mirrored runBudget's cumulative elapsed (a set) into
phaseBudgetPlan.Consume (an add) -> elapsed grew Nx -> spuriously fired soft
signals/checkpoints and inflated projections. Fixed by tracking
`lastMirroredElapsed` and consuming per-call delta (clamped >=0); added
`TestSoftBudgetRecoveryMirrorsElapsedAsDelta` regression test. Rest confirms
legacy/nil-store byte-for-byte #1 invariant across sentinel suites.

### Git Commits

None yet — working tree uncommitted (deferred to Phase 3).

### Testing

- `go test ./...` repo-wide green; `-race` application+domain green; sentinels
  (continuation planning resume, runtime evidence, evidence index, compact brief,
  stale predecessor, evidence gate, budget, protocol-correction, resilient 8/8) all
  pass fresh. `go vet ./...`, `go build ./cmd/...`, `git diff --check`, `gofmt` clean.

### Status

Phase 1 slices 1-4 + 5a complete and reviewed. `implement.md` hybrid-triggers item
marked `[x]` (slice-5a scope: forced + recovery trigger sets; automatic
context/tool-output byte-threshold trigger remains a follow-up).

### Next Steps

- Phase 1 remains: automatic byte/context-threshold checkpoint trigger; allocator
  phase advancement/close at boundaries (AdmitPhase/ClosePhase); reconstruction
  augmenting (vs replacing) predecessor runtime evidence; plan-phase soft-budget
  consumption mirroring.
- Run DB integration tests against a reachable PostgreSQL before Phase 2.
- Phase 2 (diagnosis vertical slice): goal-oriented prompt + EvidenceGate
  fact/challenge split + exhaustion proposal validation + INC-2270 regressions.

## Session 21: 08-31 phase 2 increment 1 — EvidenceGate fact/challenge split + goal prompt (INC-2270 core)

### Summary

Phase 2 increment 1 of `08-31-remediation-lifecycle-resilience`: the INC-2270
core — EvidenceGate fact/correctable-challenge split (D4, R6/R7), goal-oriented
prompt cleanup (D1, R1-R4), and coordinator evidence-correction challenge
feedback (R6/R7/AC5).

### Main Changes

- `domain/evidence.go`: classification mismatch is no longer a material
  contradiction (R7). Added `CitationClassificationMismatch` (evidence ID +
  stored authoritative classification).
- `application/evidence_gate.go`: `Apply` no longer mutates `Fixability`
  (dropped silent `code_fixable -> insufficient_evidence`) nor overwrites
  `Confidence`; still attaches/persists `EvidenceAssessment` and merges gate
  metadata. Collects correctable citation-classification mismatches (dedup by ID).
  Hard gate semantics preserved (0.39 missing-direct, 0.69 unresolved/
  material-contradiction, planning-eligibility conjunction unchanged).
- `application/coordinator.go`: after gate, correctable mismatches ->
  `challengeEvidenceCorrections` appends bounded `RecoveryChallengeV1`
  (evidence_correction/recoverable/citation_classification_mismatch) and loops
  (re-prompt); resilient_v1 records durable recovery checkpoint + evidenceCorrectionAttempts.
  No change to routeDiagnosis/insufficient-evidence/Docker-refinement/collect-loop
  terminal paths (Phase 2 item 4 untouched).
- `application/agent_engine.go`: goal-oriented diagnosis prompt - objective +
  completion criteria + capability classes (repository/provider_evidence/
  runtime_logs/ssh_inspect) as locators/options; removed "request only
  docker.logs in first collection turn and wait" and SSH "first ls" mandates;
  no required-first-tool / no mandated sequence; wire contract preserved.

### Check verdict

trellis-check verified all four claimed behaviors + legacy/resilient reconciliation;
made a comment-only fix in `application/recovery.go`. Confirmed the pre-existing
`evidence_read_test.go` /docker assertion fix was legitimate (it genuinely failed
before this session, caused by the intentional slice-3 catalog change, not a
regression).

### Git Commits

None yet. Critical finding (research/staging-phase1-commit.md): HEAD `7674d39` is
not a Go module; the working tree contains a large uncommitted infra refactor
(module scaffold, migrate runner, platform/postgres, auth, redis, telemetry,
cmd/*, dev/*) that is a compile dependency for all Phase 1 code, plus in-flight
sibling-task work mixed into the same files. A Phase-1-only commit cannot build
on a clean checkout without the module scaffold; commit split needs careful
staging (documented in that file).

### Testing

`go test ./...` repo-wide green; remediation 11 packages + `-race` green; `go vet`;
`go build ./cmd/...`; `git diff --check`; `gofmt` clean. New tests: mismatch-not-
contradiction, hard-gate preservation, Apply-preserves-conclusion (persisted
assessment), coordinator challenge-then-planning (no terminalize, budget charged),
resilient recovery checkpoint, prompt goal-oriented no-order-mandates.

### Status

Phase 2 items 1-2 `[x]`; item 3 fact-gate/evidence-correction part `[x]`; items
3(rest)-6 open.

### Next Steps

- Phase 2 item 4: replace direct terminal paths (stop / empty insufficient_evidence /
  Docker refinement / collect-loop) with exhaustion proposals + service validation.
- Phase 2 item 5: persist submitted diagnosis (D4 "persist both" - model's original
  code_fixable for hard-rejected runs) + gate decision + correction metadata.
- Phase 2 item 6: INC-2270 regression matrix + provider-detail-loss regression.
- Resolve the commit-staging complication (HEAD not a Go module; entangled
  foundation refactor) before any commit.

## Session 22: Commit — landing backend foundation + Phase 1

### Summary

Solved the commit problem. Confirmed HEAD was not a Go module (0 tracked .go
files); the whole backend + shared foundation + in-flight sibling work were
uncommitted and entangled in the same files. Per user decision (single large
commit, accept infra inclusion), committed the entire buildable backend.

### Commits

- `3aa92fb` (pre-existing today, 10:15): "feat(remediation): add phase 1 durable
  kernel" — partial Phase 1 (created by an earlier op before this session's commit).
- `a9810be` (this session): "feat(remediation): landing backend foundation +
  lifecycle resilience Phase 1" — 309 files, +32.8k/-403: the full backend
  foundation scaffold (go.mod, migrate runner, platform/postgres, redis,
  telemetry, auth, cmd, dev, tools, sqlc), rest of Phase 1 (checkpoint kernel,
  evidence.read, evidence index, budget_plan, resilience wiring, Phase 2
  increment 1 INC-2270 core), .trellis tracking/spec, and .gitignore
  (backend/api binary).

### Commit hygiene

- Excluded: frontend/ (separate consumer + unrelated feature), frontend/
  screenshots/ (artifacts), agent tooling (.pi/.agents/.claude/.codex/
  AGENTS.md/CLAUDE.md), and the `backend/api` ELF build binary (added to
  .gitignore instead).
- `git diff --cached --check` exit 0; fixed 2 markdown trailing-blank warnings.
- Verified before commit: `go build ./...`, `go vet ./...`, `go test ./...` all
  green; after commit 0 backend modifications remain (committed tree == green
  working tree).

### Status

Backend is now fully committed and buildable. 08-31 task remains in_progress
(Phases 2(rest)-4 pending).

### Next Steps

- Commit frontend/ (separate commit) when ready.
- Resume Phase 2: item 4 (exhaustion proposals + service validation), item 5
  (persist submitted diagnosis), item 6 (INC-2270 regression matrix) in a fresh
  working session.

## Session 23: Commit frontend

### Summary

Committed the frontend configuration/LLM changes as a separate commit.

### Commits

- `0aa8ee3` "feat(frontend): configuration page + LLM provider wizard + project
  switcher" — 11 files (+375/-152): LLMStep wizard (new), ProjectSwitcher (new),
  configuration feature files, AppShell, configuration test.
- `6f50fea` "chore: record journal sessions 16-22" — journal tracking.

### Verification

npm run typecheck, npm run lint, npm test (35 passed), npm run build all green.
frontend/screenshots (PNG test artifacts) excluded.

### Remaining uncommitted (intentional)

.agents/.claude/.codex/.pi/AGENTS.md/CLAUDE.md (agent tooling), frontend/
screenshots/ (artifacts). Backend fully committed + buildable.

## Session 24: 08-31 phase 2 item 4 — exhaustion proposal + service validation

### Summary

Phase 2 item 4 of `08-31-remediation-lifecycle-resilience`: replaced the direct
terminal paths (model stop, empty insufficient_evidence, Docker-refinement
exhaustion, collect-loop limits) with `ExhaustionProposalV1` + service
validation (PRD R18-R20, design D6), feature-gated to `agentLoopMode=resilient_v1`.
Legacy byte-for-byte unchanged.

### Main Changes

- `domain/exhaustion.go`: `ExhaustionProposalV1` (D6 JSON mirror), attemptedPaths/
  untriedCapabilities, `ExhaustionReasonCode`, `KnownCapabilityClasses`, bounded
  Validate.
- `application/exhaustion.go`: pure deterministic `ValidateExhaustionProposal`
  (advertised-capability coverage, run-owned refs, open-recovery acknowledgement,
  budget-can't-support-viable-action); recoverable exhaustion challenge listing
  uncovered capability on incomplete proof (AC8/AC9); `requestExhaustionProposal`
  + `handleExhaustionEnvelope`.
- `domain/recovery.go` + `application/envelope.go` + `agent_engine.go`:
  `RecoveryChallengeKindExhaustion` + exhaustion envelope kind (strict decode).
- `coordinator.go`: resilient-only intercepts at stop/insufficient/Docker-
  refinement/collect-loop; accepted proof -> blocked_manual_review
  (terminal_reason exhaustion_proof); incomplete -> recoverable challenge + loop;
  malformed -> protocol correction; forced checkpoint; budget charged normally.
- `resilience.go`: tracker exhaustionProposalAttempts/actionRefs/ownedRefs/
  openRecoveryRefs; `conversation.go` AppendExhaustionProposalRequest.

### Check findings fixed (2)

1. Run-owned refs omitted trusted bootstrap/prior evidence -> false rejection of
   INC-2270-style proofs citing first-turn provider detail; fixed by recording
   priorInvocations evidence IDs (runQueued) + bootstrap record IDs (drive,
   resilient-only).
2. Exhaustion envelope bypassed the mandatory Tencent detail gate; fixed:
   while tencentDetailGateClosed(), exhaustion rejected with
   required_direct_evidence like diagnosis/stop.

### Legacy preservation

13 legacy sentinel tests (stop, insufficient-evidence, Docker refinement,
protocol-correction) pass fresh, byte-identical, not edited. 7 resilient tests
switched terminal insufficient_evidence -> unsafe_to_automate (policy blocker
retaining direct terminal) with comments; assertions intact.

### Validation

`go test ./...` repo-wide green; remediation 11 packages + `-race` green; go vet;
go build ./cmd/...; git diff --check; gofmt clean. GitNexus detect_changes: only
expected remediation flows; drive risk high (expected, mitigated by legacy gate).

### Status

Phase 2 items 1-4 `[x]`; item 5 (persist submitted diagnosis) and item 6
(INC-2270 regression matrix) remain.

### Next Steps

- Phase 2 item 5: persist submitted diagnosis (D4 "persist both" - model's
  original diagnosis incl. hard-rejected code_fixable) + gate decision +
  bounded correction metadata, without raw model turns.
- Phase 2 item 6: INC-2270 regression matrix (AC1-AC8) incl. provider-detail-loss.

## Session 25: 08-31 phase 2 item 5 — submitted-diagnosis audit persistence (D4 "persist both")

### Summary

Phase 2 item 5 of `08-31-remediation-lifecycle-resilience`: persist the
submitted (pre-gate) diagnosis + gate decision link + bounded correction
metadata, WITHOUT raw model turns (D4/R5/R7/R8, auditability).

### Main Changes

- Migration `000017_submitted_diagnosis.up.sql`: additive
  `remediation_submitted_diagnosis` (per-run sequence, original fixability/
  confidence/reasoning/citations/missing/contradictions/next-action,
  correction_kind/correction_evidence/correction_count/corrected,
  gate_outcome, nullable decision_id FK). Comments on all 17 columns, named
  CHECKs, additive-only, rollback = drop table.
- `domain/submitted_diagnosis.go`: `SubmittedDiagnosis` + correction types +
  `SubmittedDiagnosisStore` companion port (Append/List/LatestDecisionID).
- `adapter/postgres/submitted_diagnosis.go`: transactional append + list + FK
  validation (sequence = len+1); `application/submitted_diagnosis.go`:
  pre-gate projection, mismatch->correction metadata, gate-outcome projection,
  best-effort append.
- Coordinator: capture pre-gate envelope, persist submitted row with mismatch
  correction metadata / gate outcome, link accepted decision; exhaustion
  accepted-proof row (kind exhaustion). Frozen `domain.RunStore` untouched
  (companion port pattern). Audit gated by store capability, not mode.
- sqlc regen (v1.31.1 byte-idempotent).

### INC-2270 audit core

Hard-rejected code_fixable keeps model ORIGINAL fixability/confidence in the
submitted row (gate_outcome=rejected); accepted remediation_decision stays
routed insufficient_evidence. Correction metadata carries only
service-authoritative storedClassification; no raw model text.

### Check

trellis-check verified all contract points; applied one fix (explicit
json tags on SubmittedEvidenceClassification for the jsonb round-trip).
Legacy regression tests unchanged/pass; 17 migrations pass.

### Validation

go test ./... repo-wide green; remediation 11 packages + -race green; go vet;
go build; git diff --check; gofmt clean; sqlc byte-idempotent.

### Status

Phase 2 items 1-5 `[x]`. Item 6 (INC-2270 regression matrix AC1-AC8) remains.

### Next Steps

- Phase 2 item 6: INC-2270 regression matrix (AC1-AC8) incl. provider-detail-loss
  and continuation rehydration; then Phase 3 (planning/patch/validation/
  publication recovery) and Phase 4 (API/UI/rollout).

## Session 26: 09-04 phase 2 item 6 — INC-2270 regression matrix (AC1-AC8)

### Summary

Phase 2 item 6 of `08-31-remediation-lifecycle-resilience`: additive
consolidated regression matrix mapping AC1-AC8 1:1 to named
`TestINC2270Regression_ACn_*` tests plus fixture wiring for the two canonical
INC-2270 replay contracts. Test-only: no production code, migration, sqlc, or
port changed; no existing test file modified.

### Main Changes

- `application/inc2270_regression_test.go` (new, ~950 lines, test-only):
  - `TestINC2270Regression_FixtureLoaded` loads
    `.trellis/tasks/08-31-.../research/inc-2270-replay.json` (skips when
    absent) and asserts both canonical contracts: attempt 1 silent gate
    rewrite → structured challenge + preserved causal/fixability proposal
    (AC1/AC5); attempt 2 provider-detail loss → same-series `evidence.read`
    rehydration without crossing generation/commit (AC2).
  - AC1: first turn carries trusted stack/path/line/rawTime + evidence ID;
    code-first inspection with no forced Docker-first order;
    classification challenge corrected; `code_fixable` preserved; planning
    entered without manual review.
  - AC2: continuation renders the same-series evidence index (not degraded to
    runtime-only) and re-reads earlier provider detail by ID via
    `evidence.read`; tampered cursor rejected as invalid_arguments
    (server-authoritative cursor).
  - AC3: ambiguous code-first → SSH inspect failure (retryable observation) →
    refined query → runtime evidence persisted → back to repository; run
    stays active.
  - AC4: log-API connector failure returns a safe retryable observation;
    repository/evidence.read alternatives remain advertised; run never
    terminalizes.
  - AC5: citation classification mismatch corrected through the loop with 0.9
    confidence and zero contradiction preserved.
  - AC6: malformed envelope, oversized tool observation (bounded at 64 KiB),
    and provider-native continuation loss all recover with `code_fixable`
    intact (never degraded to `insufficient_evidence`).
  - AC7: restart reconstructs from the predecessor's latest durable
    checkpoint with no opaque provider session; child checkpoints at phase
    boundary and before terminal.
  - AC8: incomplete/early exhaustion proposal rejected with a recoverable
    challenge listing every uncovered capability/recovery class; complete
    proof accepted with terminal reason `exhaustion_proof`.
  - Local fakes only: `sequenceInspectPort` (canned SSH results) and
    `countingCheckpointStore` (load-target recording); everything else reuses
    existing fakes (`newCoordinator`/`sourceWiredCoordinator`, scripted
    models, fake stores, `eligibleAutomaticAggregate`, evidence.read fakes).
- `implement.md`: Phase 2 item 6 marked `[x]` with the AC→test mapping table
  and existing-coverage reuse notes.

### Check

Check agent verified each AC test asserts the AC's core observable behavior
against the existing fakes; fixture identity/sanitized contracts;
no flaky/racy patterns (no sleeps/network/parallel; AC7 ran 10x and the
suite 5x deterministically); no production symbols invented; mtime ordering
confirms the regression file was the only new file of the delivery.

### Validation

- Focused: 9/9 `TestINC2270Regression_*` pass (including sub-tests) under
  `-count=1` and `-count=5`, plus `-race` on the focused set.
- `go test ./internal/modules/remediation/...` green (all 13 packages);
  `go test -race ./internal/modules/remediation/application ./internal/modules/remediation/domain` green.
- Repo-wide `go test ./...`: 0 failures.
- `go vet ./...`, `go build ./cmd/...`, `gofmt -l <file>` (empty),
  `git diff --check` all clean.

### Status

Phase 2 complete: items 1-6 `[x]`. Next: Phase 3 (planning/patch/validation/
publication recovery) and Phase 4 (API/UI/rollout).

## Session 26: 08-31 phase 2 item 6 — INC-2270 regression matrix (AC1-AC8)

### Summary

Phase 2 item 6 of `08-31-remediation-lifecycle-resilience`: consolidated
INC-2270 regression matrix. Additive test-only work wiring the two INC-2270
replay scenarios and proving AC1-AC8 with named regression tests.

### Main Changes

- `application/inc2270_regression_test.go` (NEW, 952 lines, test-only):
  `TestINC2270Regression_AC1..AC8_*` + `TestINC2270Regression_FixtureLoaded`.
- `.trellis/.../implement.md`: Phase 2 item 6 `[x]` + AC->test mapping table.
- No production code, no migrations/sqlc/ports, no existing tests modified.

### AC coverage

AC1 trusted-locators code-first + correction enters planning; AC2 continuation
rehydrates earlier provider detail via evidence.read (tampered cursor rejected);
AC3 ambiguous code-first switches to SSH, refines query, returns to repo; AC4
connector failure keeps alternative tool + run active; AC5 mismatch corrected
not contradicted (0.9 confidence preserved); AC6 malformed/oversized/
continuation-loss recover preserving code_fixable; AC7 restart reconstructs from
latest durable checkpoint (no opaque session); AC8 incomplete exhaustion
proposal rejected with challenge listing uncovered capability.

### Check

trellis-check reviewed line-by-line (952 lines): test-only confirmed, AC tests
assert genuine observable behavior (not vacuous), fixture wiring pins both
INC-2270 failure modes; only fix was journal doc update. 9/9 regression tests
pass, no flakes (AC7 10x), -race clean.

### Validation

go test ./... repo-wide green; remediation 11 packages; -race green; go vet;
go build ./cmd/...; git diff --check; gofmt clean.

### Status

Phase 2 COMPLETE (items 1-6 all `[x]`).

### Next Steps

- Phase 3: remaining lifecycle recovery — planning output correction/policy
  feedback; workspace/artifact checkpointing around patching; validation
  failure -> structured evidence + patch revision turns; idempotent publication
  recovery; forced checkpoint + restart tests at every phase boundary.
- Phase 4: API/UI projections, agentLoopMode rollout, metrics, full verification
  (focused/race/backend-wide/frontend/vet/build/diff/Trellis check/GitNexus
  detect_changes).


## Session 16: Archive remediation detail UI

**Date**: 2026-09-04
**Task**: Archive remediation detail UI
**Branch**: `main`

### Summary

Verified the remediation detail UI with frontend lint, typecheck, unit tests, build, and 24 Playwright E2E tests; committed the layout and unified diff viewer, then archived 09-03-remediation-detail-ui.

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `891d53f` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 17: 08-14 walking skeleton quality checkpoint

**Date**: 2026-09-04
**Task**: 08-14 walking skeleton quality checkpoint
**Branch**: `main`

### Summary

Completed Step 9 using four real pilot incidents and five known attempts. Recorded a conditional go for diagnosis/advisory integration and a no-go for unattended production repair or broad rollout; live INC-2390 had 3/3 resolvable citations, two plans, and a suggested diff. Backend full tests, remediation race, vet/build, frontend lint/typecheck/40 unit tests/build, and 24 Playwright E2E tests passed. Archived 08-14-remediation-walking-skeleton; unrelated agent config and screenshots were left untouched.

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `8af0709` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 18: Integrate bilingual hero flow illustration

**Date**: 2026-09-08
**Task**: Integrate bilingual hero flow illustration
**Branch**: `main`

### Summary

Replaced only the bilingual docs introduction hero's right-side screenshot with a local WebGL four-stage evidence-to-human-review illustration. Added local fallback/vendor assets, localized semantic labels and lifecycle controls, media metadata/OG references, focused browser/build contracts, and documented the canvas fallback pattern. Preview/public builds, docs checks, audit, full E2E (50 passed, 2 skipped), visual screenshots, and GitNexus low-risk detection passed; unrelated worktree changes remained untouched.

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `4368588` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 19: Archive docs homepage workflow polish

**Date**: 2026-09-08
**Task**: Archive docs homepage workflow polish
**Branch**: `main`

### Summary

Polished the bilingual docs homepage workflow, verified the full docs quality gate, and archived 09-08-docs-homepage-workflow-polish.

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `c8d40a9` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete
