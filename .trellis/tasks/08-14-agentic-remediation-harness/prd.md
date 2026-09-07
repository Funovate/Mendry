# Agentic Remediation Harness

## Goal

Finish the controlled production integration that turns an existing
`diagnosis_ready_for_review` result into an isolated, validated, published
change that stops at `awaiting_human_review`.

The diagnosis and lifecycle kernels already exist. This parent task now owns
only the remaining delivery map, cross-child contracts, and final integration
review. Implementation must be delivered through focused child tasks rather
than by treating this umbrella as one code change.

## Current Baseline

The following capabilities are implemented on `main` and are not remaining
scope:

- Incident-triggered remediation series and linked attempts, pinned to lifecycle
  generation and deployed commit.
- Real Git read, SSH/Docker evidence, Tencent CLS detail, OpenAI, dynamic MCP,
  evidence re-read, and tool-policy paths.
- Structured diagnosis, evidence gate, candidate plans, suggested diffs,
  corrective tool retries, bounded conversation, durable checkpoints,
  exhaustion validation, and recovery projections.
- The `ApplyPlan` / `ResumeLifecycle` application kernel for
  `patching -> validating -> publishing -> awaiting_human_review`.
- Durable PostgreSQL lifecycle effects with stable idempotency keys and tests
  for patch revision, validation failure, publication retry, process restart,
  and the mandatory human-review publication result.
- Protected start, retry, and review APIs plus the remediation detail UI.

The real production gap is integration:

- No application use case or HTTP endpoint admits an operator-selected plan.
- The frontend cannot approve and apply a plan.
- Project configuration stores agent-loop mode but not executable validation,
  repair risk, artifact, or publication policy.
- `WorkspacePort`, `ValidationPort`, and `PublicationPort` have no production
  implementations.
- Bootstrap does not inject lifecycle ports, the existing PostgreSQL
  `LifecycleStore`, validation command snapshots, publication policy, or a plan
  policy evaluator into the coordinator.
- Review projection does not expose validation/publication results needed for
  the final human handoff.

## Ownership And Task Map

This task remains a child of `08-10-production-incident-mvp`. It is an umbrella
and should not be activated for broad direct implementation.

### Delivered Children

- `08-14-remediation-walking-skeleton`
- `08-21-ssh-inspect-tool`
- `08-24-remediation-prompt-cache`
- `08-26-docker-logs-pattern-filter`
- `08-28-expand-ssh-readonly-diagnostics`
- `08-28-model-tool-corrective-retry`
- `08-31-remediation-lifecycle-resilience`

### Existing Children Requiring Separate Disposition

- `08-21-remediation-tool-driven-context`: its core conversation, tool catalog,
  SSH inspect, MCP, persistence, and recovery work has landed through later
  tasks. Audit and archive the stale original as superseded; preserve only
  separately justified generic HTTP JSON log work.
- `08-14-stdio-mcp-harness-config`: optional extensibility, not a blocker for
  production repair. Rewrite around current remote MCP/runtime contracts and
  lower its priority before implementation.

### Remaining Delivery Children To Create After Review

1. **Execution policy configuration**: approved validation commands, risk
   policy, artifact retention, publication target/namespace, and scoped write
   credential configuration.
2. **Lifecycle runtime adapters**: production workspace, validation, artifact,
   and Git/SCM publication implementations behind the existing ports.
3. **Apply-plan API and UI**: authorized durable plan admission, background or
   resumable execution, review projection, and operator controls.
4. **Controlled production pilot**: end-to-end execution against a designated
   non-production target and final parent acceptance review.

## Requirements

### R1. Authorized Plan Admission

- Add a protected application use case and HTTP endpoint for an `admin` or
  `operator` to select a plan from the latest remediation run.
- The request must include project key, incident number, lifecycle generation,
  run ID, expected run version, and plan ID.
- Admission must verify project ownership, current incident generation and
  deployed commit, latest run identity/version, `resilient_v1` mode,
  `diagnosis_ready_for_review` state, plan ownership, and policy eligibility.
- Stale, cross-project, cross-run, terminal, unsupported, or duplicate requests
  must fail safely or return the already-admitted state without creating a
  second external effect.
- Approval is not model authority. The user selects a plan; deterministic
  service policy decides whether it may enter patching.

### R2. Durable Execution Policy

- Project administrators configure immutable, versioned validation command
  definitions. Each definition uses stored argv and execution policy; the model
  can request only an approved command ID and cannot provide shell text.
- Project policy defines ordinary, high-risk opt-in, and denied mutation
  classes; required validation IDs; diff/file/iteration bounds; artifact
  retention; publication target branch; restricted branch namespace; and a
  separately scoped write credential.
- A plan application snapshots all policy and command versions before the first
  workspace effect. Later configuration changes do not alter an active run.
- Missing commands, credentials, target policy, or artifact storage make plan
  admission unavailable with a stable configuration error. They must not fall
  back to unsafe defaults.

### R3. Isolated Workspace, Patch, Validation, And Artifacts

- Implement `WorkspacePort` against the exact deployed commit in a disposable,
  run-owned workspace. It must enforce run/baseline identity, path and diff
  bounds, symlink/submodule/hook restrictions, stable idempotency, and explicit
  cleanup.
- Validation runs only administrator-approved argv in a dedicated isolation
  boundary with no model, evidence, production, or Git-push credentials; no
  host container-engine socket; deny-by-default network; and bounded CPU,
  memory, process, time, and output.
- Full patches and bounded validation outputs are stored in an
  application-owned content-addressed artifact store. PostgreSQL stores only
  ownership, hashes, safe summaries, and references.
- A failed validation may return to a bounded patch revision. Publication is
  impossible until the service has a fresh passing result for the exact tree
  hash.

### R4. Trusted Publication

- Implement `PublicationPort` using a fresh checkout of the recorded deployed
  commit, the content-addressed patch, and a separately scoped Git/SCM write
  credential unavailable to the model and validation runtime.
- Reapply the patch and verify the expected resulting tree hash before commit or
  push. Revalidate remote identity, target branch, restricted branch namespace,
  and source ancestry immediately before the write.
- The first supported production pilot may target the configured Yunxiao
  repository. Provider-neutral branch/commit/push remains the contract; native
  draft change-request creation for every SCM provider is not required here.
- Publication is idempotent across retries and restarts. A successful result
  always declares `HumanReviewRequired=true` and ends at
  `awaiting_human_review`.
- No adapter or API may merge, deploy, modify production configuration, execute
  a production rollback, or claim incident recovery.

### R5. Production Composition And Recovery

- Bootstrap injects the existing PostgreSQL `LifecycleStore`, concrete
  workspace/validation/publication ports, plan-policy evaluator, validation
  command snapshot loader, and publication policy loader.
- Plan admission is durable before long-running effects begin. The HTTP request
  must not be the only owner of progress: cancellation or process restart leaves
  an inspectable active phase that an authorized repeated request or startup
  resumer can continue through `ResumeLifecycle`.
- Successful lifecycle effects are never repeated. Recoverable failures retain
  the current phase and stable safe error; policy blockers and exhausted paths
  produce an inspectable manual-review result.
- The implementation remains in-process. RabbitMQ, PostgreSQL job outbox, and a
  separate worker are not prerequisites and must not be reintroduced by this
  task.

### R6. Review API And Operator UI

- The review API exposes selected plan, active lifecycle phase, safe effect
  summaries, changed files, validation command/version/result, artifact
  references, publication branch/commit/draft/compare metadata, and final human
  next action without raw command output, credentials, or unrestricted diffs.
- The incident UI allows an authorized user to select an eligible plan, review
  risk/rollback/affected files, confirm application, and follow progress.
- Controls are disabled for viewers, stale versions, unsupported modes,
  ineligible plans, active duplicate requests, and terminal runs.
- Recoverable execution is shown as active recovery. Manual intervention appears
  only for a policy blocker, validated exhaustion, or final human merge review.
- The terminal UI links to the published change when available and states that
  merge and deployment remain manual.

### R7. Integration Proof And Rollout

- Fake and integration tests cover admission through
  `awaiting_human_review`, including patch revision, validation failure,
  publication retry, request cancellation, process restart, stale version,
  duplicate admission, policy denial, and artifact/tree-hash mismatch.
- Run one controlled pilot against a designated non-production repository or
  branch with approved commands and scoped credentials. It must produce one
  review-ready change and no production mutation.
- Keep execution disabled by default. Enable it per project only after
  configuration validation and the controlled pilot pass.

## Acceptance Criteria

- [ ] The remaining work is split into focused child tasks with non-overlapping
      ownership; this parent is used only for integration review.
- [ ] An authorized plan-application request is project-scoped, optimistic,
      idempotent, durable before long-running work, and rejects stale or
      cross-run input before any workspace/publication effect.
- [ ] Versioned project policy provides approved command IDs, risk rules,
      artifact policy, publication target/namespace, and a scoped write
      credential; every value is snapshotted for the run.
- [ ] Production implementations satisfy `WorkspacePort`, `ValidationPort`, and
      `PublicationPort` without changing their existing signatures.
- [ ] Validation executes approved argv only in the isolation boundary and
      cannot access model/evidence/production/push credentials, host paths,
      unrestricted network, or a container-engine socket.
- [ ] Publication replays a content-addressed patch from the exact deployed
      commit, verifies the expected tree hash, writes only to the restricted
      branch namespace, and is idempotent across retry/restart.
- [ ] The complete path reaches `awaiting_human_review` with one branch/commit
      and optional draft/compare metadata, and no code path can merge, deploy,
      or claim recovery.
- [ ] API/UI expose plan approval, active execution/recovery, validation, and
      publication handoff while preserving RBAC and secret-free responses.
- [ ] Failure tests prove validation failure, policy denial, artifact mismatch,
      stale admission, process restart, and publication retry create no
      duplicate or unauthorized external effect.
- [ ] A controlled non-production pilot passes and its written read-out records
      the final go/no-go for per-project execution enablement.

## Out Of Scope

- Automatic merge, deployment, production rollback, production shell, database
  mutation, infrastructure changes, or claims of incident recovery.
- RabbitMQ, a transactional job outbox, a separate remediation worker, or
  dead-letter queues.
- Reworking diagnosis, EvidenceGate, agent conversation, tool retry,
  checkpoints, evidence re-read, or lifecycle state names already delivered.
- Generic HTTP JSON log ingestion, stdio MCP configuration/runtime, and broad
  connector expansion.
- Native pull/merge-request creation for every SCM provider.
- Native email/chat notification delivery or a general outbound-notification
  platform.
