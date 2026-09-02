# Implementation Plan: Production Incident MVP

## Delivery Shape

This task is a parent-level product plan. Implementation should be split into
independently verifiable child tasks before code begins:

1. Bootstrap self-hosted service, database, authentication, role checks, and
   encrypted secret storage.
2. Build the shared project/source/observation/error-group/incident domain and
   retention jobs.
3. Add signed inbound webhook and CLS MCP connector, including a fixture-based
   historical replay mode.
4. Add evidence collection, deterministic reports, notification policies, and
   signed outbound webhooks.
5. Add AI remediation packages and the Yunxiao hotfix/draft-PR workflow.
6. Build the internal web console and REST API.
7. Add the generic HTTP JSON connector framework and SSH configuration schema.

Each child must reference this PRD and design and must be activated only after
its own acceptance criteria are reviewed.

## Ordered Implementation Checklist

- [ ] Establish the selected Go API/worker module and React/TypeScript console,
      then create a Docker Compose baseline with application, worker,
      PostgreSQL, Redis, RabbitMQ, Docker Secrets, persistent volumes, and
      process-specific health checks.
- [ ] Implement the RabbitMQ durable job runtime with PostgreSQL transactional
      outbox publication, publisher confirms/mandatory routing, manual-ack
      consumers, idempotent handlers, bounded retry/dead-letter behavior,
      connection recovery, tracing, metrics, and graceful draining.
- [ ] Implement schema migrations for users, roles, projects, environments,
      sources, encrypted secrets, observations, error groups, incidents,
      evidence, policies, audit events, and retention settings.
- [ ] Implement bootstrap administration, local session/API authentication,
      role authorization, audit middleware, and never-return-secret APIs.
- [ ] Define versioned observation/evidence/connector contracts and fixture
      inputs from the documented CLS log examples.
- [ ] Implement deterministic normalization and fingerprinting with tests that
      prove variable request IDs and timestamps group correctly but distinct
      source locations/messages do not collapse incorrectly.
- [ ] Implement error stream persistence, incident creation/reopening/recovery,
      operator closure, policy resolution, and retention cleanup.
- [ ] Implement signed inbound webhook ingestion with replay protection and a
      source mapping UI/API.
- [ ] Implement read-only CLS MCP search/context/alarm/metric collection and
      checkpointed polling. Test with an MCP fake before customer credentials.
- [ ] Implement deterministic evidence orchestration from explicit incident
      correlators and configured runbooks; record every collection request and
      result with provenance.
- [ ] Implement deterministic report generation and the optional remote-LLM
      opt-in/redaction/audit boundary using the `LLMProvider` fake. Validate
      any LLM-proposed follow-up collection request against configured
      read-only connector capabilities; the model has no direct connector or
      secret access.
- [ ] Add real Anthropic Claude, OpenAI, and OpenAI-compatible adapters behind
      the typed Go provider contract. Test with local HTTP fakes and keep each
      provider's credentials, retries, and transport differences outside core
      evidence behavior.
- [ ] Implement the durable agentic-remediation harness: trigger/coalescing,
      evidence/repository context, typed tool policy, diagnosis and plan
      selection, exact-deployed-commit workspace, approved validation commands,
      bounded repair iterations, budgets, cancellation, and audit records.
- [ ] Implement remediation packages with cited evidence, selected-plan
      rationale, tests, risk/rollback, approval state, and verification
      criteria. Add provider-neutral Git branch/commit/push publication and the
      least-privilege Yunxiao draft-PR adapter.
- [ ] Implement outbound signed webhooks with retry, idempotency keys, policy
      routing, and delivery audit records.
- [ ] Implement the web console: login, incident list/detail/event stream,
      connector/policy management, outcomes, audit view, and role states.
      Include provider configuration metadata and project LLM opt-in after the
      provider adapters are available.
- [ ] Implement generic HTTP JSON connector contracts and validation. Add SSH
      configuration schema only; no SSH executor.
- [ ] Run a historical CLS replay for `real-estate`, compare groups and reports
      against known incidents, then enable outbound notifications in a test
      target before production notification routing.

## Validation Gates

- Unit tests cover normalization, grouping, policy evaluation, lifecycle,
  redaction, role checks, encryption round trips, retention, and webhook HMAC.
- Contract tests cover the shared connector interface using webhook, MCP fake,
  and HTTP JSON fixture implementations.
- Integration tests cover the full path from observation to incident, evidence,
  report, recovery, re-open, outbound notification, and audit event.
- Integration tests interrupt RabbitMQ and the worker between outbox commit,
  publish confirm, handler commit, and acknowledgement, proving eventual
  at-least-once handling without duplicate durable effects.
- Integration tests cover a remediation package through baseline resolution,
  isolated patch/test execution, provider-neutral hotfix branch push, and draft
  PR creation using a Yunxiao fake. They prove model/tool credentials, merge,
  deployment, and production writes are unavailable.
- Integration tests prove that evidence collection and report facts work with
  remote LLM analysis disabled, and that unsupported or unapproved LLM
  collection proposals cannot invoke a connector.
- Browser tests cover role-restricted console workflows and secret non-display.
- Compose smoke tests start a clean deployment, bootstrap admin, create a
  project/source, ingest a fixture, and verify no outbound network path is used
  when remote LLM and outbound notification are disabled.
- Before a customer deployment, validate recovery of database backup, rotation
  of connector/webhook secrets, and disabling a connector without data loss.

## Rollback Points

- Disable a source to stop pulling or accepting observations.
- Disable a notification policy to stop outbound webhooks.
- Disable remote LLM at project level; no evidence is sent after the change.
- Disable automatic remediation globally or per project to stop new harness
  runs while preserving existing diagnoses, packages, and pushed branches.
- Pause RabbitMQ consumption to stop new asynchronous execution while retaining
  PostgreSQL outbox/job state for controlled resume or dead-letter replay.
- Migrations require backward-compatible steps and a tested database backup
  before upgrade. Never delete source evidence during ordinary rollback.
