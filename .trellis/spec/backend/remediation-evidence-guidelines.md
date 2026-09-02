# Remediation Evidence Guidelines

## Scenario: Trusted Evidence Gate And Docker Runtime Sources

### 1. Scope / Trigger

Use this contract when changing normalized webhook evidence, remediation
bootstrap context, source coverage, the service-owned diagnosis gate,
exhaustion proof validation, submitted-diagnosis/tool-invocation audit, or the
read-only Docker path behind an SSH source. The application gate decides
whether a model diagnosis may enter planning; model confidence and
model-declared exhaustion alone are never authority.

### 2. Signatures

```go
type EvidenceResolver interface {
    ResolveEvidence(context.Context, string, []domain.EvidenceCitation) (domain.EvidenceResolution, error)
}

func (*application.EvidenceGate).Evaluate(context.Context, string, *application.DiagnosisOutput) (domain.EvidenceGateDecision, []application.CitationClassificationMismatch, error)
func (*application.EvidenceGate).Apply(context.Context, string, *application.DiagnosisOutput) (*application.DiagnosisOutput, domain.EvidenceGateDecision, []application.CitationClassificationMismatch, error)
func domain.EvaluateEvidenceGate(domain.EvidenceGateInput) domain.EvidenceGateDecision

func application.ValidateExhaustionProposal(domain.ExhaustionProposalV1, application.ExhaustionValidationContext) (bool, domain.RecoveryChallengeV1, []string)
func (*postgres.RunStore).RecordToolInvocation(context.Context, string, domain.ToolInvocation) error
func (*postgres.RunStore).AppendSubmittedDiagnosis(context.Context, string, domain.SubmittedDiagnosis) error
func (*postgres.RunStore).LatestDecisionID(context.Context, string) (string, error)

func (*postgres.RunStore).ResolveEvidence(context.Context, string, []domain.EvidenceCitation) (domain.EvidenceResolution, error)
func (*postgres.RunStore).PersistEvidenceAssessment(context.Context, domain.EvidenceAssessment) error
```

Evidence append idempotency is backed by the scoped database key:

```sql
CREATE UNIQUE INDEX remediation_evidence_baseline_dedup_idx
    ON remediation_evidence (
        project_id, incident_id, run_id, baseline_lifecycle_generation,
        baseline_deployed_commit, deduplication_key
    ) NULLS NOT DISTINCT;
```

The Docker runtime port accepts only the saved exact container name and a
bounded time/line request. It resolves the current container ID internally;
the ID is runtime evidence, never durable configuration or model input.

### 3. Contracts

- Direct evidence citations are resolved against the current run and its
  triggering observation under project/incident ownership. Unknown, unavailable,
  cross-run, or cross-project IDs remain missing evidence.
- Evidence deduplication is scoped to project, incident, nullable run, lifecycle
  generation, and deployed commit. Run-owned evidence takes its baseline from
  the owning series; pre-run evidence takes it from the incident. A retry in
  one run may update that run's row, but a different incident or attempt must
  create its own evidence ID and must never reassign `run_id` on an older
  incident's row. The scope migration clears any historical run binding whose
  series incident differs from the evidence incident while preserving payload
  and incident ownership.
- Only a webhook explicitly configured with provider `tencent_cls` may resolve a
  validated allowlisted `DetailUrl`. The trusted adapter follows the bounded
  no-login detail flow and persists the structured operational result, including
  `ResultsSnapshot.RawResults`. Persisted operational evidence is rendered to
  trusted model context with its original fields and values; a stored
  `DetailUrl` is therefore preserved rather than deleted or replaced. Connector
  credentials and adapter-injected authorization remain outside evidence.
- A successful Tencent `provider_detail` record is preferred in the first model
  turn only when it is `direct_fault`, available, and carries the exact trusted
  adapter provenance for validated detail resolution. The database candidate
  query and application renderer both enforce that predicate before context
  truncation. Generic, unverified, contextual, contradictory, or unavailable
  lookalikes retain ordinary ordering. Preference tells the model to analyze and
  cite the record first when causal; it never bypasses citation resolution,
  correlation checks, confidence caps, or the evidence gate.
- Tencent detail acquisition is a bounded first evidence gate, not an infinite
  coordinator lock. A non-retryable detail failure (invalid response, rejected
  redirect, oversized response, or persistence failure) is recorded as a safe
  tool observation and immediately reopens the configured Docker/Git read tools.
  A retryable timeout/transport failure may leave the detail tool available for
  one additional model-selected attempt, while the fallback read tools remain
  visible. Diagnosis must continue to account for the failed tool and may stop
  as insufficient evidence; it must never spend the run budget repeating the
  same non-retryable detail request.
- When trusted detail contains a source path, line number, stack, or fault
  message, treat it as a high-value locator. The harness advertises all allowed
  repository/evidence/SSH/Docker capabilities and states causal completion
  criteria; it must not prescribe a Docker-first or repository-first decision
  tree. The agent may inspect deployed code first and collect runtime evidence
  later when causal closure still requires it.
- Persisted resolver records, source coverage, correlation, and contradictions
  are authoritative. If the resolver has no persisted time assessment, the gate
  preserves the diagnosis `TimeAssessment` because the diagnosis was produced
  from the raw bootstrap time fields; an empty resolver field must not erase it.
- `0.00-0.39` is low, `0.40-0.69` is medium, and `0.70-1.00` is high. Missing
  direct fault evidence caps at `0.39`; a service-resolved material
  contradiction caps at `0.69`. Time, host identity, request correlation, and
  primary-source coverage remain structured audit facts, but an unresolved
  value does not independently cap every diagnosis.
- Planning requires effective confidence of at least `0.70`, a trusted direct
  citation, no material contradiction, and causal closure to the original
  symptom. The gate records an `insufficient_evidence` verdict when these facts
  fail but never mutates the model's fixability/confidence. In `resilient_v1`,
  the coordinator persists the original submission and returns a structured
  fact-check challenge to the same loop; repeated no-progress requests a
  service-validated exhaustion proof. Legacy routing remains rollout-compatible.
- Test authorization is an audit dimension, not causal evidence. An unknown or
  unmatched structured test policy may remain in `missingEvidence` and the
  operator recommendation, but it must not by itself turn an otherwise closed
  runtime/request/deployed-code explanation into `insufficient_evidence`.
- Before `resilient_v1` enters manual review for exhaustion, every advertised
  capability must be covered exactly once as attempted or untried. An attempted
  path must cite an `outcome_ref` persisted on a real tool-invocation row whose
  `tool_name` maps to that capability; evidence or recovery challenge refs
  cannot impersonate an action. The same row persists bounded evidence IDs,
  coarse outcome, and sanitized error code so continuation can reconstruct the
  binding. `irrelevant` requires owned evidence; `unavailable`/`unsafe` require
  matching service policy; `budget_prohibited` checks model calls, model cost,
  tool calls, elapsed time, and the capability-specific byte reserve.
- Submitted-diagnosis and resilient tool-invocation audit are hard consistency
  boundaries. A missing companion store, invalid correction metadata,
  latest-decision mismatch, sequence/insert failure, or tool-audit failure must
  transition to `persistence_failure`; it must never be swallowed as
  best-effort. PostgreSQL allocates submitted and tool sequences while holding
  the owning series row lock. A submitted `decision_id` must belong to the same
  run.
- A terminal `insufficient_evidence` diagnosis with
  `causalClosure.explainsOriginalSymptom=true` is structurally inconsistent.
  When it has no bounded `collectMoreContext` request, the coordinator records
  the model usage and issues exactly one fixed, sanitized reassessment
  observation. The model must either classify the actual fixability, set causal
  closure false and request material collectible evidence, or repeat the result
  and enter `blocked_manual_review`; the coordinator never loops indefinitely.
- SSH Docker discovery is bounded inventory only. A Docker deployment advertises
  both `ssh.inspect` and typed `docker.logs`; generic inspect covers host/network/
  process evidence, typed logs cover incident-window collection. Each runtime
  request performs one log-content scan after exact-name resolution; it must not
  re-read the same large window solely to calculate `window_lines` or filtered
  counts. The fallback uses fixed `docker logs --since --until --tail`; when the
  current container uses `json-file`, the adapter may inspect `.LogPath` and run
  one fixed read-only scan over that file only when the absolute path's parent and
  basename both match the resolved container ID. Any invalid path, driver,
  identity, decode, or read result falls back without exposing the path to the
  model. The
  optional `pattern` (whitelist `[a-zA-Z0-9 ._\-|()*?]`, max 256 bytes),
  `context_before`/`context_after` (0-100), and `tail` (1-2000, applied after
  the filter) extend the read without new commands. Results report
  `window_lines`/`returned_lines`/`filtered`/`truncated` plus
  `coverage_limited`/`refinement_required`/`coverage_reason` so the model can
  detect tail-only coverage and narrow the window. `window_lines=-1` means the
  connector deliberately skipped an extra full-log count; it is not zero.
  Adapter byte truncation, a full `tail`, or canonical 64KiB projection
  truncation sets refinement required. A diagnosis or stop while the latest
  Docker result remains incomplete receives one fixed budgeted refinement
  observation; ignoring the same pending result then blocks as
  `insufficient_evidence`. A newer complete Docker result clears the pending
  state. `exec`, attach, copy,
  lifecycle, mutation, arbitrary flags, and all-container sweeps are
  unavailable.
- Successful `ssh.inspect`/`docker.logs` results become durable run-owned
  `remediation_evidence` before the success observation is emitted. Both share
  one canonical secret-redacted projection (provider `ssh`/`docker`, kind
  `runtime`, primary `true`, operational correlation `true`; docker adds
  temporal correlation and `direct_fault` classification when the bounded
  window returns log lines, otherwise `correlated_supporting`). The persisted
  payload and model-visible payload are byte-identical and bounded to 64KiB;
  the model-visible observation exposes the persisted evidence ID, and a
  diagnosis that uses the result must cite it. Projection or persistence
  failure surfaces as a stable non-retryable `runtime_evidence_persistence`
  tool failure and the raw output is not presented to the model.
- Runtime evidence stays owned by the attempt that collected it. An eligible
  continuation attempt may read and cite evidence from earlier attempts in the
  same immutable series: PostgreSQL resolution requires the same project and
  incident, the evidence run belongs to the target run's series, and the
  evidence attempt number is not later than the target attempt. Pre-run
  observation evidence is readable only when its recorded lifecycle generation
  and deployed commit exactly match the target series; unbound legacy rows fail
  closed. A bounded continuation
  evidence loader renders prior sanitized runtime records with their original
  evidence IDs into diagnosis continuation context; planning continuations
  retain the compact checkpoint path. No evidence row is copied or reassigned
  to the child run.
- The two subprocess output writers share one synchronized capture. Combined
  stdout/stderr byte limits and the truncation marker remain correct when the
  streams write concurrently.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Unknown or cross-run evidence ID | Mark missing; do not trust model classification |
| Same provider record is collected by another incident/run | Create a separately owned evidence row; never move the old row's `run_id` |
| Continuation cites earlier-attempt runtime evidence in the same series | Resolve with unchanged evidence ID; no row copied or reassigned to the child |
| Continuation cites cross-series, cross-incident, or future-attempt evidence | Remain missing/unresolvable; never widen the query to arbitrary incident/run IDs |
| Historical evidence `incident_id` differs from its run series incident | Migration clears only the invalid `run_id`; retain the original incident and payload |
| Validated Tencent CLS direct detail | Render before normalized/context evidence and require normal citation/gate checks |
| Non-retryable Tencent detail failure | Persist a safe failure observation, expose configured Docker/Git fallback tools, and do not keep the detail gate closed |
| Retryable Tencent detail timeout/transport failure | Count the failed tool, keep at most one detail retry, and expose bounded fallback tools concurrently |
| Trusted detail contains path/line/fault message | Advertise bounded repository/evidence/runtime capabilities without a fixed order; require evidence-backed causal closure before planning |
| Pre-run evidence baseline differs from the target series, or is NULL | Exclude it from continuation indexes and return not-found from `evidence.read` |
| Cursor token is modified or minted from evidence ID/content hash/source code | Reject it; offset and expiry exist only in server-side random cursor state |
| Generic or unverified Tencent detail lookalike | Do not grant preferred status or URL-fetch authority |
| Missing direct fault record | Cap `0.39`; return a fact-check rejection without rewriting the submission |
| Unresolved time/host/request/source fact with direct evidence and causal closure | Preserve it for audit; do not impose a universal cap unless service resolution marks a material contradiction |
| Material contradiction | Cap `0.69`, block planning, and challenge the resilient loop |
| Attempted exhaustion path cites challenge/evidence/another capability action | Reject with `exhaustion_proof_incomplete`; continue automation |
| Untried `unavailable`/`unsafe` has no matching service policy | Reject the proof; model text cannot create policy authority |
| Untried `budget_prohibited` omits an available hard-budget dimension | Reject the proof; validate the complete projection |
| Submitted diagnosis or resilient tool audit cannot persist consistently | Fail the run with `persistence_failure`; do not continue to a business terminal |
| Resolver lacks time assessment | Retain structured diagnosis time assessment; resolver contradictions still win |
| Code-only or unsupported fixability claim | Keep hypothesis non-actionable; never enter planning |
| Causal closure true, only test authorization unknown | Preserve the audit gap and reassess actual fixability; do not block solely on authorization |
| `insufficient_evidence` with causal closure true and no collection request | Issue one budgeted causal-closure reassessment; a repeated inconsistent result may block |
| Missing exact Docker name after recreation | Source unavailable; do not choose a similarly named container |
| Docker inventory/log command exceeds bound or requests mutation | Reject before SSH/remote execution |
| Docker result hits byte/tail/canonical projection limit | Persist bounded evidence, set refinement required, and require a narrower query before terminal diagnosis |
| Docker exact container uses a validated `json-file` path | Read the incident/pattern window with one fixed scan; never run `docker logs` or count scans for that request |
| Docker log path/driver/identity is invalid | Fall back to one bounded `docker logs` scan; never read an arbitrary host path |
| Concurrent stdout/stderr capture | Serialize state, buffers, and truncation accounting; no race or byte overrun |

### 5. Good/Base/Bad Cases

- Good: a direct persisted fault plus causal closure permits planning even when
  a non-material host-correlation field is unresolved; the unresolved field
  remains visible in the assessment.
- Good: an exhaustion attempted path cites the exact durable action ref returned
  by a repository tool observation, and continuation rehydrates the same ref,
  tool identity, outcome, and evidence IDs.
- Good: each run collecting the same provider record receives a distinct,
  incident-consistent evidence ID; retries inside that run return the same row.
- Good: the resolver verifies a direct Tencent/Docker record and source
  coverage, while the diagnosis supplies a high-certainty epoch comparison;
  the gate merges both and permits planning only after causal closure.
- Good: a configured Tencent CLS webhook resolves its validated detail record;
  the model sees bounded persisted operational evidence first with an evidence
  reference, preserving fields such as `DetailUrl`, token-shaped strings, and
  password-shaped values exactly as stored. Adapter credentials are never added
  to that evidence payload.
- Base: a sparse or anchor-only callback starts bounded collection and is shown
  to operators, but remains non-actionable without a direct correlated fault.
- Good: runtime, request, and deployed-code evidence explains the symptom while
  test approval remains unknown; diagnosis proceeds with actual fixability and
  retains a separate audit recommendation.
- Bad: accept `evidenceId` or `classification` because the model supplied it,
  overwrite a valid time assessment with a nil resolver field, use a stale
  discovery container ID after recreation, or deduplicate evidence only by
  `(project_id, deduplication_key)` so a new run steals an old incident's row.
- Bad: treat missing approved-test identity as proof that an otherwise explained
  production request, panic, and HTTP failure lacks causal closure.
- Bad: accept `challenge:exhaustion:*` as proof that a repository capability
  executed, or continue after a submitted-diagnosis/tool-audit insert failed.

### 6. Tests Required

- Domain gate tests cover missing direct evidence, non-material unresolved
  time/correlation, material contradiction caps, causal closure, and
  high-confidence eligibility.
- Exhaustion validator tests reject recovery refs, cross-capability action refs,
  unowned evidence, unsupported policy reasons, unresolved recoveries, and
  incomplete hard-budget dimensions; one test accepts a fully service-backed
  proof.
- Coordinator tests prove failed fact checks preserve the submitted fixability,
  repeated malformed envelopes remain recoverable, action refs are persisted
  before exposure, and submitted/tool audit failures become
  `persistence_failure`.
- PostgreSQL integration tests prove concurrent monotonic submitted/tool
  sequences, same-run decision linkage, durable action/evidence rehydration,
  and omission of raw model/tool payloads.
- Coordinator regression tests prove a closed-causal `insufficient_evidence`
  result receives the fixed reassessment observation, can recover to
  `diagnosis_ready_for_review`, and blocks after one repeated inconsistent result.
- Prompt contract tests require the explicit separation between test-policy
  audit status and material root-cause evidence.
- Application gate tests prove citations are resolver-owned and prove a
  resolver without persisted time does not erase the diagnosis time assessment.
- Application bootstrap tests prove validated Tencent direct detail is ordered
  before alert/context records, survives the bounded candidate query, and is
  the only record receiving the preferred instruction. They also prove generic,
  missing-provenance, contextual, contradictory, and unavailable records are
  not preferred, while persisted `DetailUrl`, password-shaped, and token-shaped
  evidence values reach trusted model context unchanged.
- Migration/query tests prove the unique key includes nullable run plus the
  observation baseline, and pre-run writes capture generation/commit from the
  incident in the same statement; a database check verifies no evidence row remains bound to a run
  whose series owns another incident.
- Tencent adapter fixture tests prove the structured detail response retains
  `RawResults` and query/time content, writes safe provenance, and excludes URL
  capability material.
- SSH/Docker tests cover exact-name re-resolution, stopped/restarting inventory,
  bounded logs, one-scan command counts, validated `json-file` fast-path reads,
  fallback for invalid paths, missing/ambiguous names, prohibited operations,
  and concurrent stdout/stderr capture under `go test -race`.
- Coordinator tests prove an incomplete Docker result cannot support a terminal
  diagnosis, a narrower complete query clears the pending state, and one ignored
  refinement ends in `blocked_manual_review` without an infinite loop.
- Cross-layer checks run `make check`, sqlc generation/hash validation, frontend
  lint/typecheck/unit/build/E2E, and `git diff --check`.

### 7. Wrong vs Correct

#### Wrong

```go
resolution = resolverResult
// A nil resolver time silently discards the raw time interpretation from the
// triggering evidence and blocks every otherwise valid diagnosis.
```

#### Correct

```go
resolution = resolverResult
if resolution.Time == nil {
    resolution.Time = diagnosis.TimeAssessment
}
// Resolver-owned citations and coverage stay authoritative; only absent
// structured fields are filled from the bounded first-turn assessment.
```

Evidence rendering preserves the persisted payload while retaining context
bounds:

```go
// Wrong: retry-time sanitization changes the evidence the model must analyze.
removeDetailURL(value)
redacted := redactConversationValue(value)

// Correct: bound count/bytes, but preserve stored fields and values.
encoded, err := json.Marshal(value)
```

Evidence upserts follow the same ownership rule:

```sql
-- Wrong: a later run can steal an older incident's evidence row.
ON CONFLICT (project_id, deduplication_key)

-- Correct: pre-run idempotency and readability include the observation baseline.
CREATE UNIQUE INDEX ... ON remediation_evidence
  (project_id, incident_id, run_id, baseline_lifecycle_generation,
   baseline_deployed_commit, deduplication_key) NULLS NOT DISTINCT;
```

Harness ordering and paging authority stay server-owned:

```text
Wrong: if path/line exists, force Docker logs before repository inspection.
Correct: advertise allowed capabilities and causal completion criteria; let the
agent choose the next read while the service validates ownership and evidence.

Wrong: sign offset/expiry with evidenceId + contentHash, both visible to model.
Correct: mint a random cursor capability; persist only its digest and the
run/evidence/hash/offset/expiry binding on the server.
```

Diagnosis and exhaustion authority stay separate:

```go
// Wrong: mutate the agent claim or accept a recovery marker as an action.
diagnosis.Fixability = domain.FixabilityInsufficientEvidence
attempted.OutcomeRefs = []string{"challenge:exhaustion:stop"}

// Correct: preserve the submitted claim, challenge the same loop, and validate
// only the durable capability-bound ref returned by RecordToolInvocation.
conversation.AppendRecoveryChallenge(factCheckChallenge)
attempted.OutcomeRefs = []string{"action:repository:1"}
```

```go
// Wrong: unknown test approval terminates a causally closed diagnosis.
if !diagnosis.TestPolicyMatched {
	return blockForInsufficientEvidence()
}

// Correct: audit uncertainty stays visible while contradictory terminal
// semantics receive one bounded reassessment.
if diagnosis.CausalClosure.ExplainsOriginalSymptom && diagnosis.Fixability == FixabilityInsufficientEvidence {
	return requestCausalClosureReassessment()
}
```
