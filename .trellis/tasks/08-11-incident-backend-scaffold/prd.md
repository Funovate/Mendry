# Incident Backend Scaffold

## Goal

Create a production-oriented Go backend scaffold for the self-hosted incident
service. The scaffold must make its own HTTP, application, PostgreSQL, Redis,
RabbitMQ, and worker execution paths observable and provide stable boundaries
for later domain and security features.

## Parent References

`08-10-production-incident-mvp/prd.md`, `design.md`, and `implement.md`, plus
`08-10-incident-service-foundation/prd.md`.

## Background

- The repository currently contains only the approved React/TypeScript
  prototype; no backend implementation or established backend conventions
  exist yet.
- This is the first backend implementation child and establishes the Go module,
  layout, runtime lifecycle, dependency-injection, data-access, and
  observability conventions consumed by later children.
- The observability described here monitors this incident service itself. It
  does not instrument or directly connect to monitored production systems.

## Requirements

- Create one Go module with independently runnable API and worker commands and
  a documented build, test, and local run workflow using externally supplied
  dependency endpoints.
- Use a clear backend layout with explicit transport, application, domain,
  persistence, infrastructure, configuration, and observability boundaries.
  API and worker composition roots construct and inject dependencies; mutable
  package-level clients are prohibited.
- Define and enforce dependency directions for shared platform code,
  feature-oriented business modules, inbound transports, outbound adapters,
  runtime tool integrations, and internal commands. Later features must have a
  documented location without introducing catch-all `utils`, `common`, or
  global model packages.
- Provide validated environment configuration with secure defaults, actionable
  startup errors, and no secret-value logging.
- Provide liveness and readiness endpoints. Readiness reflects only the
  dependencies required by the current process and its enabled features;
  liveness does not fail solely because a dependency is unavailable. RabbitMQ
  availability is not an API readiness dependency, while the worker requires
  RabbitMQ before accepting deliveries.
- Handle process signals with bounded graceful shutdown for HTTP traffic,
  worker work, telemetry flush, RabbitMQ, Redis, and PostgreSQL resources.
- Trace every HTTP request from ingress through application and persistence
  activity to its response. Correlated records include a generated or
  validated request ID, trace ID, span ID, route, method, status, latency,
  detailed client-network attribution under a trusted-proxy policy, and actor
  identity when later authentication supplies one.
- Always log request and response metadata. Body capture is restricted to
  configured routes and supported JSON content, uses an explicit field
  allowlist plus recursive sensitive-field redaction, and applies strict size
  limits. Passwords, authorization values, cookies, session material, secret
  values, encryption plaintext, multipart/file bodies, and unsupported binary
  content are never logged. Production body capture defaults off and can be
  enabled only by an explicit route-and-field policy.
- Emit structured internal logs with stable event names, severity conventions,
  service/build/environment identity, request or job correlation, trace/span
  correlation, error classification, and duration fields.
- Use vendor-neutral OpenTelemetry traces and metrics, OTLP export, and W3C
  Trace Context propagation. Application code must not depend on a cloud-vendor
  SDK. Runtime configuration can route telemetry through an OTLP-compatible
  gateway to a cloud APM or another external telemetry backend.
- Keep request IDs distinct from distributed trace and span IDs. HTTP request
  IDs identify one API ingress and are returned to callers. Worker activity
  uses a job ID and continues originating trace context when available.
- Provide a fully initialized Redis client for composition roots that enable a
  Redis-backed feature, including validated settings, bounded timeouts,
  startup behavior, readiness checks, tracing, graceful shutdown, and test
  substitution. Redis is limited to disposable operational state such as
  sessions, rate limits, idempotency keys, and bounded short-lived caches; it
  is not a queue or source of business truth.
- Inject a supervised RabbitMQ client and durable job runtime into the worker,
  including validated TLS/heartbeat settings, connection and channel recovery,
  versioned topology, publisher confirms, mandatory routing, persistent
  messages, bounded prefetch/concurrency, manual acknowledgements, retry/dead-
  letter behavior, readiness, tracing, metrics, graceful shutdown, and test
  substitution. RabbitMQ transports work but is not a source of business truth.
- Provide at-least-once job delivery through a versioned, size-bounded envelope
  that carries identifiers, typed payload or durable payload reference,
  idempotency metadata, deadline, and trace context but no secrets or evidence
  bodies. Consumers acknowledge only after a durable successful or terminal
  disposition commits and must be idempotent; the system does not claim exactly-
  once execution.
- Do not claim global, project, incident, aggregate, or queue-level FIFO job
  execution. Each handler reloads authoritative PostgreSQL state, validates any
  expected business version or precondition, and classifies an already-applied
  or stale job without regressing state. Database uniqueness, optimistic
  concurrency, and handler idempotency protect concurrent and out-of-order work.
- Separate delivery identity from business idempotency. The immutable job ID and
  execution generation are preserved across ordinary publication retries,
  RabbitMQ redelivery, and delayed retries and are deduplicated per named
  consumer; only an explicit replay grant increments generation while preserving
  job ID. Every job with an external or otherwise non-repeatable effect also
  carries an application-generated, operation-scoped idempotency key bound to a
  canonical versioned payload hash. The same scoped key and hash identifies the
  same operation; the same key with a different hash is a contract conflict,
  never silent success.
- For a handler whose durable effect is wholly in PostgreSQL, commit its delivery
  receipt, business-idempotency record, and business mutations in one bounded
  application-owned transaction before acknowledging RabbitMQ. A rollback
  removes all three; a commit with a lost acknowledgement is recognized on
  redelivery. The receipt mechanism is exposed through transaction-bound ports
  without leaking pgx into application or domain contracts.
- Model an external side effect as a durable operation outside the database-only
  receipt transaction. Each adapter declares whether it supports a stable
  idempotency key, deterministic lookup/reconciliation, or neither. Recovery
  retries with the same external key when supported, otherwise reconciles by a
  stable external identity; an ambiguous outcome with neither capability stops
  automatic execution in `reconciliation_required` rather than risking a
  duplicate or claiming success. No network call runs inside a database
  transaction.
- Retain terminal delivery receipts for at least 180 days plus a cleanup safety
  margin, and reject direct retry/dead-letter replay once the original job age
  reaches 180 days. An older operation requires business review and a newly
  created job. Generic cleanup never removes business-idempotency records;
  their owning feature retains them with the protected business invariant.
  Pending, in-flight, and reconciliation-required external operations are never
  age-pruned; terminal external-operation diagnostics remain for 180 days.
- Make PostgreSQL execution state and append-only attempt records authoritative
  for consumer concurrency and retry limits. Each named consumer permits one
  active database-time lease per job ID and durably records every started,
  completed, failed, or interrupted attempt without payload content. Envelope
  attempt fields and RabbitMQ death headers are observational hints only and
  cannot reset, bypass, or determine the configured retry limit.
- For every safely parsed delivery, persist its classified disposition and any
  retry or dead-letter publication intent atomically in PostgreSQL, then
  acknowledge the original delivery. Exactly one classified disposition is
  allowed per consumer, job, execution generation, and attempt; each resulting
  publication has a new publication ID while preserving the original job ID.
  Only a message whose bounded envelope identity cannot be parsed may use broker
  dead-lettering directly, because no safe database identity can be established.
- Require every registered job kind to declare its complete retry and execution
  policy explicitly: attempt limit and timeout, retryable error classes, approved
  delay tiers, maximum job age/deadline behavior, exhaustion disposition,
  business-idempotency requirement, and external recovery capability when
  applicable. There is no implicit catch-all retry policy. Registration fails
  before consumption when a policy is missing, contradictory, or exceeds
  platform safety ceilings.
- Inject a bounded PostgreSQL pool into API and worker composition roots with
  migrations, startup behavior, readiness checks, tracing, graceful shutdown,
  and transaction-ready repository boundaries.
- Persist job publication intent in a PostgreSQL transactional outbox in the
  same transaction as the owning business state. A worker relay publishes
  outbox records with RabbitMQ publisher confirms and marks them published only
  after confirmation, so API request handling never performs an unsafe
  PostgreSQL/RabbitMQ dual write.
- Claim outbox records through a short PostgreSQL transaction using bounded
  batches, row skipping, and expiring leases. Broker publication and confirm
  waiting occur only after the claim transaction commits. Completion or retry
  updates compare an opaque lease token so an expired relay cannot overwrite a
  newer claim; an abandoned claim becomes eligible after its database-time
  lease expires.
- Retry transient or unknown outbox publication failures indefinitely with
  capped exponential backoff and jitter; a retry limit never discards committed
  publication intent. Deterministically invalid local records become durable
  `blocked` records that do not stop unrelated publication and can only be
  explicitly requeued or cancelled after correction. Outbox records are never
  sent to a broker dead-letter queue before confirmed publication.
- Retain published outbox rows for seven days and cancelled rows plus durable
  outbox-operation records for 180 days. Ready, leased, and blocked rows are
  never automatically removed. Cleanup uses database-time cutoffs and bounded,
  rate-limited batches, preserves operation history for its full retention, and
  never treats outbox retention as business history or consumer deduplication.
- Establish database conventions for module ownership, identifiers, timestamps,
  naming, constraints, indexes, JSON usage, pagination, optimistic concurrency,
  typed queries, transaction ownership, migration safety, and integration-test
  isolation. Business modules must not expose pgx or generated query types
  through domain/application contracts.
- Integrate database activity with the service logging model. Record operation
  identity, duration, outcome, and trace correlation without bind values,
  secret-bearing SQL, or high-cardinality raw statements.
- Provide a consistent versioned API surface, JSON success representation,
  machine-readable error representation, request ID response header, unknown
  route handling, panic recovery, and maximum request-size enforcement.
- Document the PostgreSQL, Redis, RabbitMQ, and OTLP connection contracts
  without provisioning those external services or coupling the application to
  a deployment mechanism.
- Provide a minimal runtime-tool extension kernel with versioned descriptors,
  capability declarations, typed invocation/result envelopes, an injected
  registry, policy checks, deadlines, bounded results, structured failures,
  audit hooks, and trace propagation. Transport, capability, and execution
  target are separate concerns: an MCP adapter may expose read-only file/log
  access, isolated diagnostic-script execution, or both, including through a
  later SSH MCP implementation. Arbitrary code is never loaded into the API or
  worker through Go plugins, dynamic libraries, or equivalent in-process hooks.
- Treat AI-produced diagnostic scripts as protected, versioned artifacts rather
  than fixed catalog entries. Each artifact records its immutable content hash,
  interpreter and argument contract, target requirements, provenance, risk
  classification, review state, reviewer, review policy version, and timestamps.
  A content change creates a new version and invalidates any earlier approval.
- Permit unreviewed script artifacts to be hosted, but label and segregate them
  unambiguously from reviewed artifacts and expose their risks to operators.
  Hosting does not grant execution permission: the initial policy permits only
  human-approved scripts whose exact artifact hash matches the review record.
- Execute an approved diagnostic script only out of process through an injected
  sandbox/remote-execution boundary. Apply a clean environment, isolated
  workspace, least-privilege identity, deadline and process-tree cancellation,
  CPU/memory/concurrency/output limits, audit and trace correlation, and deny
  filesystem or network capabilities unless the invocation policy explicitly
  grants them. A runner that cannot enforce a requested boundary fails closed.
- User application source obtained through a file-reading MCP is data for
  analysis only and must never be interpreted or executed. A later SSH MCP may
  provide `log.read` and `diagnostic.execute` capabilities, but arbitrary shell
  access is not a capability and all execution remains subject to script review,
  target, resource, secret, and audit policy.
- RabbitMQ jobs carry only script/version, connector, target, parameter, secret,
  and result references plus correlation metadata. They never carry script
  bodies, credentials, user source, or collected log/evidence bodies.
- Establish two internal-command patterns: shipped operational commands under
  `cmd/` and developer-only build/generation tools under `tools/`. Operational
  commands reuse application services and platform constructors rather than
  duplicating business logic or calling private HTTP endpoints.
- Ship a restricted `jobctl` operational command for exact-ID inspection,
  requeue, and cancellation of outbox records without direct operator SQL.
  Requeue accepts only blocked records; cancellation accepts only ready or
  blocked records; neither can mutate leased or published records. Mutations
  require a bounded reason and compare-and-set state/version, preserve the
  original job identity and attempt history, and append a durable operation
  record plus a safe structured event. No bulk, delete, or arbitrary state-edit
  operation is provided.
- Extend `jobctl` with exact-ID job inspection, one-at-a-time dead-letter replay,
  and external-operation reconciliation. Replay is limited to the 180-day
  horizon, preserves job ID and all attempt history, appends one operator retry
  grant, increments an execution generation, and creates one new publication.
  Reconciliation requeue requires idempotency-key or lookup capability; manual
  success/failure requires a durable evidence reference. Every mutation uses
  compare-and-set, confirmation, a bounded reason, and immutable operation
  history; no bulk action, reset, or unaudited outcome override exists.
- Do not connect to production systems or expose production write actions.

## Acceptance Criteria

- [ ] API and worker build and run from a clean checkout, share the intended
      internal packages, and shut down cleanly without leaked resources.
- [ ] API and worker report readiness from their own required dependency sets;
      the worker reconnects and restores RabbitMQ topology/consumers after a
      broker restart, while RabbitMQ outage alone does not make the API unready.
- [ ] Liveness remains healthy during a dependency outage while readiness
      reports each failing required dependency for that process and recovers
      after connectivity returns.
- [ ] An integration workflow proves its request ID and trace ID correlate
      ingress, application, PostgreSQL/Redis activity, transactional-outbox
      publication, RabbitMQ delivery, worker handling, and final boundary logs
      while respecting redaction rules.
- [ ] Body-capture tests cover allowed JSON fields, nested sensitive fields,
      oversized bodies, malformed JSON, multipart/file uploads, binary
      content, route-level disabling, and global production disabling.
- [ ] Client-IP tests cover direct traffic, trusted proxy forwarding, spoofed
      forwarding headers, multiple hops, and malformed addresses.
- [ ] Redis, RabbitMQ, and PostgreSQL startup, readiness degradation, recovery,
      timeout, and graceful shutdown paths are tested without mutable global
      clients.
- [ ] A transactional-outbox integration test proves that a committed job is
      eventually published after a broker outage, unroutable publications are
      not marked complete, and duplicate delivery cannot duplicate durable
      handler effects.
- [ ] Concurrent-relay and crash-window tests prove each active outbox claim has
      one owner, broker I/O never holds a database transaction open, expired
      leases are reclaimed, and a stale lease token cannot mark or reschedule a
      row after a newer relay claims it.
- [ ] Outbox failure tests prove transient and unknown results remain retryable
      with capped backoff, deterministic invalid records become visible and
      non-blocking `blocked` records, and no automatic attempt limit deletes,
      publishes, requeues, or cancels committed intent.
- [ ] Retention tests prove only published rows older than seven days and
      cancelled rows older than 180 days are cleanup candidates, non-terminal
      rows survive regardless of age, operation records survive for 180 days,
      and cleanup is bounded, concurrent-safe, and based on PostgreSQL time.
- [ ] RabbitMQ tests prove persistent confirmed publication, mandatory-route
      failure, bounded prefetch, manual acknowledgement only after durable
      disposition, retry without a hot requeue loop, dead-letter exhaustion,
      poison-message isolation, trace propagation, and graceful consumer draining.
- [ ] Out-of-order and concurrent job tests prove stale work cannot regress
      authoritative state, already-applied work is acknowledged safely, and no
      correctness guarantee depends on RabbitMQ FIFO delivery.
- [ ] Identity contract tests prove one immutable job ID and execution generation
      survive ordinary publish retry, redelivery, and delayed retry; an explicit
      replay grant alone increments generation; different consumers can process
      one generation independently; and an operation-scoped idempotency key
      accepts the same canonical payload hash but rejects a conflicting hash.
- [ ] Database-only consumer tests prove receipt, idempotency record, and
      business effect commit or roll back together; concurrent duplicate
      deliveries apply one effect; commit-before-ack recovery returns the saved
      outcome; and an unknown commit result is not acknowledged prematurely.
- [ ] External-operation tests prove capability declaration is mandatory,
      idempotency-key retries reuse one key, lookup-capable retries reconcile
      before creating an effect, capability-free ambiguous attempts enter
      `reconciliation_required`, and no external call holds a database
      transaction or is blindly repeated after ownership loss.
- [ ] Consumer-retention tests prove receipts survive the complete 180-day
      replay horizon plus cleanup grace, jobs at or beyond that age cannot be
      directly replayed, generic cleanup cannot delete feature-owned business
      idempotency, and unresolved external operations survive regardless of age.
- [ ] Attempt-accounting tests prove concurrent duplicate messages cannot hold
      two execution leases, an abandoned lease becomes an interrupted durable
      attempt, retry exhaustion follows PostgreSQL history despite missing or
      conflicting broker headers, and ordinary operations cannot reset history.
- [ ] Disposition tests prove success is acknowledged only after its terminal
      receipt commits; retry and dead-letter intents commit before acknowledgement
      and publish eventually through one durable disposition intent/publication
      ID even if broker delivery duplicates; acknowledgement loss cannot schedule
      a second intent; and only an unparseable bounded envelope uses direct broker
      dead-lettering.
- [ ] Handler-registry tests reject missing or invalid retry policies, unapproved
      delay tiers, excessive limits/timeouts, incompatible exhaustion/recovery
      modes, and side-effecting handlers without required business idempotency;
      no delivery is accepted before all registered policies validate.
- [ ] Database contract tests prove migration ordering, typed query boundaries,
      transaction commit/rollback behavior, optimistic-conflict mapping,
      safe pagination, and isolated cleanup against an explicit test database.
- [ ] Database logs share the application log schema and trace correlation,
      include operation, duration, and outcome, and omit bind values and raw
      sensitive statements.
- [ ] Trace tests cover valid upstream W3C propagation, invalid context
      replacement, downstream propagation, request ID response headers, and
      log/span correlation without a vendor-specific SDK.
- [ ] Metrics tests cover HTTP/dependency/job latency and outcomes, pool and
      backlog state, retry/dead-letter counts, and rejection of high-cardinality
      identifiers as metric labels.
- [ ] Configuration, API errors, panic recovery, body-size limits, and signal
      shutdown behavior have focused automated tests.
- [ ] Runtime-tool tests prove registration, version selection, duplicate
      rejection, capability/policy denial, safe error mapping, audit callbacks,
      and trace propagation without implementing a production connector.
- [ ] A diagnostic-script fixture proves that review is bound to the exact
      artifact hash, an edit invalidates approval, unreviewed hosting remains
      visibly segregated and cannot execute, and a reviewed artifact is passed
      to an out-of-process runner without being interpreted or dynamically
      loaded by API/worker code.
- [ ] Runner tests prove clean environment handling, process-tree termination on
      timeout/cancellation, bounded concurrency, output truncation, denied
      filesystem/network capabilities, safe failure mapping, audit correlation,
      and fail-closed behavior when an isolation control is unavailable.
- [ ] Contract tests prove file-reading MCP content cannot enter an execution
      path and that an MCP transport can later advertise separate `file.read`,
      `log.read`, and `diagnostic.execute` capabilities without changing the
      tool-runtime contract.
- [ ] Job-envelope tests prove script source, credentials, user source, logs, and
      result bodies are replaced with protected durable references.
- [ ] The migrate command demonstrates the operational CLI pattern, while
      developer-only tool dependencies cannot enter production binaries.
- [ ] `jobctl` tests prove exact-ID safe inspection, valid blocked requeue and
      ready/blocked cancellation, durable operation recording, stale-state
      conflict rejection, and denial of leased, published, bulk, delete, or
      arbitrary mutation paths without exposing envelope bodies or secrets.
- [ ] `jobctl` replay/reconciliation tests prove one operator grant creates one
      higher execution generation and publication without resetting attempts;
      over-age jobs, unsafe reconciliation requeue, missing evidence references,
      stale versions, repeated grants, and bulk or arbitrary overrides fail.
- [ ] Backend formatting, static analysis, unit tests, and externally configured
      PostgreSQL/Redis/RabbitMQ integration tests are documented and runnable
      locally.

## Out Of Scope

- React console implementation, authentication, RBAC, administrator bootstrap,
  encrypted connector secrets, audit-domain behavior, and LLM providers.
- Incident-domain models, connectors, production evidence ingestion, and any
  monitored-system instrumentation or direct database access.
- RabbitMQ broker provisioning, cluster operations, management UI, backup, and
  deployment policies. This task implements and documents the application-side
  connection, topology contract, transactional outbox, publication, and
  consumption semantics against an externally supplied broker.
- Concrete MCP, HTTP, SSH, webhook, cloud, SCM, or user-authored production tool
  adapters and production diagnostic scripts. This task provides their safe
  extension kernel, script-artifact/review contract, and isolated runner boundary
  with test fixtures.
- Production observability vendor provisioning and cloud-specific SDKs.
- Deployment manifests, infrastructure provisioning, and bundled PostgreSQL,
  Redis, RabbitMQ, telemetry gateways, or trace backends.
