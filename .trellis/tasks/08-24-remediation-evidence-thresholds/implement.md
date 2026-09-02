# Implementation Plan: Remediation Evidence Thresholds

## Ordering And Dependencies

This task changes shared project configuration, public webhook ingress, evidence
persistence, remediation routing, trusted adapters, and production UI. Work in
the order below so old configurations remain readable and the service-owned
gate is not enabled before its evidence inputs exist.

Before editing any existing function, class, or method, run GitNexus
`impact(..., direction="upstream")` for the exact symbol. Warn before proceeding
when risk is HIGH or CRITICAL. Before commit, run
`detect_changes(scope="compare", base_ref="main")` and review every affected
process.

The current worktree contains unrelated user changes. Preserve them and scope
diffs/checks to this task's files and symbols.

## Step 1: Freeze Current Gaps With Tests

- [x] Add webhook tests proving signed webhooks currently have no provider
      dispatch and Tencent malformed payloads are accepted as opaque text.
- [x] Add remediation tests proving `code_fixable` currently enters planning
      regardless of confidence/direct evidence and that the first turn lacks
      the triggering Observation.
- [x] Add SSH/configuration tests proving the current source schema has no
      deployment/container identity and `ssh.inspect` rejects Docker.
- [x] Preserve existing generic webhook, semantic fingerprint, project
      authorization, credential isolation, budget, and run-state behavior.

## Step 2: Add Compatible Configuration Contracts

- [x] Run impact analysis for `ValidateSource`, `validateSourceConfig`,
      `ValidateTrigger`, `validateTriggerConfig`, source/trigger mapping, and
      configuration editor builders before editing.
- [x] Add signed-webhook schema v2 with provider `generic|tencent_cls`; decode v1
      as generic and write v2 on explicit save.
- [x] Add SSH source schema v2 with `deployment.kind=host|docker` and exact
      `containerName` required only for Docker; decode v1 as host deployment.
- [x] Extend configuration draft/read/review mapping without returning secrets.
- [x] Audit provider and deployment changes with bounded safe metadata.
- [x] Add domain, repository, HTTP, and frontend unit tests for v1 compatibility,
      strict v2 rejection, and round trips.

## Step 3: Add Evidence Persistence And Contracts

- [x] Define normalized alert context, operational evidence, provenance,
      connector observation, source coverage, time assessment, classification,
      evidence assessment, and gate-decision values in the owning domain.
- [x] Add forward/down migrations for project/incident/run-scoped evidence and
      the final evidence assessment, including bounded fields, ownership foreign
      keys, hashes, and useful incident/run indexes.
- [x] Add sqlc queries and repository methods for idempotent append/list/load by
      triggering Observation/incident/run.
- [x] Ensure complete operational payloads are stored without log-content
      redaction while credential/control fields are absent from the port value.
- [x] Add authorization tests proving only project members can read evidence and
      viewer/operator/admin write rules remain unchanged.
- [x] Add logging/audit tests proving evidence bodies, DetailUrl capability
      material, plaintext secrets, and tokens never enter logs/audits.

Rollback point: persistence is additive and unused until Steps 4–6 are wired.

## Step 4: Add Typed Tencent Callback And Detail Adapter

- [x] Run impact analysis for webhook lookup/handler/service normalization and
      incident/Observation ingestion symbols before editing.
- [x] Extend webhook lookup with the saved provider discriminator.
- [x] Keep generic webhook behavior unchanged.
- [x] Add strict bounded Tencent JSON-object decoding, require non-empty TopicId
      and DetailUrl, preserve the other expected top-level fields when present,
      and validate the expected HTTPS DetailUrl host.
- [x] Return stable `400 invalid_tencent_cls_callback` synchronously for invalid
      JSON, missing required fields, or invalid DetailUrl; do not start
      background work.
- [x] Normalize a valid Tencent aggregate callback as `anchor_only`, retaining
      raw fields and excluding TopicId/query-count values from semantic
      fingerprint inputs.
- [x] Implement the trusted no-login detail adapter with fixed action, bounded
      redirect policy, timeout, response bytes, content type, RecordId, and
      strict result validation.
- [x] Structurally separate operational evidence from credentials/control
      material before returning or persisting the adapter result.
- [x] Preserve every AnalysisInfo item, configuration, columns, raw/formatted
      result, and owning per-item error.
- [x] Compare callback/detail TopicId as evidence-only identity; persist mismatch
      as a contradiction and never require TopicId configuration.
- [x] Keep the accepted HTTP response 202 when asynchronous detail retrieval
      fails; persist a safe connector observation.
- [x] Add fixture tests for the representative payload/detail/time values,
      redirect rejection, schema drift, oversized body, timeout, credential
      isolation, partial analysis errors, and TopicId disagreement.

Rollback point: provider selection can be disabled/defaulted to generic without
changing existing inbound URLs or raw callback persistence.

## Step 5: Feed The Triggering Alert And Time Evidence Into Remediation

- [x] Run impact analysis for remediation context assembly, coordinator start,
      incident lookup, and agent prompt symbols before editing.
- [x] Load the triggering Observation, normalized alert, provider evidence, and
      source coverage into the metadata-only bootstrap without eager SSH/Git
      reads.
- [x] Derive a bounded incident window from provider query range/exact epoch or
      the sparse occurrence anchor while preserving every original time field.
- [x] Add the global time-reasoning prompt and structured time assessment; do
      not add Tencent/project time-zone configuration.
- [x] Ensure the representative UTC+8 display/UTC epoch case correlates by epoch
      and records unresolved string/epoch differences as contradictions.
- [x] Persist the normalized window, quality factors, time assessment, coverage,
      missing evidence, and contradictions.
- [x] Add tests for timestamp-only input, generic/test-like text, provider epoch
      precedence, unresolved clocks, and first-turn visibility of the original
      symptom.

## Step 6: Add SSH Docker Discovery Configuration

- [x] Run impact analysis for project configuration probe handlers/service,
      `ParseSSHSourceConfig`, SSH adapter execution, and SourceStep/UI builder
      symbols before editing.
- [x] Add an admin-only bounded container-probe application port and HTTP route
      using draft host/port/user plus a same-project SSH credential reference.
- [x] Execute only the fixed read-only inventory operation; parse structured
      name/ID/image/state/status output and return at most 100 entries with
      running entries first.
- [x] Include stopped and restarting containers and an explicit refresh path.
- [x] Add deployment mode control and a native container select to the SSH
      source UI. Do not render a free-form container input or raw command/output.
- [x] Clear stale container options/selection when host, port, user, credential,
      or deployment mode changes; block Docker save until a returned container
      is selected.
- [x] Show deployment mode and exact container name in review/overview.
- [x] Test authorization, wrong-project credentials, unavailable Docker/socket,
      output bounds, duplicate/malformed rows, selection reset, responsive UI,
      and absence of credentials from browser state/rendered text.

## Step 7: Add Typed Runtime Docker Evidence

- [x] Add a credential-free Docker evidence port separate from `SSHInspectPort`;
      do not expose a generic Docker command.
- [x] Resolve only the configured exact name to a current ID and bounded
      read-only identity metadata.
- [x] Register a model-visible Docker log tool only for enabled SSH sources with
      saved Docker deployment. Do not accept a container name/ID parameter.
- [x] Validate mandatory since/until/tail against the incident/provider window,
      maximum expansion, line count, byte budget, and timeout before SSH.
- [x] Execute only fixed `docker ps`/minimum inspect/`docker logs` shapes;
      reject exec, attach, copy, lifecycle, mutation, arbitrary flags, and
      unregistered operations before remote execution.
- [x] Record success, empty, unavailable, missing container, permission denied,
      truncated, and host-identity-unverified coverage outcomes.
- [x] Test the rr case: application file lacks the fault, selected container
      stderr contains the correlated nil-pointer stack, and unrelated warnings
      remain non-causal.
- [x] Test recreated same-name resolution, missing exact name, no similarly
      named fallback, no all-container sweep, alert-IP mismatch, and every
      prohibited Docker operation.

Rollback point: disable Docker tool registration; saved deployment metadata is
backward-compatible and no mutation capability exists.

## Step 8: Enforce The Service-Owned Evidence Gate

- [x] Run impact analysis for `validateDiagnosis`, `routeDiagnosis`, planning
      transition, decision persistence, review mapping, and notification mapping
      before editing. Warn if the shared coordinator path is HIGH/CRITICAL.
- [x] Extend the diagnosis schema with typed evidence citations,
      classifications, coverage, time assessment, correlation, causal closure,
      material contradictions, test markers, and non-actionable hypotheses.
- [x] Resolve citations against persisted run/project evidence; reject invented,
      cross-run, or cross-project references.
- [x] Compute confidence caps in the application service: missing direct fault
      evidence `0.39`; unresolved host/time/primary source/material conflict
      `0.69`; otherwise `1.00`.
- [x] Require effective confidence at least `0.70`, direct fault evidence,
      temporal/operational correlation, primary-source coverage or direct
      bridge, no material contradiction, and causal closure before planning.
- [x] Override unsupported `code_fixable` to `insufficient_evidence`, persist the
      gate reasons, and prevent every transition to plan-candidate generation.
- [x] Permit bounded hypotheses only with `non_actionable=true`; prevent their
      evidence references from entering repair plans.
- [x] Treat test-like text as `test_suspected`; suppress remediation only for an
      auditable structured test policy match.
- [x] Add table-driven tests for every individual gate, combined caps, model
      overconfidence, exhausted collection, code-only hypotheses, conflicting
      evidence, and valid high-confidence causal closure.

## Step 9: Complete Operator Surfaces

- [x] Add Generic/Tencent provider selection and Tencent-specific callback/
      detail explanation to Trigger configuration; do not add TopicId or time-
      zone inputs.
- [x] Render provider labels in configuration review/overview.
- [x] Add project-authorized evidence/review projections for original alert,
      raw operational evidence, time interpretation, source coverage,
      classifications, citations, contradictions, confidence cap, gate outcome,
      test marker, hypotheses, and instrumentation/manual next steps.
- [x] Clearly mark hypotheses `Non-actionable` and hide/disable fix-planning
      actions for `insufficient_evidence`.
- [x] Verify long logs/paths/timestamps fit on desktop and mobile without
      overlap; use native semantic controls and established status components.
- [x] Add component and Playwright coverage for provider/deployment selection,
      container refresh/error/empty states, evidence-insufficient review, and
      complete operator evidence rendering.

## Step 10: Cross-Layer Verification And Rollout Review

- [x] Run targeted backend tests while iterating, then `make check` from
      `backend/`.
- [x] Run generated-code consistency after migration/query changes with the
      pinned local `sqlc v1.31.1`; the `make generate-check` wrapper remains
      blocked in this environment because `backend/tools/go.mod` requests
      Go 1.26.0 and the toolchain download is unavailable. A second direct
      generation produced the same hashes.
- [x] Run frontend `npm run lint`, `npm run typecheck`, `npm test`,
      `npm run build`, and relevant `npm run test:e2e` cases.
- [x] Run fixture-based end-to-end cases for timestamp-only, representative
      Tencent nil-pointer detail, mixed time zones, test-like title with real
      panic, Docker-only stack, incomplete sources, DetailUrl failure, and
      credential-bearing provider response.
- [x] Verify generic signed webhooks and v1 SSH sources retain existing behavior.
- [x] Inspect application logs/audits/UI/model fixtures for leaked credentials,
      capability tokens, DetailUrl authority material, or raw evidence in
      ordinary logs.
- [x] Run GitNexus `detect_changes(scope="compare", base_ref="main")`; review
      affected routes, symbols, and execution flows against this design.
- [x] Run `trellis-check`, update affected `.trellis/spec/` contracts, and only
      then prepare the task for commit/archive.

## Expected High-Risk Areas

- webhook token lookup and shared public ingress;
- strict project source/trigger configuration decoders and migrations;
- remediation coordinator diagnosis-to-planning transition;
- evidence persistence and project authorization;
- SSH command execution/credential loading;
- configuration editor state shared across source/trigger steps.

Any HIGH or CRITICAL impact result pauses edits long enough to report the blast
radius and adjust the implementation/test slice before proceeding.
