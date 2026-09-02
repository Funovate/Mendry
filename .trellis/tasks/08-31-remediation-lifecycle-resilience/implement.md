# Remediation Lifecycle Resilience Implementation Plan

## Phase 0: Preparation And Impact Review

- [x] Run `trellis-before-dev` and load backend remediation, database,
      error-handling, quality, frontend component/type, and cross-layer specs.
- [x] Reconcile this task with the related remediation task contracts; document
      superseded terminal/context rules rather than implementing both paths.
- [x] Use GitNexus `query/context` to trace diagnosis, continuation, review API,
      and publication flows.
- [x] Run GitNexus upstream `impact` before editing every function, class, or
      method. Report HIGH/CRITICAL blast radius before proceeding.
- [x] Capture an `INC-2270` fixture/replay that demonstrates the current silent
      gate rewrite and provider-detail loss before changing behavior.

## Phase 1: Durable Kernel

Progress markers: `[x]` = delivered slice (2026-08-31, first pass). Remaining Phase
1 work is tracked as follow-up slices; do not re-open completed items.

### Slice 4.5: Mandatory security and restart stabilization

- [x] Replace model-derivable evidence cursor signing with a random,
      server-state-backed capability bound to run/evidence/hash/offset/expiry;
      test that visible evidence ID/hash cannot forge offset or expiry.
- [x] Persist lifecycle-generation/deployed-commit baselines on evidence
      ingestion and require exact series-baseline matches for pre-run evidence;
      add cross-generation, cross-commit, and legacy-unbound rejection tests.
- [x] Correct checkpoint immutability so UPDATE remains forbidden while DELETE
      returns `OLD` and cascade cleanup remains possible; execute the real-DB
      delete scenario when PostgreSQL is available.
- [x] Persist and restore `BudgetPlanRecoveryV1` (plan, consumption, closed
      phases, current phase, frontier); serialize/restart/continue every phase
      boundary and compare projections/decisions.
- [x] Record checkpoint observed run version and implement durable-run-first
      reconciliation for the before-transition and after-transition crash
      windows; reject future/conflicting checkpoints.
- [x] Complete versioned project configuration -> root run immutable mode
      snapshot -> continuation inheritance, defaulting existing projects to
      `legacy`; test policy changes between root and continuation creation.
- [x] Remove superseded Docker-first evidence guidance, resolve actual
      validation commands, and complete the Phase 0 review gate before Slice 5.

- [x] Additive migrations and domain contracts for checkpoint events, latest
      working-memory snapshots. `000016_working_memory_checkpoint.up.sql` adds
      `remediation_checkpoint_event` (append-only, sha256, monotonic
      run+sequence, UPDATE-blocking immutability trigger that preserves DELETE
      for cascade lifecycle) and `remediation_working_memory` (latest snapshot
      with sequence forced to be event-backed). Run-mode snapshot added as
      `remediation_run.agent_loop_mode` (`legacy`|`resilient_v1`, default
      `legacy`). Domain `WorkingMemoryCheckpointV1`, `CheckpointSnapshot`,
      `CheckpointEvent`, `CheckpointStore` port, `AgentLoopMode`.
      Still pending: current recovery projection, phase allocations, reserved
      budgets (later slices).
- [x] Transactional checkpoint append/snapshot storage with canonical hash,
      optimistic sequence, run/series/context validation, and bounded JSON
      decoding. `adapter/postgres/checkpoint_store.go` + `CheckpointStore`;
      append+snapshot upsert in one transaction; load rejects wrong
      identity/schema/hash/unbacked sequence. Pending review integration-DB
      execution (tests skip without a reachable PostgreSQL).
- [x] Implement hybrid checkpoint triggers and provider-neutral conversation
      reconstruction; keep optional provider-native continuation process-local.
      Slice 5a (2026-09-01) delivered the forced+recovery trigger set and
      reconstruction behind `agentLoopMode=resilient_v1`: coordinator
      `checkpointRun` forced checkpoints at the preparing_context→diagnosing
      boundary, before plan/terminal transitions (uniform terminal hook in
      `transitionWithReason`), and after a strengthened soft-budget signal
      (recoverable `budget` challenge + recovery checkpoint); soft-budget
      allocator admission + mirror consumption in
      `recordSameStateBudget`/`transitionBudgeted` with no hard-ceiling change;
      evidence.read → checkpoint evidence index; diagnosis-continuation
      reconstruction from the predecessor durable checkpoint replacing the
      runtime-only evidence block; composition-root `SetCheckpointStore`.
      The automatic context/tool-output byte-threshold trigger remains a
      follow-up slice.
- [x] Replace runtime-only continuation bootstrap with a bounded same-series
      evidence index through the predecessor attempt. Delivered (2026-08-31,
      slice 3): `ContinuationEvidenceStore` gains
      `ListContinuationEvidenceIndex` (domain `EvidenceIndexEntry`: evidenceId/
      kind/provider/classification/sourceAttempt/contentHash, no payload);
      `queries/remediation.sql` `ListContinuationEvidenceIndex` covers all
      non-runtime kinds with the same incident/series/attempt<=through
      predicate as `ListContinuationRuntimeEvidence`; pre-run rows are readable
      only when their ingestion generation/commit matches the series
      (`sourceAttempt=0`), with legacy-unbound rows failing closed and no row
      copy/reassign; `adapter/postgres/store.go`
      mapping with bounded limit; `application/continuation.go`
      `renderContinuationEvidenceIndex` renders a compact
      `prior_evidence_index` block (bounded under `maxContinuationBriefBytes`,
      sanitized, re-readable by ID via `evidence.read`, never inlines payload);
      `application/coordinator.go` diagnosis continuation loads both the
      runtime records (unchanged) and the index; planning continuation stays
      compact. `evidence.read` advertisement extended to SSH/Docker sources in
      `baseDefinitions` (search/context remain cloud-only); analysis-only
      catalog unchanged.
- [x] Add `evidence.read` to the ToolGateway/catalog with opaque cursor paging,
      series ownership enforcement, byte bounds, audit, and checkpoint updates.
      Delivered (2026-08-31, slice 2): `domain/evidence_read.go`
      (`EvidenceReadPort`, paged page w/ provenance + opaque random cursor),
      `adapter/postgres/evidence_read.go` (`RunStore.ReadEvidence`,
      keyset-paged, attempt<=target EXISTS, pre-run rows readable, no row
      copy/reassign), `application/tool_gateway.go` (`ToolEvidenceRead`,
      fail-closed via run catalog), catalog advertisement alongside
      evidence.search/context, composition wiring. Slice 4.5 replaced the
      model-derivable MAC with a random 256-bit capability whose digest and
      run/evidence/hash/offset/expiry binding live only in PostgreSQL; TTL is
      5 minutes, offsets are rune-aligned, and pages remain <= 64 KiB. Audit via
      normal tool invocation.
      Slice 3 extended advertisement to SSH/Docker sources (connector-independent
      persisted read; `evidence.search`/`evidence.context` remain cloud
      log-window tools), so a continuation index entry is re-readable in any
      diagnosis catalog. Checkpoint evidence-index update from reads lands with
      the coordinator checkpoint-loop slice, not here.
- [x] `RecoveryChallengeV1` and progress fingerprints. Domain `recovery.go`
      (`RecoveryChallengeV1`, `FailureFingerprint`) + application `recovery.go`
      `NewRecoveryChallenge` builder reusing existing redaction; not yet wired
      into coordinator terminal routing. Still pending: recovery journal, safe
      active-run projection.
- [x] Implement soft phase allocations, later-phase/recovery reserves, forward
      transfer of unused capacity, and unchanged global hard ceilings.
      Delivered (2026-08-31, slice 4): `domain/budget_plan.go` adds
      `BudgetPlanV1` (schema `v1`: five `PhaseBudgetAllocation`s, `PhaseReserve`
      later-phase minimums, `RecoveryReserve`, concrete `BudgetLimits` ceiling)
      plus `BudgetAmount`, `BudgetPlanProjection`, `PhaseSoftRemaining`, phase
      order/allowlist helpers, and validation (non-negative amounts, phase
      allowlist, per-dimension soft+reserves ≤ ceiling);
      `application/budget_plan.go` adds `NewBudgetPlan` (zero values filled
      with conservative 15% soft / 5% later-phase reserve / 5% recovery
      defaults of the normalized ceiling) and the deterministic
      `phaseBudgetPlan` allocator (`AdmitPhase`/`Consume`/`ClosePhase`/
      `Available`/`Projection`) returning recoverable
      `within_allocation|soft_crossed|reserve_touched` signals: early phases
      borrow only the unreserved pool, standing later-phase/recovery reserves
      are never touched (AC10), and unused soft plus closed-phase reserves
      forward-transfer to the pool on close. `runBudget` and the hard
      `budget_exhausted` terminal are unchanged; coordinator wiring is a later
      slice.

## Phase 2: Diagnosis Vertical Slice

Progress markers: `[x]` = delivered increment (2026-09-02, INC-2270 core). Item 3 is
marked only for the fact-gate/evidence-correction part; protocol/tool/context/
budget challenge feedback beyond the fact gate remains later increments.

- [x] Remove mandatory provider/Docker/repository ordering from diagnosis
      prompts and express goal, trusted locators, capabilities, constraints, and
      completion criteria instead.
      Delivered (2026-09-02): `agent_engine.go` diagnosis prompt is now
      goal-oriented — objective + completion criteria, stable capability classes
      (repository / provider_evidence / runtime_logs / ssh_inspect) as
      locator/option hints, and an explicit "no required first tool and no
      mandated sequence" rule. A stack/path/function/line is a strong
      code-localization hint, not a log-skipping rule; runtime correlation is
      likewise not mandatory for every code fix. Removed the Docker-first
      ("request only docker.logs in the first collection turn and wait for its
      observation before requesting repository tools") and SSH-first ("first ls
      the hinted logPath") mandates; ssh.inspect ls guidance and docker coverage
      refinement guidance remain as non-ordered tool notes.
- [x] Split EvidenceGate into factual resolution and structured challenge;
      remove fixability/confidence rewriting and citation-mismatch contradiction.
      Delivered (2026-09-02, INC-2270 core): `domain.EvaluateEvidenceGate` no
      longer treats a citation classification mismatch as a material
      contradiction (R7); application `EvidenceGate.Apply` no longer mutates
      `Fixability` or `Confidence` (D4/R6) — it attaches and persists the
      `EvidenceAssessment` and merges gate missing-evidence/contradiction
      metadata only. `Evaluate`/`Apply` now return the correctable
      `[]CitationClassificationMismatch` (evidence ID + stored authoritative
      classification) alongside the decision. Hard gate semantics (missing
      direct evidence cap 0.39, unresolved time/correlation and genuine material
      contradictions cap 0.69, planning eligibility) are unchanged; the
      coordinator routes a hard-rejected `code_fixable` through the existing
      insufficient-evidence terminal path without entering planning.
- [x] Feed fact-gate, protocol, tool, context, and budget challenges back into
      the same conversation and checkpoint after strategy changes.
      Delivered (2026-09-02, fact-gate/evidence-correction part only): the
      diagnosis case after `EvidenceGate.Apply` appends a bounded
      `RecoveryChallengeV1` (kind `evidence_correction`, severity `recoverable`,
      reason `citation_classification_mismatch`, stored classifications in the
      sanitized message) and continues the loop (AC5); the challenged model turn
      is charged to the budget; resilient_v1 additionally records a
      `CheckpointRecovery{kind: evidence_correction, action: correct_citation}`
      and forces a recovery checkpoint (D2). Protocol/tool/context/budget
      challenge feedback beyond the fact gate remains later increments.
- [ ] Replace direct terminal paths for `stop`, empty
      `insufficient_evidence`, Docker refinement, and collect-loop limits with
      exhaustion proposals and service validation.
- [ ] Persist submitted diagnoses, accepted diagnoses, gate decisions, and
      bounded correction metadata without raw model turns.
- [ ] Pass the `INC-2270`, code-first-to-logs, continuation rehydration, malformed
      envelope, no-progress, and hard-blocker test matrix before enabling the
      feature flag for any project.

## Phase 3: Remaining Lifecycle

- [ ] Apply the same checkpoint/challenge protocol to planning output
      correction and policy/risk feedback.
- [ ] Checkpoint workspace/artifact identities around patching; let the agent
      inspect bounded workspace state and revise failed patches.
- [ ] Normalize validation failures into structured evidence and bounded patch
      revision turns while protecting validation/resolution reserves.
- [ ] Make publication recovery idempotent across process restarts and transient
      SCM failures; do not expose write credentials or bypass the human merge
      gate.
- [ ] Add forced checkpoint and restart tests before/after every phase transition
      and external effect.

## Phase 4: API, UI, Rollout, And Verification

- [ ] Add optional recovery/checkpoint projections to backend HTTP response and
      frontend schemas; preserve compatibility with legacy runs.
- [ ] Render active recovery separately from terminal manual review, including
      safe reason, attempted capability classes, next action, checkpoint age,
      and budget summary.
- [ ] Add and snapshot `agentLoopMode`; default to `legacy`, enable
      `resilient_v1` project by project, and record mode in audit/observability.
- [ ] Add metrics for challenge kind, recovery success, no-progress loops,
      compaction/reconstruction, exhaustion rejection/acceptance, phase budget,
      and terminal reason without high-cardinality evidence/model content.
- [ ] Run focused and race tests, backend-wide tests, frontend tests/type checks,
      vet/build/diff checks, migration tests, and Trellis check.
- [ ] Run GitNexus `detect_changes(scope=compare, base_ref=main)` and verify only
      the expected remediation, persistence, API, and UI flows are affected.

## Validation Scenarios

- Trusted provider detail contains stack/path/line and code inspection closes
  causality without mandatory Docker correlation.
- Code inspection is ambiguous; the agent switches to runtime logs, corrects a
  failed query, and returns to the exact deployed source.
- Provider classification differs from the submitted citation; the model
  corrects metadata without losing its causal/fixability conclusion.
- Continuation and process restart rehydrate earlier provider detail by ID.
- Each connector/tool fails alone while another capability succeeds.
- Context pressure triggers checkpoint/compaction with complete native tool
  message groups and evidence locators intact.
- An incomplete exhaustion proposal is rejected; a complete proof is accepted.
- Diagnosis reaches its soft limit without consuming planning, validation, or
  recovery reserves; the global ceiling still terminates deterministically.
- Patch, validation, and publication recover after interruption without
  duplicating an external effect.
- Legacy and resilient runs coexist and render correctly in the same incident
  review UI.

## Validation Commands

```bash
cd backend
go test ./internal/modules/remediation/application
go test -race ./internal/modules/remediation/application
go test ./internal/modules/remediation/...
go test ./...
go vet ./...
go build ./cmd/...

cd ../frontend
npm test -- --run
npm run typecheck
npm run build

cd ..
git diff --check
```

Use the repository's actual approved frontend commands if package scripts use
different names; record the resolved commands before task start rather than
inventing replacements during implementation.

## Rollback

- Switch new project runs to `agentLoopMode=legacy`.
- In-progress runs retain their snapshotted mode and may be allowed to finish or
  be cancelled through existing authority.
- Additive checkpoint/recovery rows and columns remain for audit and do not need
  destructive rollback.
- Provider-native continuation remains optional, so disabling it falls back to
  durable working-memory reconstruction.
