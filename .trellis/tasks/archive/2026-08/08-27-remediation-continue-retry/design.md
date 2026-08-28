# Technical Design: Remediation Continue And Retry Flow

## Design Principle

A terminal remediation run is immutable history. “Continue” creates a new attempt in the same `(incident_id, lifecycle_generation, deployed_commit)` series and gives the agent a bounded, explicitly labeled brief of the previous attempt. Persisted operational evidence is a separate trusted input: its fields and values are preserved for model analysis rather than passed through page/review/log sanitizers. The flow never mutates the old run, recreates a root, replays raw provider conversation, or treats model conclusions as facts.

The coordinator remains the only state advancer. The webhook path remains an accepted-background boundary. The page uses the same continuation primitive, with project authorization and optimistic concurrency at the application boundary.

## Current Flow And Target Flow

```text
Current:
POST /hooks/{token}
  -> validate token/provider/body
  -> 202
  -> observation/evidence/incident
  -> new incident only: root run
  -> existing open incident: occurrence only

Target:
POST /hooks/{token}
  -> validate token/provider/body
  -> 202
  -> observation/evidence/incident
  -> automatic gate
       -> no series: create root attempt 1
       -> queued/active or usable terminal: no-op
       -> retryable failed + new context + below auto cap:
            atomically create linked attempt N+1 and drive it

Incident page
  -> GET current series review + bounded attempt history
  -> if no series and write capability: POST /remediation/start
  -> if eligible terminal latest and write capability:
       POST /remediation/retry with generation/runId/version
       -> atomically create linked attempt N+1
       -> load the current persisted evidence snapshot unchanged
       -> drive analysis with repository-only tools and no evidence refresh
  -> invalidate review query and render the new attempt
```

The webhook gate runs only after the incident/evidence transaction has committed. A failure after `202` is reported by the existing background failure reporter and never changes the already returned HTTP response.

## Boundaries And Ports

The existing frozen `domain.RunStore` interface remains unchanged. Add a companion application port implemented by the PostgreSQL store and test fakes:

```go
type AttemptStore interface {
    GetLatestForIncident(context.Context, string, int64, string) (domain.RunAggregate, error)
    CreateNextAttempt(context.Context, domain.NextAttempt) (domain.Run, error)
}
```

`domain.NextAttempt` carries only opaque IDs, immutable series identity, current context version, expected predecessor version, continuation origin, and a bounded safe reason. It contains no credentials, URLs with userinfo, provider clients, prompts, or raw webhook data.

The coordinator adds an additive `Continue(context.Context, domain.NextAttempt)` method. `Trigger.Emit` keeps the existing incident trigger contract and becomes the automatic gate; `Trigger.Continue` is the shared manual/automatic next-attempt seam. Existing root `Start` remains idempotent for a series with no attempt and does not create attempt 2.

The remediation application service adds:

```go
func (*Service) ContinueRemediation(
    context.Context, authdomain.User, string, string,
    int64, string, int64,
) (domain.Run, error)
```

The arguments are project key, public incident identifier, current lifecycle generation, expected latest run ID, and expected latest run version. The service resolves project access and incident identity before requesting continuation. The store repeats ownership and predecessor checks inside its transaction.

## Durable Model

Add an additive migration after the current remediation schema:

- `remediation_run.continuation_of_run_id uuid NULL REFERENCES remediation_run(id) ON DELETE SET NULL` records the direct predecessor.
- `remediation_run.trigger_reason text NOT NULL DEFAULT ''` records `automatic`, `manual`, `automatic_continue`, or `manual_continue` as safe origin metadata.
- `remediation_run.continuation_reason text NOT NULL DEFAULT ''` stores a bounded operator/system reason, never an error body or payload.
- `remediation_run.context_version bigint NOT NULL DEFAULT 0` snapshots the incident/evidence context seen by the attempt.
- `remediation_run.terminal_reason text NOT NULL DEFAULT ''` stores a safe stable terminal classification.
- `remediation_run.retryable boolean NOT NULL DEFAULT false` is service-owned eligibility metadata for automatic continuation.

Add length and non-negative constraints for the new textual/version fields. Existing rows remain readable; old failed rows default to non-retryable and can still be manually continued when their state is in the manual allowlist.

Extend provider-neutral `domain.Run` and `domain.RunAggregate` with the corresponding safe metadata and bounded `AttemptSummaries`. The aggregate returned by `RunStore.Get` includes summaries for all runs in the series, while decisions/plans/artifacts/tool invocations remain attached to the requested run. The HTTP review response exposes only attempt IDs, numbers, states, origins, context versions, terminal classifications, retryability, versions, and timestamps.

The root `NewRun` gains `ContextVersion` and `TriggerReason`; it is populated from the incident version and existing automatic/manual path. The next-attempt input carries `ExpectedPreviousVersion` and `ContinuationOfRunID`. `domain.Effect` gains terminal reason and retryability metadata so terminal transitions persist safe eligibility without changing any frozen method signature.

## Atomic Next-Attempt Creation

`RunStore.CreateNextAttempt` performs one PostgreSQL transaction:

1. Parse and validate the predecessor UUID and immutable identity values.
2. Lock the current incident row with `FOR SHARE`, verify its version equals the requested context version, and retain that lock through commit. Taking the incident lock before the series lock matches lifecycle-write lock order and prevents a concurrent occurrence from advancing the context cursor after validation.
3. Lock the predecessor's series row with `FOR UPDATE`; this serializes all next-attempt creators for one series.
4. Load the predecessor and all series runs. Confirm it is the latest run, its version equals `ExpectedPreviousVersion`, its series/incident/generation/commit match the request, and no active attempt exists.
5. Apply the origin-specific eligibility rule:
   - `automatic_continue`: latest state is `failed`, latest `retryable` is true, request context version is greater than the latest context version, and the series has fewer than `maxAutomaticContinuations` (3) prior automatic continuations;
   - `manual_continue`: latest state is `failed`, `budget_exhausted`, or `blocked_manual_review`.
6. Insert attempt `latest.AttemptNumber + 1` with the predecessor link, origin, reason, and current context version in `queued` state.
7. Commit and return the new attempt.

The transaction returns typed safe categories for stale predecessor/active attempt, unsupported state, automatic gate rejection, and ceiling exhaustion. The automatic caller treats expected gate races/rejections as no-op after the webhook has already been accepted. The manual HTTP caller maps them to stable `409` errors. A duplicate click with the same predecessor cannot insert twice; a concurrent caller sees a newer latest attempt or an active queued attempt.

`maxAutomaticContinuations` is a code-owned bound for this slice. Counting rows while holding the series lock avoids a second automatic attempt racing past the limit. Manual continuations remain subject to active-attempt and optimistic-version checks and the existing per-attempt budget; they do not bypass tool/evidence policy.

## Automatic Gate

`incidents.application.ingestInbound` keeps current behavior for a newly created incident and for non-open incidents. After an open occurrence and its optional evidence writer succeed, the normal webhook path invokes the existing automatic remediation seam with the updated incident context version. The compatibility `IngestInbound` path continues to support root creation; the enriched webhook path is the one that enables continuation after evidence persistence.

`Trigger.Emit` resolves the current latest run through `AttemptStore`:

- no series/run: call root `Start`;
- queued or any active state: no-op;
- `diagnosis_ready_for_review` or `completed_non_code`: no-op;
- failed but non-retryable, blocked/manual-review, budget exhaustion, or unchanged context: no-op;
- retryable failed with a newer context version and fewer than three automatic continuations: call `Continue` with `automatic_continue`;
- expected stale/active/ceiling gate responses: no-op and preserve safe diagnostic observability;
- storage/control-plane failures: return an error to the detached background reporter.

Root creation performed transactionally with incident creation remains idempotent: a subsequent `Emit` sees the queued root and does not create a second attempt.

The automatic gate does not infer retryability from free-form error text. The coordinator writes retryable metadata only for typed transient provider/runtime failures and other explicitly classified failures. Budget exhaustion and human/policy decisions are not automatically replayed.

## Coordinator Continuation

`RemediationCoordinator.Continue` loads the predecessor aggregate before creating the next attempt, validates that the requested identity is consistent, and asks `AttemptStore` to create the queued child. It then follows the same queued-run driver and observer lifecycle as `Start`. For the protected manual page path, `Trigger.QueueContinuation` reuses the same preparation and atomic child creation but returns immediately after the child is durable; it drives that child with `context.WithoutCancel` in the background and reports later failures through the existing remediation failure reporter. Automatic webhook continuation keeps the synchronous `Continue` path because the webhook has already detached its accepted background work.

Refactor the common queued path into a private runner that accepts an optional continuation brief:

```text
create/claim queued run
  -> resolve credential-free repository/evidence refs
  -> load current persisted observation/evidence payloads
  -> prepend bounded continuation brief
  -> choose the phase from the latest valid planning checkpoint in the continuation chain
       same series/context and latest durable decision code_fixable: planning
       changed/unknown context or no checkpoint: diagnosing
  -> for manual continuation, build a repository-only catalog without source discovery
  -> existing diagnosing/planning state machine
```

The continuation brief is built from safe structured projections only:

- predecessor attempt number/state/origin/terminal reason/retryability;
- prior diagnosis classifications, bounded reasoning, confidence, evidence IDs,
  missing/contradictory summaries, and next action;
- prior plan IDs, affected relative paths, risks, rollback summaries, and
  artifact references/diff availability;
- tool names, phases, safe outcomes/error codes, and byte counts, without result
  payloads or secret-bearing parameters;
- explicit continuation reason and instruction to verify, overturn, or extend
  the prior hypothesis;
- current incident context version and a reminder that current bootstrap
  evidence is authoritative for the new attempt.

The continuation brief is JSON-encoded and capped before it enters model context. It is a typed projection of prior attempt metadata, not the carrier for operational evidence. Persisted operational evidence is loaded through `BootstrapEvidenceLoader` and rendered with its original fields and values; size/count caps remain context-budget controls, not redaction. Provider prompts/responses, adapter-injected credentials, SDK state, and control-plane configuration are not evidence and never enter the brief, page, logs, or model context.

Manual continuation uses an analysis-only catalog built without source-policy resolution or dynamic discovery. The catalog exposes repository list/read/search/history only; Tencent detail, Docker logs, SSH inspect, evidence search/context, source search/refresh, and dynamic MCP tools are absent. Automatic webhook continuation keeps the normal source catalog because its gate is driven by a newer persisted context version.

Each attempt receives a fresh `runBudget`. Before creating a manual child, the service snapshots the current incident version and the store compares that cursor with the incident row inside the next-attempt transaction; a concurrent occurrence therefore returns a stale conflict instead of persisting context version `0` or an obsolete snapshot. For phase resume, the persistence companion finds the latest attempt at or before the expected predecessor whose latest durable decision is `code_fixable`, but only inside the exact same non-changed context version. That service-owned evidence-gate checkpoint resumes directly in planning even when later same-context attempts persisted weaker diagnoses. A newer or unknown context version never inherits the checkpoint and restarts diagnosis against the current persisted evidence. Manual continuations still avoid external evidence collection; automatic continuations may restart diagnosis against newly persisted webhook evidence. The series key and exact deployed commit remain unchanged. Reopened incidents or changed deployed commits enter the existing root path and do not use a continuation brief from the old series.

A valid `insufficient_evidence` diagnosis that does not request any collection tools terminalizes immediately as `blocked_manual_review` with terminal reason `insufficient_evidence`. Only explicit non-empty `collectMoreContext.toolCalls` consume the bounded collection loop; analysis-only manual continuation never re-prompts an identical diagnosis through an empty collecting state.

## Failure Classification And Terminal Metadata

Add a small provider-neutral failure classification helper at the coordinator boundary:

- typed transient provider/runtime error or model output exhaustion -> safe code plus `retryable=true`;
- caller/run deadline -> `budget_exhausted` with elapsed reason and `retryable=false`;
- invalid envelope, policy/authorization/configuration failure, persistence failure, or unknown control-plane error -> safe code plus `retryable=false`;
- explicit `blocked_manual_review` and evidence insufficiency -> safe reason plus `retryable=false`.

OpenAI's existing typed output exhaustion and transient transport/status paths remain the source of provider retryability; no response body is persisted. The state transition continues to use the existing `Effect` parameter and stores only safe terminal metadata. Existing logs may include the safe error class/retryability fields already used by remediation observers.

## REST Contract

Keep the existing protected `POST .../remediation/start` for an initial manual start when no series exists. Add:

```text
POST /api/v1/projects/{projectKey}/incidents/{id}/remediation/retry
```

Request body is strict JSON:

```json
{"generation":1,"runId":"<latest-run-id>","version":2}
```

Response uses the shared success envelope and returns `runId`, `seriesId`, `status`, `generation`, `attemptNumber`, and `version`.

The existing GET review response gains:

- current `attemptNumber`, `version`, origin, terminal reason, and retryability;
- `continuationAvailable` computed server-side from project write capability and
  the manual state allowlist;
- bounded `attempts` history.

Authorization remains project-scoped: admin/operator capability is required for both start and retry; viewers can inspect review/history but never see an enabled continuation action. Generation and expected predecessor checks happen in the service and again in the transaction. Error mappings use stable categories for invalid request, forbidden, not found, stale conflict, active attempt, and unsupported continuation; server diagnostics remain outside client messages.

## Frontend Behavior

`RemediationPanel` continues to own the local review query and mutation states. It uses `useCurrentProject` for server-provided `capabilities.writeIncidents`, the existing remediation query key, and mutation invalidation rather than copying server state into component state.

- No review (`remediation_not_found`) + write capability: show `Start remediation`.
- Review with `continuationAvailable`: show `Continue analysis` with a bounded pending state. Submit the current generation, run ID, and version from the response.
- Active/usable terminal/latest conflict: disable/hide the action based on the server response and render the returned safe message.
- The retry response is returned after the new child is durably queued, not after model or connector execution completes. The page invalidates the remediation query after that acknowledgement and renders the queued/active attempt while background progress continues.
- Viewer or read-only project: retain review display and omit mutation controls.
- Display attempt number/origin/history and safe terminal reason so operators can tell root, automatic continuation, and manual continuation apart.

The API decoder validates the expanded review/action DTOs. Playwright route mocks record the nested project path, method, and request body; tests cover successful manual continuation, viewer denial/hidden control, conflict handling, and no-secret rendering.

## Compatibility, Rollout, And Rollback

- Schema changes are additive and old rows default to conservative manual-only metadata.
- The root incident transaction and public webhook `202` behavior remain intact.
- The feature can be disabled by leaving the automatic gate unconfigured or setting its code-owned ceiling to zero; existing incident/evidence/review reads remain available.
- Rolling back application code leaves additive columns readable by the previous version. Do not delete attempt history or collapse attempt N+1 into attempt N.
- Queue/outbox/worker restart resume remains deferred. The current in-process background driver may lose accepted work on process termination, but all committed attempts and prior review history remain inspectable and manually continuable.
