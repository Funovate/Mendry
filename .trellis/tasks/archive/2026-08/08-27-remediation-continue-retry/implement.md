# Implementation Plan: Remediation Continue And Retry Flow

## Phase A: Domain And Persistence Contracts

1. Add provider-neutral continuation metadata and typed next-attempt input/errors without changing the frozen `RunStore` method signatures.
   - Extend `NewRun`, `Run`, `Effect`, `RunAggregate`, and safe attempt summary types with context version, origin, predecessor, terminal reason, and retryability.
   - Add the companion `AttemptStore`/continuation contract used by the trigger and coordinator.
   - Add domain tests for active/terminal/automatic/manual eligibility and bounded metadata.

2. Add an additive remediation attempt migration.
   - Add predecessor link, origin/reason, context version, terminal reason, and retryable columns to `remediation_run`.
   - Add constraints and comments; preserve old rows with conservative defaults.
   - Update remediation sqlc queries for root metadata, row locking, and atomic next-attempt insertion; regenerate generated Go files.

3. Implement PostgreSQL `CreateNextAttempt`.
   - Lock the series row, verify the expected latest predecessor/version and immutable incident/generation/commit identity, confirm the requested context version still matches the incident row, reject active or unsupported states, enforce the three automatic-continuation ceiling, and insert exactly one queued child attempt.
   - Map duplicate/stale/active/eligibility results to safe typed categories.
   - Include attempt summaries in `Get` aggregates while keeping decision/plan/artifact/tool data attached to their owning run.
   - Add PostgreSQL tests for concurrent/stale attempts, numbering, ceiling, rollback, and old-row compatibility.

## Phase B: Coordinator And Trigger Behavior

4. Add typed terminal metadata to coordinator transitions.
   - Classify transient provider/runtime/output-exhaustion failures as retryable only through explicit safe categories.
   - Persist safe terminal reasons and retryability through the existing `Effect` transition path.
   - Keep budget, policy, authorization, persistence, and manual-review outcomes non-automatic.
   - Preserve existing observer/log redaction and add tests for the classification matrix.

5. Refactor the queued driver to support continuation briefs and phase-aware resume.
   - Keep `Start` root creation/idempotency unchanged.
   - Add `RemediationCoordinator.Continue` and share the queued drive/final observation path.
   - Build a bounded structured brief from predecessor metadata and prepend it to the current persisted evidence snapshot.
   - Render persisted operational evidence into trusted model context without review/log redaction, field deletion, or value replacement; keep count/byte limits only as context-budget bounds.
   - Resume directly in planning from the latest durable `code_fixable` checkpoint anywhere in the continuation chain at or before the expected predecessor, but only when its series and context version exactly match the child; a newer context always restarts diagnosis.
   - Terminalize a valid `insufficient_evidence` diagnosis immediately when it requests no collection tools; only non-empty `collectMoreContext.toolCalls` consume the bounded collection loop.
   - For manual page continuation, build the catalog without MCP discovery and expose only repository list/read/search/history; never expose Tencent detail, Docker logs, SSH inspect, evidence search/context, source discovery, or dynamic tools.
   - Keep provider messages, adapter-injected credentials, and control-plane configuration outside evidence, model context, page responses, and logs.
   - Add fake-model tests proving manual continuation performs zero external evidence calls, original persisted evidence values reach the model, and planning continuation performs no diagnosis/log calls.

6. Turn `Trigger.Emit` into the webhook automatic gate and add shared `Trigger.Continue`.
   - Keep root behavior for a missing series and no-op for queued/active/usable terminal runs.
   - On an open repeated webhook with a newer context version, continue only retryable failed runs below the automatic ceiling.
   - Treat expected stale/active/ceiling races as accepted no-ops; report storage/control-plane failures through the safe background reporter.
   - Add tests for new root, repeated open occurrence, new-context requirement, active/usable/non-retryable skips, concurrency, and automatic ceiling.

7. Wire the enriched inbound path to the gate after evidence persistence.
   - Preserve `202` and detached request cancellation semantics.
   - Invoke automatic continuation only after the occurrence/evidence write succeeds; keep evidence failure from starting a child attempt.
   - Preserve `Info`, closed/recovered, fingerprint, lifecycle-generation, and deployed-commit behavior.
   - Update incident/hooks tests without weakening existing root transaction coverage.

## Phase C: Protected API And Console

8. Add the manual continuation application use case and HTTP route.
   - Require project incident-write capability, canonical incident ID, current generation, expected latest run ID, and positive version.
   - Allow only `failed`, `budget_exhausted`, and `blocked_manual_review`; reject active, plan-ready, and completed-non-code runs.
   - Map stale/active/not-eligible/not-found cases to stable safe HTTP errors.
   - Keep `/remediation/start` for initial manual start when no series exists.
   - Add handler/application tests for authentication, role/capability, stale generation/version, duplicate clicks, and response metadata.

9. Expand the remediation review contract.
   - Return current attempt metadata, continuation availability, safe terminal fields, and bounded attempt history.
   - Ensure project authorization is applied before latest-run/history lookup and no internal credential/provider payload is exposed.
   - Update backend review tests and frontend Zod/API contract tests.

10. Add page controls and mutation states.
    - Add `startRemediation` and `retry/continueRemediation` API methods with strict DTO validation.
    - Update `RemediationPanel` to show `Start remediation` for missing runs and `Continue analysis` only when server capability/state allows it; submit generation/run/version from the response.
    - Treat `Continue analysis` as analysis-only over the current persisted evidence snapshot; evidence refresh is not an implicit side effect of the page mutation.
    - Invalidate the project-scoped remediation query after success; render pending, conflict, error, active, and viewer states.
    - Render attempt history/origin/terminal reason without putting controls in a nested card or exposing operational evidence payloads through the page.
    - Extend Playwright mocks and E2E assertions for operator success, viewer omission, request body/path, and stale conflict.

## Phase D: Verification And Finish

11. Run focused backend tests for domain, remediation application, PostgreSQL store, incidents, hooks, and HTTP handlers.
12. Run frontend lint, typecheck, unit tests, build, and remediation E2E tests.
13. Run backend formatting, vet, full tests, race tests, command build, and `make generate-check`.
14. Run `git diff --check`; inspect generated SQL/Go diffs and all touched files for preservation of unrelated worktree changes.
15. Run GitNexus `detect_changes` before commit and confirm only continuation/retry, incident inbound gating, remediation review, and related frontend flows are affected.
16. Run the final Trellis spec/quality check; update only the owning remediation/incident/frontend specs if the new contract is not already recorded, then commit only task-owned code and planning changes according to the existing worktree policy.

## Validation Commands

Backend from `backend/`:

```bash
gofmt -w <changed Go files>
go test ./internal/modules/remediation/...
go test ./internal/modules/incidents/...
go test ./internal/modules/hooks/...
go test ./internal/modules/remediation/adapter/http ./internal/modules/remediation/adapter/postgres
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/...
make generate-check
git diff --check
```

Frontend from `frontend/`:

```bash
npm run lint
npm run typecheck
npm run test
npm run build
npm run test:e2e
```

## Review Gates

- Before code edits: review this PRD/design and run symbol impact analysis for every changed function/method/class.
- Before migration acceptance: verify `CreateNextAttempt` is serialized by a series lock and does not reassign evidence or mutate prior attempts.
- Before frontend acceptance: verify server capabilities, generation/version checks, and mutation request body/path in route-mocked E2E.
- Before commit: run GitNexus `detect_changes`, full quality commands, and inspect `git status` so existing unrelated uncommitted work is not staged.

## Rollback Points

- Before coordinator changes: the additive migration and generated code can be reverted as a unit; root attempt behavior remains unchanged.
- Before enabling the webhook gate: leave the repeated-open continuation call disabled while retaining manual retry API and review history.
- If the UI/API path is rolled back: committed attempt history remains readable, and old clients can continue to read the original review subset.
- Never delete predecessor attempts or collapse a linked child into its parent during rollback.
