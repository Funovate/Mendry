# Implementation Plan: Remediation Harness Walking Skeleton

Ordered, independently verifiable steps. Each step lists its change, its
validation, and its review gate. Build order is inside-out: freeze contracts and
persistence first, then the in-process coordinator against fakes, then swap in
real adapters for the checkpoint. No workspace mutation, sandbox, or Git write
is implemented in any step.

Validation baseline for every code step (run from `backend/`):

```bash
go build ./...
go vet ./...
go test ./...            # targeted package first, then full suite before a gate
sqlc generate            # after any query/schema change; diff must be committed
```

Migrations are additive only, applied under the existing advisory-lock migrate
path; no destructive or in-place column rewrites.

---

## Step 1 — Freeze seam contracts (R1)

- Create `internal/modules/remediation/domain` with the value types
  (`RepoRef`, `EvidenceScope`, `RunState`, fixability enum, `Decision`,
  `RepairPlanCandidate`, `ToolInvocation`, `Effect`, budget counters) and the
  four port interfaces: `RepositoryReadPort`, `EvidenceLogPort`,
  `LLMProviderPort`, `RunStore`. No credential, raw client, or provider SDK type
  appears in any signature.
- Define the agent protocol envelope decoder in `application`: a versioned,
  strict JSON schema accepting exactly one of `request_tool | diagnosis |
  plan_candidates | stop`, rejecting unknown fields, unknown evidence IDs,
  invalid paths, unavailable tools, and out-of-phase kinds. Declare (but leave
  unreachable) `patch_complete` / `validation_assessment` with a `schemaVersion`.
- **Validation:** `go build ./...`, unit tests for the envelope decoder covering
  each accept/reject case.
- **Gate:** interfaces reviewed against umbrella `design.md`; a compile-time
  assertion or doc note confirms adding sandbox/publisher ports later needs no
  signature change here.

## Step 2 — Incident coupling + additive migration (R2)

- Add `LifecycleGeneration int64` and `DeployedCommit string` to
  `incidents/domain/incident.go` and its sqlc-backed persistence via an additive
  migration (new columns, defaulted; existing rows valid).
- Define increment rule in `incidents/application`: `LifecycleGeneration` starts
  at 1 and increments only on recovery→reopen; `DeployedCommit` captured at
  trigger time from `projects` `Repository.DeployedCommit` at its recorded
  `Version`.
- **Validation:** `sqlc generate`; migration up/down applies against a clean DB;
  existing incident tests stay green; new tests for the increment rule.
- **Gate:** confirm existing incident/project read+write paths are unchanged
  (acceptance criterion: existing data remains readable and writable).

## Step 3 — Remediation persistence + additive migrations (R8, R9 storage)

- Add migrations + sqlc queries for `remediation_series` (unique
  `(incident_id, lifecycle_generation, deployed_commit)`), `remediation_run`,
  `remediation_decision`, `remediation_plan`, `remediation_artifact`, and
  `remediation_tool_invocation`. Every mutable table carries `version bigint`.
- Implement the `RunStore` adapter (`adapter/postgres`) mapping rows to
  domain types, with `CreateSeriesAndRun`, `AppendDecision`,
  `RecordToolInvocation`, `Transition(from,to,Effect)`, and `Get` inside single
  transactions using optimistic versioning.
- Artifact rows store a reference + content hash (no inline diff in audit JSON);
  tool-invocation rows store no secret-bearing parameters.
- **Validation:** repository integration tests (series-key uniqueness conflict,
  optimistic-version conflict, transactional transition) against a test DB.
- **Gate:** DB reviewer confirms additive-only + advisory-lock conformance.

## Step 4 — In-process coordinator + tool gateway against fakes (R1, R7, state machine)

- Implement `RemediationCoordinator` in `application` as the only advancer of run
  state, driving the subset state machine (`queued → preparing_context →
  diagnosing → {collecting_more_context loop | completed_non_code |
  blocked_manual_review | planning → diagnosis_ready_for_review}`; any active →
  `failed | budget_exhausted`). Reserved states (`patching`, `validating`,
  `publishing`, `awaiting_human_review`) exist but are unreachable.
- Implement `ContextAssembler`, `AgentEngine` (schemas in, validated envelopes
  out — no adapter clients), and read-only `ToolGateway`. The gateway advertises
  only `repository.*` and `evidence.*` read tools; any mutation/execution tool
  request is rejected `tool_unavailable`. Gateway validates phase, path scope
  (no traversal), and size/time budget before any adapter call.
- Provide fakes for all four ports, including a scripted fake model that
  exercises each fixability class and the bounded collect-more-context loop.
- **Validation:** state-machine tests routing every fixability class to the
  correct terminal/continuation state; the **credential-isolation contract
  test** proving coordinator/AgentEngine receive no credentials or raw clients
  and that invalid / out-of-phase / unavailable / oversized tool requests are
  rejected before adapter execution.
- **Gate:** this is the primary R1/R7 acceptance gate — full green before real
  adapters are wired.

## Step 5 — Synchronous trigger wiring (R2)

- Wire the qualifying incident transition (default new/reopened `P1/P2`;
  `admin/operator` manual `Info` start) to synchronously invoke the coordinator
  and create one series for the unique key plus its first run attempt inside the
  same transaction as the status change.
- Repeated observations for the same fingerprint do not create a second series
  for the same key. Isolate the call site as the future outbox-emit seam.
- **Validation:** trigger tests for the `P2` threshold, manual `Info` start,
  reopen-driven lifecycle generation, deployed-commit change, fingerprint
  coalescing, and unique series key — all in-process, no duplicate root runs.
- **Gate:** confirm the seam is a single call site swappable for a transactional
  outbox insert without changing series/run identity rules.

## Step 6 — Budgets, privacy, audit enforcement (R9)

- Enforce per-run limits (elapsed time, model calls/tokens/cost where available,
  tool calls, evidence bytes, repository bytes); exhaustion → terminal
  `budget_exhausted`. Persist structured conclusions + provider/model/usage
  metadata; assert no raw provider requests/responses, plaintext credentials,
  repository archives, or duplicate raw logs are persisted; stored excerpts are
  redacted and retention-bounded.
- **Validation:** budget tests terminating a run at each limit; an audit test
  asserting persisted rows contain no raw prompts/responses/credentials.
- **Gate:** privacy invariants reviewed against `logging-guidelines.md`.

## Step 7 — Console REST + in-console notification (R8)

- Add protected REST handlers (`adapter/http`) exposing the review chain: run
  status, diagnosis, evidence refs, suggested diff, risk classification —
  standard envelope, `DecodeJSON`, stable codes, RBAC-scoped
  (`admin/operator/viewer`). No secret values, raw provider payloads, or
  unrestricted logs exposed.
- Emit durable in-console notifications for terminal outcomes
  `non_code_diagnosed`, `diagnosis_ready_for_review`, `manual_review_required`,
  `insufficient_evidence`.
- **Validation:** handler tests for RBAC scoping and secret-free responses;
  notification-emission tests per terminal outcome.
- **Gate:** API review confirms envelope + no-secret-exposure compliance.

## Step 8 — Real adapters for the checkpoint (R3, R4, R5, R6)

> Pilot decisions locked (PRD Resolved Decisions): log source = **SSH log
> path**, provider/model = **OpenAI `gpt-5.6`** via the encrypted credential
> store. Step 9 uses the complete reviewable pilot set and records its limits in
> `research/quality-readout.md`.

- Extend the Git read adapter (reusing the `projects/adapter/git` credential-
  injection pattern) with bounded `list_tree` / `read_file` / `search` /
  `history` at the exact `deployed_commit`; size/count bounds; binary refused
  with a stable reason. Credentials stay inside the adapter.
- Implement one real `EvidenceLogPort` adapter over an **SSH log path** (native
  Go, bounded, redacted, incremental), reusing the SSH temp-key/`IdentitiesOnly`/
  `BatchMode` pattern from `lsremote.go`; SSH credential stays inside the adapter.
- Implement one real `LLMProviderPort` adapter for **OpenAI `gpt-5.6`**; the API
  secret uses the existing encrypted store (`FIXTHE_ENCRYPTION_KEY`); SDK type and
  secret never cross the port.
- For `code_fixable`, produce candidate plans + recommended plan with rationale
  + a human-readable suggested unified diff (advisory only, no write). Compute
  and display risk classification (ordinary / high-risk / denied-control-plane);
  no publication gate because nothing is published.
- **Validation:** repository-read tests (reads at exact deployed commit, bounded,
  credential-free to coordinator/model); a real-data smoke run wired end-to-end.
- **Gate:** adapters pass their contract tests against the frozen ports.

## Step 9 — Real-incident go/no-go checkpoint (acceptance)

- Run the slice against the real pilot log source, real repository, and real
  model on a small set of real incidents. Produce a **written quality read-out**
  (diagnosis validity, evidence citation quality, suggested-diff usefulness) that
  informs go/no-go for the sandbox/publication slices. This is a human-observed
  run, not a CI gate.
- Add or confirm the forward-compatibility note/test showing outbox/async,
  sandbox, and SCM-publisher slices can be added without changing frozen port
  signatures or run-state names.
- **Gate:** all PRD acceptance criteria checkboxes satisfied; read-out reviewed.

**Completed 2026-09-04.** The quality read-out covers INC-2267, INC-2268,
INC-2270, and live INC-2390. It records a conditional go for the
diagnosis/advisory architecture and a no-go for unattended production repair or
broad rollout. The retained live run has 3/3 resolvable citations, two plans,
and one human-readable suggested diff. See
[`research/quality-readout.md`](research/quality-readout.md).

---

## Review Gates Summary

1. After Step 1 — frozen contracts reviewed, forward-compatibility asserted.
2. After Step 4 — credential-isolation + state-machine contract tests green
   (primary correctness gate).
3. After Step 7 — no-secret-exposure + RBAC API review.
4. After Step 9 — real-data quality read-out reviewed; go/no-go recorded.

## Rollback Points

- All schema changes are additive; a rollback disables per-project remediation
  enablement and, if needed, applies the migration `down` for the new tables and
  the two incident columns without touching existing incident/project data.
- The synchronous trigger (Step 5) is a single guarded call site; disabling the
  per-project enablement flag stops all new runs while leaving incident
  ingestion and status transitions intact.
- Steps 1–7 use fakes only and produce no external effects; reverting them
  removes the `remediation` module and the two incident columns with no residual
  side effects.
