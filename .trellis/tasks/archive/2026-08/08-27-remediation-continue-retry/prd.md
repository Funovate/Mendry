# Remediation Continue And Retry Flow

## Goal

Allow a remediation investigation to progress after an earlier attempt stopped, without pretending that a terminal run can be restarted in place. A repeated webhook may open a bounded automatic continuation opportunity, and an authorized operator may explicitly continue from the incident page. Each continuation is a new, linked attempt in the same incident lifecycle series, while the prior diagnosis and evidence trail remains inspectable.

## User Value

- A webhook that reports the same still-open fault can add fresh evidence and give a failed remediation a bounded opportunity to continue.
- An operator can recover from a Fixthe-side failure or newly deployed Fixthe fix from the incident page without reopening the incident or waiting for the monitored project to deploy a different commit.
- The harness can reason from prior analysis, rather than discarding the useful diagnosis, plan, and collection history on every retry.

## Confirmed Facts

- The remediation series identity is `(incident_id, lifecycle_generation, deployed_commit)`, and `remediation_run` already enforces unique `(series_id, attempt_number)` (`backend/migrations/000006_remediation_persistence.up.sql:15-34`).
- `Trigger.Start` currently creates or reuses only the root run and `RemediationCoordinator.Start` drives only a `queued` run (`backend/internal/modules/remediation/application/trigger.go:64-93`, `backend/internal/modules/remediation/application/coordinator.go:255-263`). A terminal root therefore cannot be retried by the existing start path.
- The manual endpoint is named `/remediation/start` and currently calls the same root-start path after checking project incident-write access and lifecycle generation (`backend/internal/modules/remediation/adapter/http/handler.go:48-63`, `backend/internal/modules/remediation/application/service.go:63-90`). The incident page currently reads the review only and has no remediation mutation control (`frontend/src/features/incidents/RemediationPanel.tsx:6-69`).
- Webhook ingress validates the token/provider/body synchronously, returns `202`, and performs normalization, observation persistence, evidence persistence, and incident ingestion in detached background work (`backend/internal/modules/hooks/application/service.go:132-159`).
- A new webhook incident creates a root remediation record transactionally with the incident, but an existing open fingerprint only records an occurrence and does not emit remediation again (`backend/internal/modules/incidents/application/service.go:250-290`, `backend/internal/modules/incidents/adapter/postgres/repository.go:52-103`).
- Existing durable records can supply a bounded continuation brief plus the persisted operational evidence payloads used for analysis. Those evidence payloads are loaded into the trusted model context without field deletion, value replacement, or review/log redaction. Provider prompts/responses, connector credentials injected by adapters, and control-plane configuration are not evidence and are neither persisted nor exposed through retry metadata or the page.
- The current workspace contains unrelated uncommitted changes from prior remediation and configuration work. This task must preserve them and limit edits to the continue/retry behavior and its direct contracts.

## Requirements

### R1. Webhook Automatic Gate

- Keep synchronous webhook acceptance limited to valid token/provider/body checks; the webhook response remains `202` and never waits for remediation.
- For a new or reopened qualifying `P1/P2` incident, retain exactly one automatic root attempt for the current series key.
- For a repeated observation of an existing open qualifying incident, evaluate the current series/run through a remediation-owned automatic gate after the incident/evidence write has succeeded.
- The gate must never create a second root run for the same series key, must not create more than one active attempt for a series, and must enforce a bounded automatic-attempt ceiling.
- The gate must record why it continued, skipped, or reached its ceiling using safe metadata; duplicate or concurrent webhook deliveries must converge on the same durable attempt state.
- Automatic continuation must not be triggered by `Info`, by an incident in a different lifecycle generation, by a changed baseline without a new series key, or by a run already awaiting human review/completed with a usable result.
- Automatic continuation must not use a webhook payload as a command, a retry authorization token, or a source of credentials.

### R2. Page Manual Continue/Retry

- Add a protected, project-scoped endpoint for an authorized `admin` or `operator` to continue the current incident remediation series from the page. Keep the existing initial-start behavior available for incidents with no remediation series, but make retry/continue a distinct operation in the contract.
- Require the current lifecycle generation and an expected latest attempt identity/version so a stale page cannot create an attempt for a reopened incident or overwrite a newer operator action.
- Allow an explicit continuation only from a retryable terminal outcome and from a bounded set of review-blocked outcomes where additional evidence or a Fixthe fix can reasonably change the result. Do not retry a ready-for-review success, completed non-code result, or an already active attempt.
- Return the newly created attempt identity and status. A successful request must represent a new monotonically numbered attempt, not the previous terminal run.
- Render the control only when server-provided project capabilities and the current remediation state allow it. Show pending, conflict, forbidden, failure, and success states without losing the existing review chain.
- Repeated clicks must be idempotent or return a stable conflict; they must not create two next attempts for one expected predecessor.

### R3. Continue Semantics

- A continuation is a new attempt linked to the previous attempt inside the same series. It preserves the prior attempts and does not mutate their terminal state or counters.
- Before the next model turn, provide a bounded, clearly labeled continuation brief containing prior structured conclusions, cited evidence identifiers, plan/diff summary, tool outcomes, terminal reason, and the reason for continuation. Load the persisted operational evidence payloads separately and preserve their original fields and values in the trusted model context. Prior conclusions remain bounded inputs, but a durable `code_fixable` decision records that the service-owned evidence gate already admitted planning.
- Resume from the latest durably completed phase: if the predecessor's latest persisted decision is `code_fixable` and the attempt stopped during planning, start the child in planning without re-running provider/log diagnosis. Other manual continuations restart model diagnosis against the persisted evidence snapshot without refreshing external evidence.
- A manual page continuation is analysis-only for the current context version. It must not call Tencent detail, Docker logs, SSH inspect, evidence search/context, dynamic MCP/source tools, or another external evidence collector. Repository read-only tools remain available for code analysis. A repeated webhook may supply a newer persisted context before an automatic continuation.
- Do not run persisted operational evidence through review/log sanitizers before model analysis and do not delete evidence fields such as provider detail URLs. Connector credentials, provider SDK state, model messages, and control-plane capabilities remain outside evidence and outside model/page/log payloads.
- A new attempt receives a fresh per-attempt budget and records the input/configuration snapshot used to continue. The series-level attempt ceiling and automatic/manual origin remain auditable.
- If the prior attempt stopped for insufficient evidence, a later automatic continuation may use newly persisted webhook evidence and overturn the prior conclusion. A page retry analyzes the current durable snapshot as-is; refreshing evidence is a separate explicit workflow. Policy rejection, invalid configuration, authorization failure, or a completed review result must not be silently retried.
- A continuation must use the same exact deployed commit for the existing series. A changed monitored-project commit or reopened incident creates a separate series through the existing lifecycle rules.

### R4. Durable Attempt And Audit Contract

- Persist attempt linkage, origin (`automatic` or `manual`), bounded continuation reason, retryability/eligibility, and the expected predecessor/version needed for concurrency control.
- Persist only safe terminal reason and classification metadata; do not persist raw webhook bodies, model prompts/responses, credentials, authenticated URLs, or unrestricted evidence as retry metadata.
- Expose the current attempt and enough bounded attempt history for the review page to explain whether the result is a root, automatic continuation, or manual continuation.
- Preserve project and incident authorization on every read/write path. An unknown project, non-member, stale generation, cross-incident run, or unauthorized user must not reveal or create an attempt.

### R5. Compatibility And Failure Behavior

- Existing webhook ingestion, fingerprint coalescing, lifecycle-generation rules, exact deployed-commit identity, evidence ownership, and terminal state names remain compatible.
- Existing root-trigger calls remain safe when the series has no prior run; repeated root calls remain idempotent.
- If continuation creation or background driving fails, the prior attempt remains inspectable and the failure is reported through the existing safe server diagnostic path. A failed automatic continuation must not make the webhook return a non-2xx after acceptance.
- No queue, RabbitMQ worker, arbitrary shell retry, automatic merge/deploy, or production rollback is introduced by this task. The current in-process execution remains bounded; durable worker resume remains a later service-foundation integration.

## Acceptance Criteria

- [ ] A new qualifying webhook creates exactly one queued/root attempt for its series, including concurrent duplicate delivery.
- [ ] A repeated open-fingerprint webhook persists its observation/evidence and passes the automatic gate; a retry-eligible terminal attempt creates at most one linked next attempt, while an active, successful, non-qualifying, or ceiling-reached series does not create one.
- [ ] Automatic continuation is bounded, safe under concurrent webhook deliveries, and records an auditable skip/continue reason without exposing payloads or credentials.
- [ ] An authorized operator can use the incident page to continue a retry-eligible terminal attempt; the API returns attempt N+1 and the page refreshes to that attempt/review state.
- [ ] Viewer/non-member users cannot invoke the mutation; stale generation, stale expected attempt/version, duplicate click, active run, and unsupported terminal state return stable safe errors and create no extra attempt.
- [ ] Attempt N+1 preserves attempt N, carries a bounded continuation brief, and receives the persisted operational evidence with original fields and values intact in trusted model context; raw model prompts/responses and connector credentials are absent.
- [ ] A manual page retry performs no external evidence collection or dynamic source discovery and exposes only repository read tools to the analysis model.
- [ ] New evidence can change the continuation diagnosis, while a continuation keeps the same incident lifecycle and deployed-commit series key.
- [ ] PostgreSQL and in-process fakes prove unique attempt numbering, predecessor/version concurrency, idempotency, automatic-attempt ceiling, and transaction rollback behavior.
- [ ] Backend and frontend contracts validate the new response fields, project-scoped query/mutation behavior, loading/error/conflict states, and no-secret rendering.
- [ ] Existing remediation, webhook, incident lifecycle, evidence, budget, authorization, and observability tests remain green.

## Resolved Product Decisions

- Repeated webhook continuation is bounded: it requires committed new evidence, a retryable terminal latest attempt, no active attempt, and fewer than 3 automatic continuations in the series. `blocked_manual_review` and non-retryable failures remain manual-only.
- Manual page continuation is available for `failed`, `budget_exhausted`, and `blocked_manual_review` outcomes. It requires current generation plus an expected latest attempt/version. `diagnosis_ready_for_review` and `completed_non_code` are not rerun because they already have a usable business result.
- Manual page continuation is analysis-only over the current persisted context version. It does not refresh external evidence; a future explicit refresh action would be a separate contract.
- Automatic and manual continuation share the same next-attempt creation path, but their drive modes differ: automatic continuation may analyze newly persisted webhook context, while manual continuation disables external evidence/source tools. Both obey the same active-attempt, generation, optimistic-concurrency, and budget checks.

## Technical Notes

- “Continue” means a new linked attempt with a bounded structured continuation brief, not mutation of a terminal run and not replay of raw provider conversation history. Persisted operational evidence is loaded separately into trusted model context without redaction or field deletion; the brief contains prior decisions, plans, evidence IDs, tool outcomes, and terminal reason.
- The exact deployed commit and lifecycle generation stay fixed within a series. Reopen or a changed monitored-project baseline follows existing rules and creates a new series.
- Manual and automatic continuation share the same attempt-creation and coordinator path; only authorization and gate origin differ.

## Out Of Scope

- Full RabbitMQ/outbox delivery, worker restart resume, dead-letter replay, or cross-process lease recovery.
- Resuming an interrupted model turn from raw provider conversation history; only the bounded structured continuation brief is supported here.
- Automatic retry of policy, authorization, invalid-configuration, or human-gated outcomes.
- New production mutation, Git publication, merge, deployment, rollback, or notification delivery channel.
- Reopening or closing incidents as a side effect of remediation continuation.
