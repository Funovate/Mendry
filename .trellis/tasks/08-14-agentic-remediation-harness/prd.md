# Agentic Remediation Harness

## Goal

Build the backend incident-remediation harness that turns a triggered error
into a durable, evidence-backed diagnosis and, when policy permits a safe code
fix, an isolated, tested, committed, pushed, and review-ready Git change. The
harness automates analysis and change preparation while a human remains the
only authority allowed to merge or deploy the change.

## Dependencies And Ownership

This task is a child of `08-10-production-incident-mvp` and owns the agent run
lifecycle, tool-policy gateway, structured reasoning loop, isolated repository
workspace, repair-plan selection, bounded revision loop, and end-to-end
orchestration.

It reuses rather than duplicates sibling boundaries:

- `08-10-incident-evidence-notifications` owns provenance-backed evidence,
  redaction, and connector-mediated log collection.
- `08-10-incident-llm-providers` owns provider transports, provider secrets,
  and normalized model request/result handling.
- `08-10-incident-remediation-changes` owns the durable remediation package
  and SCM-specific branch, push, and draft-PR adapters.
- `08-10-incident-service-foundation` owns the worker, transactional outbox,
  RabbitMQ delivery, authentication, authorization, and encrypted secrets.

## Background

- Project repository configuration already records a remote URL, SCM provider,
  transport, opaque credential reference, production branch, and immutable
  deployed commit in
  `backend/internal/modules/projects/domain/project.go:82-90`.
- The current Git adapter only runs `git ls-remote`; it does not clone, inspect,
  edit, commit, push, or create a pull request
  (`backend/internal/modules/projects/adapter/git/lsremote.go:27-64`).
- Current project APIs expose only secret metadata and repository references
  (`backend/internal/modules/projects/adapter/http/handler.go:52-64`).
- The RabbitMQ/outbox worker described by the parent design is not implemented
  in the current backend; current tests explicitly assert that `job_outbox`
  does not exist (`backend/tests/integration/postgres_test.go:152-160`).
- The approved product boundary permits automatic restricted hotfix branch
  creation, patching, testing, push, and draft-PR creation, but prohibits
  automatic merge, deployment, production configuration/database changes, or
  unverified claims of recovery.

## Requirements

### R1. Durable Run Lifecycle

- Each project configures whether automatic remediation is enabled and its
  minimum incident priority; the MVP default threshold is `P2`, so new or
  reopened `P1/P2` incidents qualify automatically while `Info` incidents
  require a manual start by a project `admin` or `operator`.
- A qualifying new/reopened incident transition creates one remediation series
  for the unique combination of incident, lifecycle generation, and exact
  deployed commit, then creates its first versioned run attempt and durable job.
  RabbitMQ messages carry identifiers and versions, never credentials,
  repository archives, raw logs, or prompts.
- Repeated observations for the same fingerprint update the incident and its
  append-only evidence/context version but do not create another run for the
  same lifecycle generation and deployed commit. Recovery/reopening or a changed
  deployed commit creates a new series. An explicitly authorized retry creates
  a linked, monotonically numbered run attempt inside the existing series; it
  cannot create a second automatic root run.
- The run has explicit persisted states covering queued, context preparation,
  diagnosis, planning, patching, validation, publication, awaiting human review,
  non-code outcome, blocked/manual-review outcome, failure, cancellation, and
  budget exhaustion.
- Worker interruption and at-least-once redelivery resume from authoritative
  persisted state without creating duplicate branches, commits, pushes,
  notifications, or draft PRs.

### R2. Versioned Inputs And Immutable Source Baseline

- Each run references the incident, triggering observation, error location,
  evidence IDs, project/environment/source, repository configuration version,
  the configured production branch at its exact recorded deployed commit,
  agent policy version, prompt/template version, and model provider
  configuration version.
- Diagnosis, code inspection, patching, and validation all use that immutable
  deployed commit. The latest remote branch HEAD must not silently replace the
  incident's actual deployed source, even when the remote branch has advanced.
- Later project configuration changes do not silently change an in-flight run.
  An explicit retry or replacement run records the new input versions.
- Evidence added while a run is active advances an explicit context version;
  every model decision records the context version and evidence IDs it used.

### R3. Capability-Based Tool Gateway

- The model receives typed tool descriptions and opaque resource identifiers,
  not Git, SCM, connector, log-service, model, or notification credentials.
- The harness validates every requested tool call against the run phase,
  project policy, source capability, repository scope, path scope, size and
  time budgets, and idempotency state before a trusted adapter executes it.
- Initial tools cover bounded repository search/read/history, bounded log
  search/context retrieval, remediation workspace read/write/diff, and
  configured formatter/linter/test execution. Commit, push, and change-request
  creation are deterministic coordinator effects after policy and validation
  gates; they are not model-callable tools.
- Repository files, logs, and tool output are untrusted input. Their contents
  cannot grant tools, reveal credentials, change system policy, or authorize a
  merge/deploy action.

### R4. Context Discovery

- The harness checks out the selected immutable source baseline into an
  ephemeral isolated workspace and discovers repository guidance, language and
  package structure, dependency manifests, formatting/lint/test conventions,
  recent commit-message conventions, and relevant code paths.
- Context is retrieved incrementally. The harness does not send an entire
  repository or unrestricted full logs to a remote model.
- Log retrieval remains connector-mediated, redacted, paginated, and bounded.
  The model may request more context, but the evidence layer validates and
  records every collection request and result.

### R5. Structured Diagnosis And Fixability Decision

- Diagnosis must cite evidence and classify the incident as `code_fixable`,
  `external_dependency`, `configuration`, `data`, `infrastructure`,
  `insufficient_evidence`, or `unsafe_to_automate`.
- The result records confidence, causal reasoning, contradictory evidence,
  missing evidence, verification steps, and any bounded follow-up collection
  request. Classification is validated before it changes run state.
- Non-code, insufficient-evidence, and unsafe outcomes stop code mutation and
  notify users with the diagnosis and recommended next action.

### R6. Candidate Plans And Selection

- For a `code_fixable` outcome, the agent produces one or more structured
  candidate plans. Each candidate identifies evidence, affected files,
  intended behavior, compatibility impact, tests, risk, and rollback.
- The agent recommends the candidate that best matches repository conventions
  and incident evidence. The harness independently rejects candidates that
  exceed change policy or require unavailable capabilities.
- The selected plan and selection rationale are persisted before mutation. A
  single viable plan must explain why alternatives were not meaningful rather
  than inventing artificial choices.

### R7. Branch And Commit Convention

- The harness inspects remote branch names and, when supported, recent merged
  change metadata to derive the repository's hotfix prefix, separators,
  incident/ticket token style, slug style, and length constraints.
- Inference is deterministic, produces evidence and confidence, and is checked
  against the configured write namespace. The model may suggest a descriptive
  slug but cannot select an unrestricted ref.
- When repository history is absent or ambiguous, the fallback branch template
  is `hotfix/INC-<incident-number>-<short-problem-slug>`. The slug is sanitized
  and bounded; a collision appends a stable short remediation-run ID.
- Commit messages follow an inferred or configured project convention and
  include a stable incident/remediation reference without secrets or raw logs.
- The hotfix branch is created from the exact recorded deployed commit and the
  draft PR targets the configured production branch. If that branch has moved,
  any resulting conflict remains visible for human resolution; the harness
  does not rebase onto unverified code or change the run baseline implicitly.

### R8. Isolated Repair And Validation

- All model-directed file changes and project commands run in a disposable,
  resource-limited workspace with no production credentials, production
  network path, host filesystem access, or ability to bypass the tool gateway.
- A dedicated sandbox-runner boundary owns one stable sandbox identity per run
  attempt. The general worker must not mount a container-engine socket or gain
  host container authority. Sandboxes run rootless with a project-configured
  read-only toolchain image (per target language/ecosystem), dropped
  capabilities, no-new-privileges, deny-by-default network, resource quotas,
  explicit terminal cleanup, and TTL cleanup after worker loss.
- Writable paths, diff size, file count, binary changes, command set, command
  duration, network policy, and maximum repair iterations are bounded by a
  versioned project policy.
- The MVP risk policy has three publication levels:
  - Ordinary application source and tests may be committed and pushed after
    required validation succeeds.
  - Database migrations/schema, authentication/authorization, and dependency
    manifest or lockfile changes require explicit project opt-in plus their
    configured specialized checks. Published draft changes are marked high
    risk for human review.
  - CI/CD workflows, deployment manifests, infrastructure-as-code, credential
    or secret policy, Git hooks/submodules, and binary artifacts are denied for
    automated mutation and push.
- A denied or non-opted-in high-risk plan may still produce an evidence-backed
  diagnosis and manual remediation plan, but the harness must not create a
  publishable commit from it.
- The harness runs the repository's configured formatting, lint, type-check,
  unit, and targeted test commands as applicable. It records sanitized command,
  exit status, duration, and bounded output artifacts.
- During project setup, the system may inspect repository manifests, build
  files, and CI definitions to propose formatter, lint, type-check, and test
  command definitions. A project administrator must review and persist each
  approved command as a versioned command ID before the harness may execute it.
- Agent runs may request only approved command IDs. The model cannot submit a
  shell string, and a command-definition change does not alter an in-flight
  run's snapshotted command version.
- A failed validation may return to a bounded repair iteration. Exhausted
  budget, policy rejection, or unresolved tests stops publication and requests
  human review.
- After successful validation, the harness captures only the bounded patch,
  expected resulting Git tree hash, and approved validation artifacts in a
  content-addressed artifact store, then destroys the sandbox. It does not
  persist or pass a mutable workspace or repository archive to publication.

### R9. Publication Integration Contract

- The harness may invoke the sibling change publisher only for a
  policy-compliant diff with required validation results. The publisher uses a
  separately scoped Git/SCM change credential that is unavailable to the model
  and repair sandbox.
- The trusted publisher creates a fresh checkout from the exact deployed commit,
  applies the content-addressed patch, and verifies the resulting tree hash
  before commit or push. A mismatch rejects publication.
- Publication verifies the remote target and base relationship immediately
  before push, records the resulting branch and commit IDs, and is idempotent
  under worker retry.
- The SCM adapter creates a draft pull/merge request when supported. Otherwise
  it returns the pushed branch reference and, where supported, a stable compare
  link for manual change-request creation and review.
- The integrated MVP uses provider-neutral HTTPS/SSH Git checkout, branch,
  commit, and push for configured GitHub, GitLab, Yunxiao, Gitee, and generic
  remotes. The sibling Yunxiao publisher additionally creates a native draft
  PR. Other providers stop after branch push and expose branch/compare metadata;
  native GitHub, GitLab, and Gitee change-request adapters are follow-up work.
- The harness can never approve or merge the request, push directly to the
  protected production branch, deploy, or execute production configuration,
  infrastructure, or database changes.

### R10. User Notification And Review

- The MVP notification surfaces are durable in-console notifications and
  configurable HMAC-signed outbound webhooks. It does not implement native
  email or vendor-specific chat-bot adapters.
- The harness emits stable `non_code_diagnosed`, `manual_review_required`,
  `repair_failed`, and `change_ready_for_review` events for the corresponding
  outcomes. Authorized project members can inspect the durable notification
  and complete run record in the console even when webhook delivery fails.
- A successful notification includes the incident, diagnosis, selected-plan
  rationale, evidence references, changed-file/diff summary, validation
  results, risk and rollback, branch, commit, draft PR or compare link, and the
  explicit requirement for human merge.
- Notification delivery is signed, durable, retryable, idempotent, and audited
  through the sibling notification runtime.

### R11. Audit, Privacy, And Budgets

- Persist run state, input version references, structured conclusions, selected
  plan, policy decisions, tool-call metadata, evidence/artifact references,
  validation summaries, branch/commit/PR identifiers, token/usage metadata,
  and outcomes.
- Do not persist raw provider requests/responses, plaintext credentials,
  unrestricted repository archives, or duplicate raw logs. Stored excerpts and
  command output are redacted and retention-bounded.
- Enforce per-run limits for elapsed time, model calls/tokens/cost where
  available, tool calls, evidence bytes, repository bytes, diff size, command
  execution, repair iterations, and provider/job retries.
- Administrators can disable new runs globally or per project; operators can
  manually start/retry or cancel a run. Cancellation cannot undo an
  already-pushed commit but prevents further automated effects and records the
  terminal state.

## Acceptance Criteria

- [ ] A fake incident, evidence connector, model, repository, sandbox, SCM, and
      notifier exercise the complete durable path from trigger to either a
      classified non-code outcome or a review-ready pushed change.
- [ ] Trigger tests prove the default `P2` threshold, project override, manual
      `Info` start, lifecycle reopening, deployed-commit change, fingerprint
      coalescing, context-version advancement, unique series key, and linked
      retry attempts behave without duplicate root jobs or external effects.
- [ ] Contract tests prove the model never receives credentials or direct
      clients and that invalid, out-of-phase, over-budget, or policy-expanding
      tool calls are rejected before adapter execution.
- [ ] Diagnosis outputs are schema-validated, evidence-cited, and route every
      supported fixability class to the correct terminal or continuation state.
- [ ] A code-fixable fixture produces candidate plans, records the selected
      rationale, changes only allowed paths, follows discovered project style,
      passes configured validation, and publishes exactly one branch/commit and
      one draft PR or compare link.
- [ ] SCM contract tests prove every configured provider can use the common Git
      checkout/push path, Yunxiao alone creates a native draft PR in the MVP,
      and GitHub/GitLab/Gitee/generic publication returns branch/compare
      metadata without pretending a change request exists.
- [ ] A non-code, insufficient-evidence, unsafe, failing-test, oversized-diff,
      forbidden-path, timeout, and exhausted-budget fixture creates no push and
      sends the correct audited user notification.
- [ ] Risk-policy tests prove ordinary source/test changes can publish after
      validation, high-risk changes require explicit opt-in and specialized
      checks, and denied control-plane/binary changes cannot be committed or
      pushed even when their generic test suite passes.
- [ ] Branch and commit convention tests cover a confident repository pattern,
      ambiguous history with configured fallback, name collision, invalid ref,
      and RabbitMQ redelivery.
- [ ] Sandbox tests prove project commands cannot access production credentials,
      host paths, unauthorized network destinations, the Git push identity, or
      a container-engine socket; terminal and TTL cleanup destroy the sandbox.
- [ ] Command-policy tests prove discovery only proposes commands, administrator
      approval creates versioned executable command IDs, unapproved or changed
      commands cannot run, and the model cannot inject arbitrary shell text.
- [ ] Publication tests prove the selected source baseline, remote target, and
      protected-branch boundary are revalidated before push; an advanced remote
      production branch never replaces the recorded deployed commit, patch/tree
      hash mismatch prevents a write, and no API or worker path can merge or
      deploy.
- [ ] Restart/redelivery tests resume every externally visible phase without
      duplicating durable evidence, commits, pushes, PRs, or notifications.
      Uncertain read/model attempts may repeat only within the shared bounded
      budget and remain audited without duplicating durable state transitions.
- [ ] Console/API responses expose the complete review chain and status without
      exposing secret values, raw provider payloads, or unrestricted logs.
- [ ] Notification tests cover every stable remediation event through both the
      durable console record and an idempotent signed-webhook fake, including
      retry/exhaustion without losing the console-visible outcome.

## Out Of Scope

- Automatic merge, release, deployment, rollback execution, production shell,
  production database writes, or production infrastructure changes.
- A general-purpose coding assistant, arbitrary user prompts, unrestricted
  shell/network access, or arbitrary model-installed tools.
- Native email and vendor-specific chat-bot integrations; the MVP exposes a
  signed webhook for those external integrations.
- Native GitHub, GitLab, and Gitee pull/merge-request creation; their MVP path
  stops at a pushed branch plus available compare metadata.
- Guaranteeing a code fix when evidence is insufficient, the cause is outside
  the repository, or required validation cannot run safely.
