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
      identity/schema/hash/unbacked sequence. Verified against the development
      PostgreSQL database through the complete remediation adapter test package
      after applying migrations 16 and 17.
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

Progress markers: `[x]` = delivered increment; `[ ]` = partially delivered or
pending full acceptance coverage. Fact-gate and protocol recovery are delivered;
tool/context challenge unification and full lifecycle restart coverage remain open.

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
      classification) alongside the decision. In `resilient_v1`, every failed
      `code_fixable` fact check now persists the original submission and returns
      a structured challenge to the same loop; repeated identical no-progress
      decisions request an exhaustion proposal instead of rewriting fixability.
      Missing direct evidence and genuine material contradictions remain hard
      fact issues. Time, host identity, request correlation, and primary-source
      coverage are retained as audit facts but are no longer universal planning
      prerequisites when direct evidence and causal closure establish that the
      gaps are non-material. Legacy mode retains its rollout routing.
- [x] Feed fact-gate, protocol, tool, context, and budget challenges back into
      the same conversation and checkpoint after strategy changes.
      Delivered (2026-09-04): fact-gate/evidence-correction and protocol
      correction already use `RecoveryChallengeV1`; resilient repository/log
      tool failures now add the same bounded `tool_failure` challenge after
      invocation/budget persistence. Lifecycle patch/validation failures use
      the shared challenge builder as well; legacy routing remains unchanged.
- [x] Replace direct terminal paths for `stop`, empty
      `insufficient_evidence`, Docker refinement, and collect-loop limits with
      exhaustion proposals and service validation.
      Delivered (2026-09-03, feature-gated to `agentLoopMode=resilient_v1`):
      new `domain.ExhaustionProposalV1` (D6 JSON: unresolvedGoal /
      attemptedPaths{capability,outcomeRefs} / untriedCapabilities{capability,
      reasonCode unavailable|irrelevant|unsafe|budget_prohibited,evidenceRefs} /
      bestConclusion / handoff; bounded validation, known D1 capability classes,
      no inner schemaVersion — the envelope carries it) plus
      `RecoveryChallengeKindExhaustion`. New pure application validator
      `ValidateExhaustionProposal` checks structural bounds, coverage of every
      advertised catalog capability, and capability-bound action refs generated
      only for real persisted tool invocations. Migration 17 persists each
      service-issued `outcome_ref` together with `tool_name`, coarse outcome,
      sanitized error code, and bounded evidence IDs; continuation rehydrates
      that same ref, so the binding survives process loss. Recovery challenge
      refs and
      evidence from another capability cannot prove an attempted path. Untried
      `irrelevant` reasons require owned evidence; `unavailable`/`unsafe` require
      matching service policy state; `budget_prohibited` validates model call,
      model cost, tool, elapsed, and capability-specific byte dimensions.
      Incomplete proofs return a recoverable exhaustion challenge listing the
      uncovered capability/recovery class (AC8/AC9/R20). Coordinator
      wiring is gated by `resilientStateFrom(ctx) != nil`: `stop`, empty
      `insufficient_evidence`, Docker-refinement exhaustion, and collect-loop
      exhaustion now append a bounded D6 proposal request
      (`conversation.AppendExhaustionProposalRequest`, kind exhaustion,
      reasonCode exhaustion_proof_required) with a forced recovery checkpoint
      (D2) before re-prompting; a strictly decoded `exhaustion` envelope is
      service-validated, an accepted proof persists a bounded proposal-derived
      decision and transitions to `blocked_manual_review` with terminal reason
      `exhaustion_proof`, and an incomplete/malformed proposal returns a
      recoverable challenge (or protocol correction) and the loop continues.
      Review hardening (2026-09-03): the exhaustion intercept also honors the
      mandatory Tencent detail evidence gate — while `tencentDetailGateClosed`,
      an exhaustion envelope is rejected with `required_direct_evidence` exactly
      like diagnosis/stop, so a proof cannot bypass the gate to hand off.
      Budgeting reuses `recordSameStateBudget`/`transitionBudgeted` unchanged;
      only the global hard ceiling yields `budget_exhausted`. The mode gate also
      keeps `legacy` byte-for-byte: legacy default-mode coordinator tests
      (`TestCoordinator_StopReachesBlockedManualReview`,
      `TestCoordinator_InsufficientEvidenceBoundedLoop`,
      `TestCoordinator_InsufficientEvidenceWithoutCollectionStopsImmediately`,
      `TestCoordinator_BlocksRepeatedDiagnosisWithPendingDockerRefinement`, the
      causal-closure and stop-without-suggestion tests) pass unchanged and were
      not edited; legacy `handleTurnError` terminalization and direct
      stop/insufficient routing remain unchanged. In `resilient_v1`, repeated
      malformed envelopes now checkpoint protocol recovery and request a
      service-validated exhaustion proof instead of directly terminalizing.
      Existing resilient-mode tests that used terminal
      `insufficient_evidence` were updated to `unsafe_to_automate` (a D6 policy
      blocker that retains its direct terminal) so they keep asserting their
      checkpoint/soft-budget/continuation behavior without the new exhaustion
      turn; new tests cover the validator matrix and the resilient coordinator
      flows (stop->proposal->accept, incomplete->challenge->retry, insufficient,
      collect-loop, docker-refinement recovery via a narrower query, repeated
      malformed recovery, and malformed proposal -> protocol correction).
- [x] Persist submitted diagnoses, accepted diagnoses, gate decisions, and
      bounded correction metadata without raw model turns.
      Delivered (2026-09-03, D4 audit slice): migration `000017_submitted_diagnosis`
      adds the `remediation_submitted_diagnosis` audit table and additive durable
      outcome-ref/evidence columns on `remediation_tool_invocation` (per-run
      sequence, original pre-gate fixability/confidence/reasoning/citations/
      missing/contradictions/next-action, correction_kind/evidence/count/
      corrected, gate_outcome, nullable decision_id FK, semantic comments on
      every table/column, named CHECKs incl. fixability enum, confidence
      [0,1], bounded reasoning/arrays/next-action, correction-kind allowlist
      and corrected-requires-kind). Domain `SubmittedDiagnosis` +
      `SubmittedDiagnosisCorrection`/`SubmittedEvidenceClassification` with
      bounded `Validate`; companion port `domain.SubmittedDiagnosisStore`
      (`AppendSubmittedDiagnosis`/`ListSubmittedDiagnoses`/`LatestDecisionID`)
      keeps the frozen `RunStore` unchanged. `adapter/postgres` implements the
      port on `RunStore` with transactional append, series-lock-serialized
      sequence allocation, numeric confidence, JSONB correction evidence,
      same-run decision validation, run-FK validation, and fail-closed mapping.
      Checked-in sqlc artifacts are synchronized with the query contract and
      compile under the full backend suite; byte-idempotent `generate-check`
      remains pending because the pinned Go 1.26/sqlc toolchain could not reach
      `sum.golang.org` in this environment. Coordinator wiring (both modes; gated behind store
      capability) captures the pre-gate envelope and requires the submitted row
      to persist. Store capability, validation, latest-decision lookup, or
      append failure is a persistence blocker rather than a best-effort
      omission. PostgreSQL serializes sequence allocation with the existing
      series row lock, verifies decision ownership against the same run, and
      enforces correction kind/count/evidence consistency.
      Correction metadata is recorded when
      `CitationClassificationMismatch`s exist (kind `evidence_correction`,
      stored classifications, corrected=true) or with gate_outcome
      planning_eligible/rejected; the accepted `remediation_decision` row is
      linked via `LatestDecisionID` after `appendDecision` (frozen
      `AppendDecision` returns no ID; the mismatch-challenge turn stays
      unlinked); `handleExhaustionEnvelope` persists an accepted-proof row with
      correction kind exhaustion linked to its decision. The durable
      `remediation_tool_invocation` row also stores the service-issued action ref,
      bounded evidence IDs, coarse outcome, and sanitized error code before that
      ref is exposed to the model; audit failure is a persistence blocker and
      continuation rehydrates the same authority. Hard-rejected
      `code_fixable` keeps the model's ORIGINAL fixability/confidence in the
      submitted row (INC-2270 audit core); resilient mode challenges the same
      loop and never persists a silently rewritten diagnosis. Legacy rollout
      routing remains unchanged. No raw model turn/prompt/conversation text is
      persisted. Tests: domain bounds/consistency matrix, store integration
      round-trip + concurrent monotonic sequence + decision link + omit-raw-text,
      coordinator hard-reject/mismatch/exhaustion/legacy-routing/omit-raw-text
      cases; migration source test extended to 17 embedded versions. The full
      remediation PostgreSQL adapter package passes against the development
      database at schema version 17, including checkpoint/evidence continuation
      and concurrent audit sequence coverage.
- [ ] Pass the `INC-2270`, code-first-to-logs, continuation rehydration, malformed
      envelope, no-progress, and hard-blocker test matrix before enabling the
      feature flag for any project.
      AC1-AC6 and AC8 diagnosis regressions are green. The named AC7 test covers
      diagnosis-boundary reconstruction only; AC7's required before/after crash
      windows at every lifecycle phase remain in Phase 3, so this aggregate
      acceptance item is intentionally not marked complete.

      Diagnosis subset delivered (2026-09-04): additive regression matrix
      `backend/internal/modules/remediation/application/inc2270_regression_test.go`
      (test-only; no production behavior or existing test changed) maps named
      `TestINC2270Regression_AC1..AC8_*` tests 1:1 to AC1-AC8 using the existing
      fakes (`newCoordinator`/`sourceWiredCoordinator`, scripted models, fake
      stores), plus `TestINC2270Regression_FixtureLoaded` which loads
      `research/inc-2270-replay.json` (skips when the file is absent) and asserts
      both canonical replay contracts: attempt 1 (silent gate rewrite →
      structured challenge + preserved causal/fixability proposal, AC1/AC5) and
      attempt 2 (provider-detail loss → same-series `evidence.read` rehydration,
      AC2). No-progress and hard-blocker behavior is exercised through the AC4/
      AC6/AC8 flows and the existing legacy stop/insufficient tests. `go test
      ./...`, race, vet, build, gofmt, and `git diff --check` stay green.

      | AC | Test | Core assertion |
      |----|------|----------------|
      | AC1 | `TestINC2270Regression_AC1_TrustedLocatorsLeadCodeFirstAndCorrectionEntersPlanning` | First turn carries trusted stack/path/line/rawTime + evidence ID; agent inspects the exact deployed code first (no forced Docker-first order); classification challenge corrected; `code_fixable` preserved; enters planning without manual review |
      | AC2 | `TestINC2270Regression_AC2_ContinuationRehydratesEarlierProviderDetailByID` | Later attempt re-reads earlier provider detail by ID via `evidence.read` (index not degraded to runtime-only); tampered cursor cannot forge offset/expiry |
      | AC3 | `TestINC2270Regression_AC3_AmbiguousCodeFirstSwitchesToRuntimeThenBackToRepository` | Ambiguous code-first → SSH inspect failure → refined query → back to repository; run stays active |
      | AC4 | `TestINC2270Regression_AC4_ConnectorFailureKeepsAlternativeToolAndRunActive` | Log-API failure returns a safe retryable observation; repository/evidence.read alternatives remain advertised; run never terminalizes |
      | AC5 | `TestINC2270Regression_AC5_CitationClassificationMismatchIsCorrectedNotContradicted` | Mismatch corrected through the loop; 0.9 confidence and zero contradiction preserved |
      | AC6 | `TestINC2270Regression_AC6_RecoverableLoopFailuresPreserveDiagnosisClass` | Malformed envelope, oversized observation, provider-native continuation loss all recover with `code_fixable` intact (never degraded to `insufficient_evidence`) |
      | AC7 | `TestINC2270Regression_AC7_RestartReconstructsFromDurableCheckpoint` | Restart at a phase boundary reconstructs from the latest durable checkpoint with no opaque provider session |
      | AC8 | `TestINC2270Regression_AC8_IncompleteExhaustionProposalRejectedWithChallenge` | Early/incomplete proposal rejected; challenge lists every uncovered capability/recovery class; complete proof accepted with `exhaustion_proof` |

      Existing coverage reused without modification: `TestCoordinator_EvidenceCorrectionChallengeThenPlanning` (AC1/AC5), `TestCoordinatorContinueRendersSameSeriesEvidenceIndex` + `evidence_read_test.go` (AC2), Docker refinement + `TestCoordinator_SSHInspectHintsReachFirstTurnWithoutEagerRead` (AC3), `TestCoordinator_ConnectorFailureIsModelVisibleAndSafe` / `TestCoordinator_ConnectorUnavailableDoesNotBlockFirstTurn` (AC4), `TestResilient_ContinuationReconstructsFromDurableCheckpoint` (AC7), `TestCoordinator_ResilientIncompleteExhaustionProposalChallengesAndRetries` + `TestValidateExhaustionProposal` (AC8).

## Phase 3: Remaining Lifecycle

Phase 3 implementation is delivered as a resilient backend vertical slice. The
existing diagnosis and plan-review flow remains the human selection boundary;
`ApplyPlan` is the explicit, policy-authorized entry into patching. Legacy runs
keep the existing walking-skeleton behavior because all new lifecycle wiring is
additive and requires `resilient_v1` plus the companion stores/ports.

- [x] Apply the same checkpoint/challenge protocol to planning output
      correction and policy/risk feedback.
      Delivered: planning now evaluates bounded candidate path/risk policy,
      feeds recoverable `validation_revision` challenges into the same
      conversation, checkpoints strategy changes, and turns unchanged policy
      rejection into an explicit policy blocker.
- [x] Checkpoint workspace/artifact identities around patching; let the agent
      inspect bounded workspace state and revise failed patches.
      Delivered: `WorkspacePort`, `LifecycleToolGateway`, content-addressed
      patch results, immutable baseline/tree CAS, `ApplyPlan`, and a bounded
      patch tool loop are implemented. Workspace IDs and patch artifacts are
      restored from checkpoint/effect state after restart.
- [x] Normalize validation failures into structured evidence and bounded patch
      revision turns while protecting validation/resolution reserves.
      Delivered: approved command-ID/version validation, bounded result artifact
      references, runner-authoritative pass/fail, `validation_revision` feedback,
      changed-patch-only revision, and allocator handling that keeps later and
      recovery reserves protected during the backward validation-repair edge.
- [x] Make publication recovery idempotent across process restarts and transient
      SCM failures; do not expose write credentials or bypass the human merge
      gate.
      Delivered: `PublicationPort` has no merge/deploy operation; publication
      effects persist started/recoverable/succeeded state under a stable key,
      snapshot target branch/baseline, cap transient retries, restore successful
      branch/commit/change identities, and transition only to
      `awaiting_human_review`.
- [x] Add forced checkpoint and restart tests before/after every phase transition
      and external effect.
      Delivered: focused fake matrix covers restart after workspace, patch,
      validation, and publication effects; it verifies no duplicate external
      calls, stable artifact/tree/baseline identities, publication policy
      retention, same-key retry, and final human-review state. Boundary
      checkpoint ordering is asserted by the lifecycle flow tests.

Delivery status: Phase 3 production contracts, coordinator loop, persistence,
policy feedback, and lifecycle restart regressions are complete. Phase 4 remains
for API/UI recovery projections, rollout controls, metrics, and final cross-layer
acceptance.

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
