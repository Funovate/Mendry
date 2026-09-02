# Remediation Harness Walking Skeleton

## Goal

Build the first end-to-end vertical slice of the MVP-1 agentic remediation
harness and, in doing so, freeze the seam contracts that all later MVP-1 slices
depend on. The slice turns a qualifying incident into a durable,
evidence-backed, schema-validated diagnosis and, for a `code_fixable` outcome, a
human-readable suggested patch surfaced in the console. It performs no workspace
mutation, no sandboxed execution, and no Git write.

This slice is the go/no-go checkpoint on diagnosis quality: it must run against
real incidents from a pilot project so we learn whether the model produces
useful diagnoses before investing in the heavy MVP-1 execution core (sandbox,
publication, async engine).

## Scope Decisions (locked)

- **Tool runtime: native adapters.** Repository read and log query are backed by
  native Go adapters behind an evidence/repository port, not an MCP client. MCP
  remains a later extensibility path (see sibling `08-14-stdio-mcp-harness-config`).
- **Checkpoint data: one real source + real incident.** The slice wires at least
  one real log source and real repository read, and is exercised on real pilot
  incidents. Fakes exist for tests, but the checkpoint uses real data.
- **Execution: in-process synchronous coordinator.** The coordinator runs
  synchronously in-process. No PostgreSQL outbox / RabbitMQ consumer in this
  slice, but state transitions and job intents are shaped so the async engine
  drops in later without reshaping them.

## Dependencies And Ownership

Child of `08-14-agentic-remediation-harness` (the MVP-1 umbrella, which owns the
full auto-push design). This slice owns the run lifecycle skeleton, the seam
contract freeze, the context assembler, the structured diagnosis loop, and the
console/diagnosis surface. It defers auto-push, sandbox, and async engine to
later sibling slices.

It reuses existing engineering assets rather than rebuilding them:

- Encrypted credential storage: `projects/adapter/secret/aesgcm.go`.
- Immutable repository baseline config: `projects/domain/project.go`
  `Repository{RemoteURL, SCMProvider, Transport, CredentialSecretID,
  ProductionBranch, DeployedCommit, Version}`.
- Git credential-injection pattern (HTTPS auth remote, SSH temp key):
  `projects/adapter/git/lsremote.go`.
- RBAC roles `admin/operator/viewer`: `auth/domain/user.go`.
- Auth/session and the hexagonal `domain/application/adapter` + sqlc +
  `Version int64` optimistic-locking house style across all modules.
- Source/connector config with strict validation and capabilities:
  `projects/domain/project.go validateSourceConfig`.
- Incident ingestion + status transition hook: `incidents/application/service.go`
  `UpdateStatus`, `Fingerprint`.

## Background

- `jobruntime/` and `platform/rabbitmq/` are empty placeholder directories; no
  worker/outbox/consumer exists yet, and migration `000002` records that a prior
  outbox schema was withdrawn. The async engine is greenfield and deferred.
- The Git adapter only runs `git ls-remote`; it cannot read files or trees.
- `incidents/domain/incident.go` has `Fingerprint` but neither
  `lifecycle_generation` nor `deployed_commit`; the harness series key requires
  both.
- No port/interface for provider, evidence, repository, or SCM exists anywhere in
  `backend/internal`; the seams are prose-only in the umbrella design.

## Requirements

### R1. Seam Contract Freeze

- Define stable Go port interfaces consumed by the coordinator and implemented by
  adapters: repository-read port, evidence/log port, LLM-provider port, and a
  run/decision persistence port. Interfaces must not leak credentials, raw
  clients, or provider SDK types to the coordinator.
- Define the agent protocol envelope as versioned, strictly validated schemas:
  the model returns exactly one of `request_tool | diagnosis | plan_candidates |
  stop`. Unknown fields, unknown evidence IDs, invalid paths, unavailable tools,
  and out-of-phase requests are rejected. (`patch_complete` /
  `validation_assessment` envelopes are defined but unused until the sandbox
  slice.)
- Contracts must be forward-compatible: adding the outbox/async engine, the
  sandbox runner, and the SCM publisher in later slices must not change these
  interface signatures or the run state names.

### R2. Incident Coupling And Trigger

- Add `lifecycle_generation` and `deployed_commit` to the incident aggregate and
  its persistence, additively. Define exactly when `lifecycle_generation`
  increments (recovery/reopen) and how `deployed_commit` is captured from
  repository config at trigger time.
- A qualifying incident transition (MVP default: new/reopened `P1/P2`;
  `admin/operator` may manually start `Info`) synchronously invokes the
  coordinator and creates one remediation series for the unique key
  `(incident_id, lifecycle_generation, deployed_commit)` plus its first run
  attempt.
- Repeated observations for the same fingerprint do not create a second series
  for the same key. The trigger boundary is written so a later outbox emit slots
  in without changing the series/run identity rules.

### R3. Immutable Source Baseline And Bounded Repository Read

- The run records and reads the exact `deployed_commit` from repository config;
  latest remote HEAD must not replace it.
- Extend the repository adapter with bounded `list_tree`, `read_file`, `search`,
  and `history`/`diff` reads at that commit, using the existing
  credential-injection pattern. Credentials exist only inside the adapter and
  never enter model context or the run record.
- Reads are size- and count-bounded; large/binary files are truncated or
  refused with a stable reason.

### R4. Context Assembler (minimal, real source)

- Assemble bounded, redacted context from at least one real log source (via the
  native evidence/log port) and repository read. Retrieval is incremental: the
  model may request more context; the assembler enforces size, count, and
  redaction limits.
- The harness never sends an entire repository or unrestricted logs to the
  model. Every collection request and result is recorded with its context
  version and evidence IDs.

### R5. Structured Diagnosis (real model)

- The agent runs against a real LLM provider (a minimal real adapter behind the
  frozen provider port) so the checkpoint measures real diagnosis quality.
- Diagnosis is schema-validated, cites evidence, and classifies the incident as
  `code_fixable | external_dependency | configuration | data | infrastructure |
  insufficient_evidence | unsafe_to_automate`, with confidence, causal
  reasoning, contradictions, missing evidence, and any bounded follow-up
  collection request.
- Non-code / insufficient-evidence / unsafe outcomes terminate the run with the
  diagnosis and recommended next action; they do not proceed to a patch.

### R6. Suggested Patch (no execution, no write)

- For `code_fixable`, the agent produces one or more candidate plans (evidence,
  affected files, intended behavior, risk, rollback) and a recommended plan with
  rationale, plus a human-readable suggested unified diff.
- The slice performs no workspace mutation, no validation command execution, no
  commit, and no push. The suggested diff is advisory output for a human to
  apply. A single viable plan must explain why alternatives were not meaningful.
- Change-risk classification is computed and displayed (ordinary / high-risk /
  denied-control-plane) for reviewer context, but no enforcement gate on
  publication exists in this slice because nothing is published.

### R7. Tool Gateway (read-only boundary, real enforcement)

- The model receives typed tool descriptions and opaque resource IDs, never Git,
  connector, log-service, or model credentials.
- Every tool request is validated against run phase, path scope, size/time
  budget, and tool availability before a trusted adapter executes it. Only
  read tools exist in this slice (repository read, log/evidence read); any
  mutation/execution request is rejected as unavailable.
- Repository files, logs, and tool output are untrusted: their contents cannot
  grant tools, reveal credentials, or change system policy (prompt-injection
  isolation).

### R8. Durable Run Record, Console, And Notification

- Persist the run, series, input version references, structured diagnosis,
  candidate/selected plan, suggested-diff artifact reference, tool-call metadata,
  evidence references, budget/usage counters, and terminal outcome, following the
  existing hexagonal + sqlc + optimistic-versioning pattern with additive
  migrations.
- Expose the complete review chain (run status, diagnosis, evidence refs,
  suggested diff, risk classification) through protected REST APIs and the
  console, scoped by RBAC. No secret values, raw provider payloads, or
  unrestricted logs are exposed.
- Emit a durable in-console notification for the terminal outcomes present in
  this slice (`non_code_diagnosed`, `diagnosis_ready_for_review`,
  `manual_review_required`, `insufficient_evidence`).

### R9. Budgets, Privacy, And Audit

- Enforce per-run limits: elapsed time, model calls/tokens/cost where available,
  tool calls, evidence bytes, and repository bytes. Budget exhaustion is a
  terminal state.
- Persist structured conclusions, tool-call metadata, and provider/model/usage
  metadata. Do not persist raw provider requests/responses, plaintext
  credentials, repository archives, or duplicate raw logs; stored excerpts are
  redacted and retention-bounded.

## Acceptance Criteria

- [ ] Frozen port interfaces and agent envelope schemas exist with fakes, and a
      contract test proves the coordinator receives no credentials or raw
      clients and that invalid, out-of-phase, or unavailable tool requests are
      rejected before adapter execution.
- [ ] Additive migrations add incident `lifecycle_generation` / `deployed_commit`
      and the run/series/decision/plan/artifact/tool-invocation tables; existing
      incident and project data remain readable and writable.
- [ ] Trigger tests prove the default `P2` threshold, manual `Info` start,
      reopen-driven lifecycle generation, deployed-commit change, fingerprint
      coalescing, and the unique series key, all in-process without duplicate
      root runs.
- [ ] Repository-read tests prove reads occur at the exact deployed commit, are
      size/count/binary bounded, and never expose credentials to the coordinator
      or model.
- [ ] Diagnosis outputs are schema-validated and evidence-cited, and every
      fixability class routes to the correct terminal or continuation state
      against a scripted fake model.
- [ ] Running the slice against a real pilot log source, real repository, and a
      real model on at least a small set of real incidents produces
      schema-valid, evidence-cited diagnoses and, for a code-fixable case, a
      human-readable suggested diff — with a written quality read-out that
      informs the go/no-go for later slices.
- [ ] Console/API responses expose the full diagnosis + suggested-diff + risk
      review chain without secrets, raw provider payloads, or unrestricted logs,
      and are RBAC-scoped.
- [ ] Budget/audit tests prove per-run limits terminate a run and that no raw
      prompts/responses/credentials are persisted.
- [ ] A forward-compatibility note (or test) demonstrates that the outbox/async,
      sandbox, and SCM-publisher slices can be added without changing the frozen
      interface signatures or run state names.

## Out Of Scope (deferred to later MVP-1 slices)

- Sandbox runner, ephemeral workspace checkout, workspace mutation, and
  validation-command execution.
- Automatic commit, push, branch/commit-convention inference, draft PR, and the
  publication risk-enforcement gate.
- PostgreSQL outbox, RabbitMQ consumer, retry/dead-letter, and worker-restart
  redelivery durability.
- MCP-client tool transport, multi-provider LLM support, native email / chat
  notification adapters, and webhook delivery.
- Any automatic merge, deployment, or production change (out of scope for the
  entire product).

## Resolved Decisions (pilot checkpoint)

- **Log source: SSH log path.** The pilot `EvidenceLogPort` reads a bounded,
  redacted log file (or rotated set) over SSH, reusing the existing SSH
  credential-injection pattern (`projects/adapter/git/lsremote.go` temp key +
  `IdentitiesOnly`/`BatchMode`). Native Go adapter, no MCP client. CLS/cloud
  sources are deferred to later slices.
- **LLM provider/model: OpenAI `gpt-5.6`.** A minimal real `LLMProviderPort`
  adapter targets OpenAI `gpt-5.6`. Its API secret lives in the existing
  encrypted credential store (`projects/adapter/secret/aesgcm.go`,
  `FIXTHE_ENCRYPTION_KEY`); the provider SDK type never crosses the port.

## Open Questions

- **Checkpoint sample size and quality bar (proposed default, refine after first
  runs):** exercise ~10 real pilot incidents spanning at least two fixability
  classes (including ≥1 `code_fixable`). Passing go/no-go = a reviewing engineer
  judges the root-cause direction correct for a majority, every cited evidence ID
  resolves to real collected evidence (no fabricated citations), and at least one
  `code_fixable` case yields a suggested diff a human would apply with only minor
  edits. Confirm or adjust these numbers before Step 9.
