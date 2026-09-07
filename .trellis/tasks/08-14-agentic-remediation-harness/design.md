# Technical Design: Agentic Remediation Harness

## Purpose

This document describes only the remaining production integration. The
existing remediation coordinator already owns diagnosis, planning, lifecycle
states, recovery, budgets, checkpoints, and durable effect contracts. The
implementation should connect those contracts to authorized product entry
points and production adapters, not replace the coordinator.

## Existing And Missing Boundaries

```text
Already live

incident/webhook/manual start
  -> RemediationService
  -> RemediationCoordinator
  -> diagnosis -> planning -> diagnosis_ready_for_review
  -> PostgreSQL run/decision/plan/artifact/checkpoint records
  -> review API -> incident UI

Already implemented behind test ports

ApplyPlan / ResumeLifecycle
  -> patching -> validating -> publishing -> awaiting_human_review
  -> WorkspacePort
  -> ValidationPort
  -> PublicationPort
  -> LifecycleStore (PostgreSQL implementation exists)

Remaining production integration

plan approval API/UI
  -> durable application admission
  -> existing lifecycle coordinator
  -> production workspace + validator + artifact store + publisher
  -> lifecycle result projection
  -> human merge/release handoff
```

`RemediationCoordinator` remains the only run-state owner. HTTP handlers,
frontend code, workspace adapters, validators, and publishers cannot transition
runs directly.

## Plan-Application Admission

Add an application service contract separate from HTTP:

```go
type PlanApplicationInput struct {
    ProjectKey        string
    IncidentNumber    int64
    Generation        int64
    RunID             string
    ExpectedRunVersion int64
    PlanID            string
}
```

The actual type may follow local naming conventions, but it must carry all six
identity/version boundaries. The service performs, in order:

1. `RequireIncidentWrite` for the authenticated principal.
2. Resolve the incident inside that project.
3. Match lifecycle generation and deployed commit.
4. Load the latest series/run and compare run ID/version.
5. Require `resilient_v1` and `diagnosis_ready_for_review`.
6. Resolve the selected plan from that run.
7. Load and validate execution policy and credentials.
8. Persist a checkpoint/admission that includes selected plan and policy
   snapshots before returning acceptance or starting external effects.

A duplicate request with the same identity returns the current admitted run. A
request for another plan, version, run, project, or lifecycle after admission
returns conflict. No external adapter is called before all checks and durable
admission succeed.

## HTTP Contract

Add one protected endpoint:

```text
POST /api/v1/projects/{projectKey}/incidents/{incidentId}/remediation/apply
```

Request:

```json
{
  "generation": 1,
  "runId": "run-uuid",
  "version": 28,
  "planId": "plan-key"
}
```

The response uses the existing standard envelope and returns a bounded action
projection with run ID, status, generation, attempt number, version, and whether
execution was newly admitted or already active. Stable errors distinguish
invalid input, forbidden, not found, stale conflict, unsupported mode/state,
policy rejection, and missing execution configuration.

Long-running patch/validation/publication work must not rely on the client
connection staying open. Admission is persisted first; execution can be driven
in-process after admission. An authorized repeated request and the process
startup resumer both use `ResumeLifecycle` for active `patching`, `validating`,
or `publishing` states.

## Policy Snapshot

Project-owned execution configuration is versioned and loaded through an
application port. A run checkpoint stores immutable references or bounded
projections for:

- approved validation command IDs and versions;
- argv, working-directory policy, timeout, output bound, resource limits, and
  bounded network policy resolved only inside the validator;
- ordinary, opted-in high-risk, and denied path/change classes;
- changed-file, patch-byte, and repair-iteration limits;
- artifact store/retention policy version;
- publication target branch and restricted branch prefix;
- publication credential reference and configuration version.

The model sees command IDs and safe policy outcomes, never argv secrets,
credentials, authenticated remotes, or unrestricted execution controls.

A production `PlanPolicyEvaluator` maps the project policy to the existing
`PlanPolicyDecision`. Denied control-plane/binary paths remain manual-only.
High-risk paths continue only when the project explicitly opts in and the
required specialized validation command IDs are present.

## Workspace And Artifact Adapter

The production `WorkspacePort` owns a disposable checkout bound to:

```text
run ID + project ID + exact deployed commit + workspace version
```

`Ensure` is idempotent. It checks out the exact baseline and records base/current
tree hashes. `ApplyPatch` requires the expected current tree hash, validates the
bounded unified diff and path policy, applies without Git hooks, and records the
result tree hash and changed files. `Destroy` is idempotent and is attempted on
terminal states plus TTL cleanup.

The workspace does not receive the publication credential. Symlinks,
submodules, hooks, binaries, control-plane paths, and paths outside policy fail
closed.

A content-addressed artifact store persists the complete bounded patch and
redacted validation outputs. Artifact identity includes project/run ownership,
content hash, media kind, size, creation time, and retention policy. PostgreSQL
lifecycle effects keep only references, hashes, and safe summaries.

## Validation Adapter

`ValidationPort.Run` resolves `(CommandID, CommandVersion)` from the snapshotted
project policy. It executes argv directly without a shell. Model-provided shell
text is not part of the contract.

The runner is a separate isolation boundary with:

- no model, connector, evidence, Git-push, or production credentials;
- no host filesystem outside the workspace;
- no container-engine socket or elevated container authority;
- non-root execution, dropped capabilities, and no-new-privileges;
- deny-by-default network with only command-version-approved destinations;
- CPU, memory, process, elapsed-time, and output bounds.

The adapter returns only exit status, timing, bounded/redacted output artifact,
output hash, and safe summary. A passing result is valid only for its expected
workspace tree hash.

## Publication Adapter

`PublicationPort` is deterministic and trusted. It receives no mutable
workspace. It:

1. Creates a fresh checkout from the snapshotted remote and exact deployed
   commit.
2. Fetches the patch from the artifact store by project/run-scoped reference.
3. Verifies patch content hash.
4. Applies it and verifies the expected result tree hash.
5. Revalidates remote identity, target branch, source ancestry, branch prefix,
   and idempotency state.
6. Creates or reuses the restricted branch and commit, then pushes.
7. Creates a draft change request when the configured provider adapter supports
   it; otherwise returns branch and optional compare metadata.

The credential is scoped for the configured repository and branch namespace and
exists only inside the publication adapter. The port intentionally has no
merge, deploy, rollback, or production-mutation method.

## Bootstrap And Recovery

Bootstrap wires:

```text
RunStore as RunStore + LifecycleStore
CheckpointStore
PlanPolicyEvaluator
WorkspacePort
ValidationPort
PublicationPort
execution policy loader
artifact store
startup lifecycle resumer
```

The startup resumer scans only bounded active lifecycle states and calls
`ResumeLifecycle`. Existing successful lifecycle effects are authoritative, so
workspace, patch, validation, or publication is not repeated. Recoverable
adapter failures retain a safe code and phase; hard configuration or policy
failures remain inspectable and do not trigger unsafe fallback behavior.

The runtime stays in-process. This design does not require RabbitMQ or a job
outbox. A later asynchronous engine may call the same admission/resume contracts
without changing lifecycle state names or port signatures.

## Review Projection And UI

Extend the existing review projection additively with a lifecycle section:

```text
selectedPlanId
execution.active
execution.phase
execution.lastSafeReason
workspace.changedFiles
validation.commandId/version/status/summary/artifactRef
publication.branchRef/commitHash/draftChangeRef/compareUrl
publication.humanReviewRequired
nextAction
```

Do not expose raw patch artifacts automatically, raw validation output,
credentials, local workspace paths, or authenticated remotes. Existing
suggested-diff review remains available before admission.

The UI adds plan selection and one explicit apply command for authorized users.
The confirmation surface shows plan, risk, affected files, rollback, baseline,
and the human-review boundary. After admission it polls the existing review
query and renders active phases, recoverable failures, validation result, and
publication handoff. Viewer and stale-state controls are absent or disabled.

## Child Delivery Boundaries

### Child 1: Execution Policy Configuration

Owns project domain/storage/API/UI for approved commands, risk rules, artifact
policy, publication policy, and scoped write credential metadata. It does not
execute code or publish Git changes.

### Child 2: Lifecycle Runtime Adapters

Owns workspace, isolated validation, artifact storage, publisher, lifecycle
store bootstrap injection, and restart resumer. It does not add public HTTP or
frontend controls.

### Child 3: Apply-Plan API And UI

Owns authorization, identity/version validation, durable admission, HTTP
mapping, review projection, plan selection, execution controls, and UI states.
It consumes child 1 policy and child 2 ports.

### Child 4: Controlled Pilot And Parent Review

Owns a non-production end-to-end run, failure drills, security review, written
read-out, and the final decision on per-project execution enablement.

## Rollout And Rollback

- Execution remains disabled by default and requires complete validated project
  configuration.
- Enable one designated pilot project first; diagnosis/advisory behavior for all
  other projects remains unchanged.
- Roll back by disabling new plan admission. Preserve active/terminal runs,
  lifecycle effects, artifacts, and already-pushed branches for audit and human
  handling.
- Do not delete remote branches automatically and do not claim a published
  change was reverted or deployed.

## Explicit Non-Goals

No RabbitMQ/outbox worker, automatic merge/deploy, production recovery claim,
generic HTTP log connector, stdio MCP runtime, native PR adapter for every SCM,
or broad notification platform belongs to this parent completion path.
