# Technical Design: Remediation Harness Walking Skeleton

## Design Principle

Inherited from the umbrella (`08-14-agentic-remediation-harness/design.md`): the
harness, not the model, owns authority. The model reasons over bounded, redacted
observations and requests typed read actions; deterministic coordinator code
validates policy, executes trusted adapters, records effects, and decides
whether the run may continue. Credentials live only inside trusted adapters and
never become model context.

This slice realizes that principle for the read-only diagnosis path and freezes
the interfaces the later execution slices extend.

## Alignment With Current Backend Contracts

- The in-process synchronous coordinator is chosen deliberately and is
  consistent with `spec/backend/directory-structure.md`, which states RabbitMQ,
  workers, and durable outbox "must not be added without a concrete product
  consumer." The async engine arrives in a later slice when publication makes it
  a real consumer.
- New code lives under `internal/modules/remediation/{domain,application,
  adapter/...}` per the module-organization rule. `application`/`domain` import
  no HTTP, env, pgx, or redis. Adapters own concrete clients and map rows to
  feature-owned types.
- HTTP surfaces use the standard success/error envelope, `DecodeJSON`,
  `WriteJSON`/`WriteListJSON`, and stable machine codes (`spec/backend/
  error-handling.md`). Errors are wrapped with `fmt.Errorf("operation: %w", err)`
  and never logged-and-returned by lower packages.
- Credential handling reuses the AES-GCM store and `FIXTHE_ENCRYPTION_KEY`; no
  new secret transport is introduced.

## Component Boundaries (this slice)

```text
incidents.UpdateStatus  (existing hook)
      |  synchronous, in-process
      v
RemediationCoordinator ---------------------------+
   |            |               |                 |
   v            v               v                 v
ContextAssembler   AgentEngine       ToolGateway      RunStore
   |     \            |                 |             (postgres)
   v      v           v                 v
RepositoryReadPort  EvidenceLogPort   LLMProviderPort
   |                  |                 |
   v                  v                 v
git read adapter   log source adapter  real provider adapter
(at deployed commit) (one real source) (one real model)
      |
      v
DiagnosisResult / SuggestedPatch  --> Console REST + in-console Notification
```

`RemediationCoordinator` is the only component that advances run state.
`AgentEngine` receives tool schemas and returns validated envelopes; it never
receives adapter clients or credentials. `ToolGateway` is the sole execution
entry point for model tool requests and only read tools exist here.

## Frozen Seam Contracts

These signatures are the freeze point. Later slices add methods on new ports
(sandbox, publisher) and add unused envelope variants, but do not change these.

### Ports (Go, illustrative signatures)

```go
// RepositoryReadPort reads a repository at an immutable commit. The adapter
// injects credentials internally; callers pass only opaque references.
type RepositoryReadPort interface {
    ListTree(ctx context.Context, ref RepoRef, path string, opts TreeOptions) (TreeListing, error)
    ReadFile(ctx context.Context, ref RepoRef, path string, opts ReadOptions) (FileContent, error)
    Search(ctx context.Context, ref RepoRef, query SearchQuery) (SearchResult, error)
    History(ctx context.Context, ref RepoRef, path string, opts HistoryOptions) (History, error)
}

// EvidenceLogPort retrieves bounded, redacted evidence/log windows.
type EvidenceLogPort interface {
    Search(ctx context.Context, scope EvidenceScope, query LogQuery) (EvidencePage, error)
    Context(ctx context.Context, scope EvidenceScope, anchor EvidenceAnchor) (EvidencePage, error)
}

// LLMProviderPort runs one bounded model turn. Returns normalized usage and the
// raw text to be schema-validated by the AgentEngine; provider SDK types and
// secrets never cross this boundary.
type LLMProviderPort interface {
    Complete(ctx context.Context, req ModelTurn) (ModelResult, error)
}

// RunStore persists run/series/decision/plan/artifact/tool-invocation records
// with optimistic versioning inside a single transaction.
type RunStore interface {
    CreateSeriesAndRun(ctx context.Context, in NewRun) (Run, error)
    AppendDecision(ctx context.Context, runID string, d Decision) error
    RecordToolInvocation(ctx context.Context, runID string, t ToolInvocation) error
    Transition(ctx context.Context, runID string, from, to RunState, effect Effect) error
    Get(ctx context.Context, runID string) (RunAggregate, error)
}
```

`RepoRef` carries `{projectID, repositoryConfigVersion, deployedCommit}` — never
a URL with an embedded credential. `EvidenceScope` carries opaque source and
evidence-profile IDs, never connector secrets.

### Agent Protocol Envelope (versioned, strict)

The model returns exactly one JSON object; the AgentEngine rejects unknown
fields, unknown evidence IDs, invalid paths, unavailable tools, and out-of-phase
requests:

```text
{ "kind": "request_tool", "tool": "<id>", "arguments": { ... } }
{ "kind": "diagnosis", "fixability": "code_fixable|external_dependency|
      configuration|data|infrastructure|insufficient_evidence|
      unsafe_to_automate", "confidence": 0.0-1.0, "evidenceIds": [...],
      "reasoning": "...", "contradictions": [...], "missingEvidence": [...],
      "verificationSteps": [...], "followUp": { optional collection request } }
{ "kind": "plan_candidates", "candidates": [ { intendedBehavior, evidenceIds,
      affectedPaths, changeSteps, compatibilityImpact, risk, rollback } ],
      "recommendedIndex": n, "rationale": "..." }
{ "kind": "stop", "reason": "..." }
```

`patch_complete` and `validation_assessment` are declared in the schema with a
`schemaVersion` but are unreachable until the sandbox slice enables mutation
tools. The envelope is versioned so later additions are additive.

## Incident Coupling

Additive changes to `incidents/domain/incident.go` and its persistence:

- `LifecycleGeneration int64` — starts at 1; increments only on a
  recovery→reopen transition. Captured by `incidents.UpdateStatus` when it
  detects reopen.
- `DeployedCommit string` — captured at trigger time from the project
  `Repository.DeployedCommit` at its recorded `Version`; stored on the incident
  so the series key is stable even if project config later changes.

The trigger is a synchronous call inside `UpdateStatus` (and the manual-start
path) that hands the coordinator `(incidentID, lifecycleGeneration,
deployedCommit)`. This call site is the exact seam where a later slice replaces
the direct call with an outbox insert in the same transaction; the identity
rules for series/run do not change.

## Data Model (additive migrations)

New tables under the remediation module, following the sqlc + `version bigint`
optimistic-locking pattern:

- `remediation_series` — `(incident_id, lifecycle_generation, deployed_commit)`
  unique constraint enforces the series key at the DB level; plus project ref
  and current attempt number.
- `remediation_run` — identity, series ref, attempt number, state, input version
  refs (repository config version, agent-policy version, template version,
  provider config version), evidence-context version, budget counters, terminal
  reason, timestamps, `version bigint`.
- `remediation_decision` — diagnosis fixability, confidence, evidence links,
  reasoning summary, contradictions, missing evidence, verification steps, and
  the context version + evidence IDs used.
- `remediation_plan` — candidate and selected plan fields plus selection
  rationale and computed risk classification.
- `remediation_artifact` — reference + content hash + retention for the
  suggested unified diff (stored in an application-owned artifact location, not
  inline in audit JSON).
- `remediation_tool_invocation` — tool ID, normalized arguments, phase, policy
  decision, timing, bounded/redacted result reference, usage counters. No
  secret-bearing parameter is ever stored.

Existing incident/project data stays readable and writable; all additions are
new columns/tables gated behind per-project enablement.

## In-Process Coordinator Flow And State Machine

State subset for this slice (names are the frozen superset from the umbrella; the
deferred states exist but are unreachable here):

```text
queued -> preparing_context -> diagnosing
  diagnosing -> collecting_more_context -> diagnosing   (bounded)
  diagnosing -> completed_non_code                      (terminal)
  diagnosing -> blocked_manual_review                   (terminal)
  diagnosing -> planning
  planning   -> diagnosis_ready_for_review              (terminal: suggested diff)
  any active -> failed | budget_exhausted               (terminal)
```

Each transition uses optimistic versioning and stores the validated phase result
before continuing. Because execution is synchronous and in-process, "job intent"
is a direct next-phase call rather than an enqueue; the transition API already
takes an `Effect` value so the outbox slice records intent in the same
transaction without changing callers.

`patching`, `validating`, `publishing`, and `awaiting_human_review` states are
reserved and not entered in this slice (no mutation tools, no publisher).

## Tool Gateway (read-only)

The registry advertises only read tools per phase:

- `preparing_context` / `diagnosing`: `repository.list_tree`,
  `repository.read_file`, `repository.search`, `repository.history`,
  `evidence.search`, `evidence.context`.
- Any `request_tool` for a mutation/execution tool (`workspace.apply_patch`,
  `workspace.run_validation`, …) is rejected as `tool_unavailable` — those IDs
  are defined but unregistered until the sandbox slice.

The gateway validates phase, path scope (within repo, no traversal), size/time
budget, and availability before calling the adapter. Adapter output is summarized
and redacted before the next model turn. Tool output is untrusted and cannot
grant tools or change policy.

## Real-Data Checkpoint Wiring

- One real repository read adapter extends the existing Git credential-injection
  pattern (`projects/adapter/git`) to `list_tree`/`read_file`/`search` at the
  deployed commit.
- One real log source adapter implements `EvidenceLogPort` over an **SSH log
  path** via a native Go adapter, reusing the SSH credential-injection pattern
  from `projects/adapter/git/lsremote.go` (temp key file, `IdentitiesOnly`,
  `BatchMode`, `StrictHostKeyChecking=accept-new`). Reads are bounded, paginated,
  and redacted; the SSH credential stays inside the adapter.
- One real `LLMProviderPort` adapter targets **OpenAI `gpt-5.6`**. The API secret
  lives in the existing encrypted credential store (`FIXTHE_ENCRYPTION_KEY`); the
  provider SDK type and secret never cross the port.

The console renders the run, diagnosis, evidence references, suggested diff, and
risk classification through the standard REST envelope, RBAC-scoped by
`admin/operator/viewer`.

## Forward Compatibility

- **Async engine slice**: replace the synchronous coordinator invocation at the
  `UpdateStatus` seam with a transactional outbox insert + RabbitMQ consumer; the
  `RunStore.Transition(..., Effect)` shape already carries next-intent, and run
  state names are unchanged.
- **Sandbox slice**: register `workspace.*` tools, add a `SandboxRunner` port and
  the ephemeral workspace; the AgentEngine envelope already reserves
  `patch_complete`/`validation_assessment`.
- **Publisher slice**: add an `SCMPublisherPort` and the `patching → validating →
  publishing → awaiting_human_review` path; the artifact record already stores a
  content-hashed diff for replay verification.

No later slice requires changing the port signatures or envelope contract frozen
here; they add new ports, new tool registrations, and new (already-named) states.

## Testing And Fakes

- Fakes for every frozen port (repository, evidence, provider, run store) drive
  deterministic contract and state-machine tests, including a scripted fake model
  exercising each fixability class and the bounded collect-more-context loop.
- A contract test asserts the coordinator and AgentEngine receive no credentials
  or raw clients and that out-of-phase / unavailable / oversized tool requests
  are rejected before any adapter call.
- The real-data checkpoint is a separate, human-observed run against pilot
  incidents producing a written quality read-out; it is not a CI gate.
