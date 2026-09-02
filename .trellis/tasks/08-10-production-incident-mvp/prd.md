# Production Incident MVP

## Goal

Deliver a customer-managed production-incident service that progresses from
evidence-backed detection and diagnosis to AI-assisted, human-governed code
remediation. It preserves every occurrence in a project event stream, explains
the evidence behind its diagnosis and proposed fix, and can create an auditable
hotfix branch and PR from the verified production baseline.

## Background

- The monitored production deployment is Tencent Cloud Frankfurt. Backend logs
  are in CLS; PostgreSQL has cloud monitoring. Monitored backend services,
  Nacos, Redis, and RabbitMQ run in Docker. Source code and delivery pipelines
  are in Yunxiao. The incident service uses separate local PostgreSQL, Redis,
  and RabbitMQ instances and never treats monitored middleware as its own.
- Frontend error telemetry does not yet exist. Cloudflare R2 hosts frontend
  assets. Frontend discovery is outside this MVP.
- The first release is a single-tenant, customer-environment Docker Compose
  deployment on a dedicated operations CVM in the customer's VPC. It must not
  share the production business host.
- Raw production logs and credentials remain inside the customer environment.
  Evidence processing is local by default. Remote LLM analysis is customer
  opt-in and receives only minimized, redacted evidence summaries.
- Existing backend JSON-like logs may omit service identity or request IDs, may
  misuse `ERROR`, and may preserve only terminal middleware errors.

## Product Model

- A project, for example `real-estate`, is the primary boundary for ownership,
  source configuration, incidents, retention, and audit records.
- Every source and incident belongs to one project and environment. Service is
  an optional validated label; missing service identity groups at project and
  source level.
- Every successfully ingested error observation is persisted and visible in a
  project event stream. Matching a prior fingerprint never hides or discards an
  occurrence.
- A new error fingerprint creates an `Info` incident by default. Repeated
  occurrences append to that incident. User rules control priority, muting,
  and outbound notification.
- An incident becomes `Recovered` after a configurable quiet period or an
  explicit upstream recovery event. Only an operator can mark it `Closed`.
  Recurrence automatically reopens the historical incident.
- A remediation package belongs to an incident and records diagnosis evidence,
  proposed change, tests, risk, rollback, review decision, and verification.
  It can create a restricted `hotfix/*` branch from the exact production
  baseline, but it cannot merge, deploy, or directly modify production.

## Delivery Map

This parent task owns the product contract and integration review; all code is
delivered through the following independently verifiable child tasks.

| Child task | Deliverable | Depends on |
|---|---|---|
| `08-10-incident-console-prototype` | Reviewable incident-console prototype with representative local data | None; user prototype approval |
| `08-10-incident-service-foundation` | Docker Compose baseline, PostgreSQL/Redis/RabbitMQ, local authentication/RBAC, encrypted secrets | Console prototype approval |
| `08-10-incident-domain-lifecycle` | Core data model, grouping, lifecycle, retention | Service foundation |
| `08-10-incident-ingestion-cls` | Signed webhook ingestion and read-only CLS MCP connector | Service foundation; domain and lifecycle |
| `08-10-incident-evidence-notifications` | Evidence orchestration, reports, LLM boundary, outbound webhooks | Domain and lifecycle; ingestion and CLS |
| `08-10-incident-llm-providers` | Claude, OpenAI, and OpenAI-compatible LLM Provider adapters | Service foundation; evidence and notifications |
| `08-14-agentic-remediation-harness` | Durable diagnosis/repair agent loop, tool policy, isolated workspace, plan selection, and validation | Service foundation; domain and lifecycle; evidence and notifications; LLM providers |
| `08-10-incident-remediation-changes` | Remediation package, provider-neutral Git publication, and Yunxiao draft-PR workflow | Service foundation; agentic remediation harness |
| `08-10-incident-console-api` | Protected REST API and internal web console | Console prototype approval; service foundation; domain and lifecycle; ingestion and CLS; evidence and notifications; LLM providers; agentic remediation harness; remediation changes |
| `08-10-incident-connector-framework` | Generic HTTP JSON connector and SSH configuration schema | Service foundation; domain and lifecycle; ingestion and CLS |

Each child PRD repeats its dependencies and acceptance criteria. Completion
requires an integration review against every acceptance criterion in this PRD.

## Functional Requirements

- R1: Normalize observations into timestamp, normalized level, message,
  source location, host, request ID, project/environment/service identity, and
  a raw-log reference.
- R2: Compute stable error fingerprints and aggregate occurrences into error
  groups and incidents without using the fingerprint as an ingestion filter.
- R3: Collect contextual CLS evidence for an incident: log context,
  request-ID-correlated events when available, error trend, and relevant alert
  history.
- R3a: Build the initial evidence chain deterministically from project,
  environment, source, time window, request ID, error fingerprint, and
  configured runbooks. It must remain available and auditable without an LLM.
- R4: Produce a report that clearly separates observed facts, hypotheses,
  missing evidence, manual verification steps, and proposed remediation.
- R5: Allow `admin` and `operator` users to record root cause, known issue,
  false positive, or manual action required. Confirmed outcomes may become
  future diagnostic rules.
- R6: Provide configurable per-fingerprint notification policy. The default
  lifecycle notification is first unmuted occurrence, material escalation,
  recovery, and manual closure. Per-occurrence notification and muting are
  allowed overrides.
- R7: Support signed outbound webhooks for the default notification lifecycle.
- R8: Support signed inbound webhooks, a read-only Tencent CLS MCP connector,
  and a generic read-only HTTP JSON connector framework in the first runnable
  release.
- R9: Model all connectors as push ingestion, pull collection, or both, and
  require them to produce shared observation/evidence contracts. The product
  architecture must admit cloud log API, MCP, custom API, and SSH collection
  connectors without changing grouping or reporting behavior.
- R10: Define SSH connector configuration and read-only safety boundaries, but
  do not execute SSH commands in the MVP.
- R11: Provide an internal web console and REST API for incident review,
  evidence inspection, connector configuration, feedback, and audit viewing.
- R11a: Before implementation begins, provide a reviewable prototype using
  representative local data for the primary incident-review and configuration
  workflows. The approved prototype guides the console implementation but has
  no production access or credentials.
- R12: Use local accounts with `admin`, `operator`, and `viewer` roles.
  Bootstrap the first administrator through deployment configuration. SSO is
  deferred.
- R13: Keep useful deterministic grouping, evidence collection, and reports
  available when remote LLM diagnosis is disabled.
- R18: Run a durable, project-enabled remediation harness for qualifying
  incidents. It must coalesce repeated observations, analyze redacted evidence
  and the exact deployed commit through policy-mediated typed tools, classify
  code-fixable and non-code outcomes, compare viable repair plans, and perform
  bounded isolated patch/validation iterations without exposing credentials or
  arbitrary shell/network authority to the model.
- R18a: Generate a remediation package that ties facts and hypotheses to the
  selected code change, test results, risk analysis, rollback plan, and
  verification criteria. The system must state why the selected change follows
  from the evidence and label uncertainty or missing evidence.
- R19: Use separately scoped Git/SCM change credentials to create a
  policy-permitted hotfix branch from the exact deployed commit, commit and push
  the validated patch for any configured Git provider, and create a native
  draft PR for Yunxiao. Other providers expose the pushed branch and available
  compare metadata for manual change-request creation. Operator approval is not
  required for these restricted repository actions.
- R20: Require human approval to merge a remediation PR into the production
  branch. Do not trigger a production deployment, execute a production
  configuration/database change, or claim recovery without the relevant
  external approval and verification evidence.
- R21: Execute polling, recovery checks, retention, evidence collection, LLM
  analysis, notifications, and remediation work through RabbitMQ with
  PostgreSQL transactional-outbox publication, at-least-once delivery, manual
  acknowledgement after durable success, idempotent handlers, bounded retries,
  and dead-letter isolation. Redis is not a task queue, RabbitMQ is not a
  business source of truth, and the API does not directly depend on broker
  availability.

## Security And Data Requirements

- R14: Connector and notification secrets are encrypted in the local database.
  The encryption key is loaded from a Docker Secret and is never displayed,
  logged, audited as a value, or sent to an LLM.
- R15: Before any remote LLM request, require project-level opt-in; redact
  configured sensitive fields and secret patterns; minimize the evidence; and
  record the opt-in plus sent evidence metadata in the audit trail.
- R15a: A remote LLM may analyze redacted evidence and propose a next
  read-only collection request, but may not access connector credentials,
  invoke arbitrary tools, or create evidence without connector provenance and
  audit records. The application validates every proposed request against the
  source capabilities and configured collection policy before execution.
- R15b: The initial provider set is Anthropic Claude, OpenAI, and explicitly
  configured OpenAI-compatible endpoints. Their transport differences must be
  isolated behind the shared `LLMProvider` contract; a disabled or unavailable
  provider must not block deterministic evidence or reporting.
- R15c: Only the background worker may make a remote LLM request. It must use
  either direct HTTPS to an administrator-allowlisted provider endpoint or an
  administrator-configured HTTPS proxy; arbitrary public-network egress is
  prohibited.
- R15d: Provider configurations and their encrypted API-key references are
  instance-scoped. Each project separately selects a configured provider/model
  and enables LLM opt-in; project selection never exposes or duplicates the
  underlying key.
- R15e: Do not persist raw LLM requests or raw provider responses. Retain only
  evidence IDs, prompt template version and content hash, provider/model,
  timestamp, available usage metadata, and validated structured conclusions
  included in the incident report.
- R16: Record the source query, collection time, source access scope, and
  content reference for every evidence item.
- R17: Retain evidence excerpts for 30 days, and incident metadata plus audit
  records for 180 days by default, configurable per project. Do not replicate
  the full CLS corpus.

## Out Of Scope

- Write actions against monitored production SSH, Docker, Nacos,
  Redis/RabbitMQ, PostgreSQL, or cloud resources. This does not prohibit the
  incident service from using its own PostgreSQL, Redis, and RabbitMQ instances.
- PR merge, production deployment, production configuration/database changes,
  and automatic rollback.
- Automatic reproduction of production failures.
- Frontend discovery and Cloudflare R2 diagnostics.
- Direct PostgreSQL connections, Kubernetes deployment, high availability,
  multi-tenant hosting, enterprise SSO, and platform-specific IM integrations.

## Acceptance Criteria

- [ ] JSON logs from CLS and signed inbound webhooks normalize to a common
      observation contract and retain a raw-source reference.
- [ ] Every successfully ingested error is visible in its project event stream.
- [ ] A new fingerprint creates one `Info` incident; matching occurrences join
      it without becoming invisible or creating unrelated incidents.
- [ ] An incident exposes error samples, context, trend, alert history when
      available, evidence provenance, and separate facts from hypotheses.
- [ ] Deterministic correlation creates an auditable initial evidence chain
      when remote LLM analysis is disabled; any LLM-proposed follow-up query is
      constrained to configured read-only connector capabilities and retains
      normal evidence provenance.
- [ ] Project-level rules can mute a fingerprint or set its notification mode
      without deleting its occurrences or incident history.
- [ ] A quiet incident becomes recovered but not closed; a subsequent matching
      occurrence reopens it with its prior history intact.
- [ ] Operators can record an outcome without any production write permission.
- [ ] A remediation package visibly connects the diagnosis to its evidence,
      patch rationale, test results, risk/rollback plan, and verification
      criteria, including unresolved uncertainty.
- [ ] A qualifying incident creates one coalesced, restart-safe remediation run
      that either records a cited non-code/manual-review outcome or selects,
      applies, and validates a policy-compliant repair against the exact
      deployed commit without exposing credentials or arbitrary tools.
- [ ] An automated remediation creates and pushes a convention-compliant
      hotfix branch for every configured Git provider and creates a draft
      Yunxiao PR without production merge or deployment permission; other
      providers return branch/compare information and the final merge remains
      visibly awaiting human approval.
- [ ] Connector implementations can be replaced or added without modifying
      observation grouping, incident lifecycle, or report contracts.
- [ ] A committed asynchronous job survives API/worker/broker restart, executes
      with at-least-once semantics without duplicating durable effects, and
      reaches bounded retry or dead-letter state with observable provenance.
- [ ] Secrets never appear in API responses, application logs, audit payloads,
      or remote LLM evidence.
- [ ] The Docker Compose installation runs on a clean dedicated CVM and
      exposes a protected web console with role-based access.
