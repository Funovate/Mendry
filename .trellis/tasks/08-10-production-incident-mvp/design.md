# Technical Design: Production Incident MVP

## Boundaries

The service is a self-hosted, single-tenant control plane. It owns its local
state, the web/API surface, connector configuration, encrypted secrets, and
audit log. It does not own production infrastructure or execute production
changes.

```text
Inbound Webhook  ─┐
CLS MCP Connector ├─> Connector Runtime ─> Observation Pipeline
HTTP JSON Connector┘                             |
                                                   v
Project / Environment / optional Service -> Event Stream -> Fingerprint Group
                                                        -> Incident Lifecycle
                                                        -> Evidence Collector
                                                        -> Diagnosis / Report
                                                        -> Remediation Package
                                                        -> Yunxiao Hotfix / Draft PR
                                                        -> Web Console
                                                        -> Outbound Webhook
```

All raw evidence remains local. Remote LLM analysis is a separately enabled
report-enrichment step after redaction; deterministic reports work without it.

## Implementation Stack

The API and background worker are separate Go processes from one shared Go
module. They own authorization, connector execution, evidence orchestration,
audit logging, and all PostgreSQL access. The internal console is a React and
TypeScript single-page application that consumes the protected REST API; it
does not receive connector credentials or direct production-system access.

Docker Compose runs the Go API, Go worker, PostgreSQL, Redis, RabbitMQ, and the
built console.
The initial database and application lifecycle must be usable without a
remote LLM provider. Remote model integration is a Go `LLMProvider` adapter
behind a typed request/result contract. Provider configuration selects an
endpoint and model at deployment/project scope, so an OpenAI-compatible,
vendor-specific, or later local implementation can be substituted without
changing evidence collection or incident behavior.

Provider configurations, including encrypted API-key references, are
instance-scoped. Projects select only provider/model metadata and maintain
their own LLM opt-in. This permits shared operations while avoiding plaintext
key duplication in project records.

## Asynchronous Work

Polling, recovery checks, retention, evidence collection, LLM analysis,
notification delivery, and remediation run as versioned RabbitMQ jobs. An
application transaction writes business state and a job envelope to a
PostgreSQL transactional outbox. The worker publishes committed outbox rows as
persistent messages using mandatory routing and publisher confirms, then
consumes with bounded prefetch and manual acknowledgement.

Delivery is at least once. Handlers load authoritative PostgreSQL state and are
idempotent; RabbitMQ carries bounded identifiers/references rather than secrets
or evidence bodies. Transient failures use bounded delayed retry/dead-letter
topology, exhausted or invalid work reaches a dead-letter queue, and immediate
requeue loops are prohibited. Redis remains disposable state and is not a job
queue. The API does not connect directly to RabbitMQ, so broker outage delays
work without losing committed publication intent or making the API itself
unready.

## Resource Model

```text
Project
  Environment
    Source (connector configuration)
      Observation
      Evidence
  Service (optional label)
  ErrorGroup (fingerprint-scoped)
    Incident
  NotificationPolicy
  AuditEvent
```

`Source` supplies a default project, environment, and optional service. A
validated service label in an observation can override the default. An
observation belongs to precisely one source and project.

## Connector Contract

Each connector declares immutable capabilities:

```text
push_ingestion | pull_collection | context_collection | metric_collection
```

It implements only the capabilities it declares. The core calls connectors
through two shared contracts:

```text
Observation ingest(input) -> NormalizedObservation[]
Evidence collect(incident, collectionRequest) -> EvidenceItem[]
```

First implementations are:

- Signed inbound webhook: validates signature, preserves source envelope, and
  normalizes configured JSON paths.
- Tencent CLS MCP: read-only log search, log context, alarm history, and
  metrics. The product's deployed MCP client configuration is independent of a
  developer's Codex configuration.
- Generic HTTP JSON: read-only, explicitly configured endpoint, authentication
  secret reference, pagination strategy, and JSON mapping.
- SSH: configuration schema and a read-only command policy only; no runtime
  execution in this release.

Cloud APIs and new MCP/custom/SSH collectors later implement the same contract.

## Observation And Grouping

The durable raw event is `Observation`; `ErrorGroup` is a presentation and
policy aggregate, never a filter.

```text
Observation
  id, projectId, environmentId, sourceId, serviceId?
  occurredAt, normalizedLevel, message, sourceLocation, host, requestId?
  rawReference, rawExcerpt?, fingerprint, ingestedAt

ErrorGroup
  fingerprint, project/environment/source scope, firstSeen, lastSeen,
  occurrenceCount, hostCount, notificationPolicy

Incident
  errorGroupId, priority, status, openedAt, recoveredAt?, closedAt?
```

Fingerprint generation uses deterministic normalization: selected volatile
tokens such as UUIDs, request IDs, timestamps, IPs, and long numeric IDs are
replaced; the normalized message plus project/environment/source and source
location are hashed. The original observation remains searchable.

A first fingerprint creates an `Info` incident. Later events update group
counts and evidence. Notification and priority are policy-driven, not an
ingestion gate.

## Lifecycle And Notifications

```text
Open -> Recovered -> Open (recurrence)
Open -> Closed (operator)
Recovered -> Closed (operator)
```

Recovery occurs after the per-project quiet window or an explicit upstream
recovery event. Closure requires an operator outcome.

Notification policies are scoped to project and may target a fingerprint,
source, or default. Supported modes are muted, lifecycle default, and
per-occurrence. Outbound payloads are HMAC-signed and include only the
incident summary, URL, event type, and timestamp.

## Evidence And Reports

Evidence is append-only and stores provenance: connector, query/request,
collection time, access scope, retention expiry, content hash, and retained
excerpt. For CLS incidents, collection includes nearby logs, request-ID query
when available, count trend, and alert history.

The evidence orchestrator first creates collection requests deterministically
from the incident's project/environment/source, occurrence time window,
request ID, fingerprint, and configured runbooks. This produces a useful,
auditable fact chain even with remote LLM analysis disabled. After project
opt-in and redaction, an LLM may analyze that chain and propose an additional
read-only request. The application validates the request against the source's
declared capabilities and configured collection policy before a connector can
execute it. The model never receives credentials, has no direct tool access,
and cannot create an evidence item without normal connector provenance.

The Go provider adapter receives only redacted evidence summaries and an
allowlisted structured response schema. The service validates the response
before persisting a hypothesis or translating a proposed query into a
collection request. Provider outage, malformed output, or disabled opt-in is
recorded as a report limitation and never blocks deterministic evidence.

The service does not retain raw provider request or response bodies. Its audit
record stores evidence IDs, prompt template version and content hash,
provider/model, request time, available usage metadata, outcome, and only the
validated structured conclusion included in the report. This supports review
without creating a second retained copy of redacted evidence.

The report contains sections for facts, hypotheses with evidence links,
missing evidence, suggested verification, and remediation proposal. A remote
LLM may enrich the proposal only after project opt-in and redaction. The audit
trail records the provider, model, request time, and evidence item IDs, never
secret values.

## Remediation And Change Requests

A remediation package is a durable, reviewable record linked to an incident.
It contains the diagnosis, cited evidence IDs, proposed patch and rationale,
test plan/results, risk assessment, rollback plan, approval state, and
post-change verification criteria.

The domain-specific remediation harness owns the durable agent loop. It
coalesces qualifying incident triggers, assembles redacted evidence and bounded
repository context, lets the model request only typed policy-mediated tools,
classifies fixability, compares repair plans, and applies the selected plan in
an isolated workspace from the exact deployed commit. The model never receives
credentials, a direct connector/client, arbitrary shell, Git publication, or
merge authority. Validation uses administrator-approved versioned command IDs;
passing output and policy checks are required before publication.

Git read and change identities are separate. The change scope is restricted to
named repositories and policy-approved branch namespaces. Automated
remediation may commit and push a validated hotfix branch for configured Git
providers without operator approval. The Yunxiao adapter additionally creates
a draft PR with the remediation package; other providers return branch and
available compare metadata for manual change-request creation. No adapter
merges, deploys, changes production configuration/database state, or asserts
recovery; those steps remain human approval and verification actions.

## Security

- The deployment encryption key is read from a Docker Secret. Per-connector
  and per-notifier credentials are envelope-encrypted before database storage.
- Secret read APIs return metadata only; values can be replaced or deleted but
  not retrieved.
- Roles: `admin` manages users, connectors, policies, secrets, and LLM opt-in;
  `operator` handles incidents; `viewer` is read-only.
- Inbound webhooks verify HMAC and enforce timestamp/replay limits. Outbound
  webhooks sign payloads and use retry with bounded backoff.
- Redaction runs before evidence is stored in any optional remote-LLM queue.
  The local evidence item remains subject to project retention.
- Only the worker may initiate a remote LLM connection. It uses direct HTTPS
  to an administrator-allowlisted endpoint or a configured HTTPS proxy; the
  API and console have no LLM egress path. TLS verification, request size
  limits, timeouts, and bounded retries are mandatory. Provider IP addresses
  are not pinned because they may be CDN-backed.

## Deployment And Data

Docker Compose runs the application, worker, dedicated local PostgreSQL and
RabbitMQ instances, disposable Redis, and the built console on an operations
CVM. It mounts Docker Secrets for the encryption key and bootstrap admin
secret. PostgreSQL and RabbitMQ use persistent volumes so database state and
durable queue contents survive service restart. PostgreSQL job/outbox records,
not broker contents alone, own durable job provenance and controlled replay.
The persistent database volume must be backed up by the customer; backups use
the same retention and encryption expectations.

Retention jobs purge evidence excerpts after 30 days and incident/audit records
after 180 days, with project overrides. Purging must retain referentially safe
summary metadata only where necessary for audit deletion records.

## Compatibility And Rollout

Start with a single `real-estate / production / cls-backend` source. Run in
observe-only mode for historical CLS replay before enabling outbound
notifications. The connector boundary permits a Tencent Cloud API adapter to
replace the MCP connector later without changing core state.

The principal rollback is to disable a connector or notification policy, which
stops new pulls or outbound calls while preserving locally stored evidence.
