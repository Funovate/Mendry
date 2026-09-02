# Implementation Plan: Incident Backend Scaffold

## Current Delivery Slice

The backend scaffold is delivered as independently verified infrastructure
checkpoints. The pure process, OpenTelemetry, PostgreSQL, Redis, and RabbitMQ
transport checkpoints are complete. PostgreSQL establishes typed queries,
transactions, migrations, readiness, and integration-test safety without
business tables or outbox. Redis adds optional disposable-state connectivity
without queue semantics. RabbitMQ adds worker-only supervised AMQP transport,
versioned topology, confirmed publication, manual acknowledgement boundaries,
safe telemetry, and recovery without claiming durable job semantics.

Durable jobs/outbox, runtime tools, and script execution remain planned. The
task stays in progress after the RabbitMQ checkpoint.

Checkpoint status:

- [x] Initialize the root Git monorepo and move the unchanged frontend package
      to `frontend/`.
- [x] Add independently buildable API, worker, and migrate commands using only
      the Go standard library.
- [x] Add injected typed configuration, JSON logging, bounded lifecycle, and
      liveness/readiness endpoints.
- [x] Add unit/race coverage, release-style builds, local run documentation,
      and executable backend code-specs.
- [x] Verify the frontend still builds from its new package root and the API
      endpoints respond from a running process.
- [x] Audit the pure scaffold against the backend comment contract and add
      Chinese package, exported-contract, lifecycle, and safety-invariant
      comments while preserving canonical English technical terms.
- [x] Add instance-owned OpenTelemetry trace/metric providers, W3C propagation,
      bounded sampling, optional explicit OTLP gRPC or HTTP/protobuf export,
      HTTP/process instrumentation, log correlation, shutdown flush, tests,
      and configuration documentation.
- [x] Add bounded process-owned PostgreSQL pools, startup/readiness/shutdown
      behavior, safe query and pool telemetry, application-owned transactions,
      embedded checksum-verified migrations, a sqlc typed migration boundary,
      reproducible generation, and fail-closed opt-in integration coverage.
- [x] Add optional process-owned Redis clients with typed bounded configuration,
      startup/readiness/shutdown behavior, safe command tracing and pool metrics,
      explicit disposable-state-only ownership, and fail-closed integration
      coverage that cleans only a random namespaced key.
- [x] Add worker-only supervised `amqp091-go` RabbitMQ transport with typed
      bounded configuration, TLS/heartbeat, versioned durable topic/quorum
      topology, publisher confirms/mandatory returns, persistent publication,
      manual-ack consumer boundaries, bounded prefetch/concurrency, readiness,
      reconnect recovery, safe telemetry, graceful shutdown, fake/race tests,
      and fail-closed integration coverage that deletes only random exact-name
      topology resources.

## Ordered Checklist

- [ ] Create `backend/` with the Go module, API/worker/migrate commands,
      Makefile targets, build metadata, dependency-direction documentation,
      `sqlc` configuration, and the agreed package layout.
- [ ] Implement typed environment configuration, validation, safe diagnostic
      output, process-specific required dependency sets, and focused
      configuration tests.
- [ ] Implement JSON `slog`, stable event fields, context correlation helpers,
      event ownership/level rules, secret-safe values, log-level configuration,
      and captured-record test utilities.
- [ ] Implement the OpenTelemetry trace/metric lifecycle, W3C propagation,
      optional OTLP export, bounded sampling, in-memory test export, and
      bounded flush.
- [ ] Implement PostgreSQL pool construction, safe query trace/log hooks,
      typed `sqlc` query boundary, feature-specific transaction runner, health
      probe, transactional job outbox schema and lease primitives, embedded
      migrations, and the explicit migrate command.
- [ ] Test transaction begin/commit/rollback/panic cleanup, isolation and
      read-only options, deadline propagation, optimistic conflicts,
      serialization/deadlock retry classification and bounded attempts, and
      preservation of original errors when rollback also fails.
- [ ] Implement Redis client construction, safe command trace/log hooks, health
      probe, and shutdown behavior without queue semantics.
- [x] Implement the supervised `amqp091-go` RabbitMQ connection/channel
      lifecycle, TLS/heartbeat configuration, versioned durable topology,
      publisher confirms/returns, persistent mandatory publication, manual-ack
      consumers, bounded prefetch/concurrency, readiness, telemetry, and
      graceful shutdown.
- [ ] Implement the versioned bounded job envelope, transactional outbox relay,
      short-transaction `SKIP LOCKED` claims, opaque-token compare-and-set lease
      completion and recovery, capped-backoff unlimited transient publication
      retries, durable deterministic-failure blocking, terminal-row retention
      and bounded cleanup, immutable delivery identity, scoped business
      idempotency keys and canonical payload hashes, transactional delivery
      receipts and business effects for database-only handlers, leased external-
      operation state and recovery-mode contracts, consumer receipt retention
      and replay-horizon enforcement, PostgreSQL-authoritative execution leases
      and append-only attempts, explicit handler registry with mandatory per-kind
      execution/retry policies and platform ceilings, idempotent fake handler,
      transactionally scheduled retry/dead-letter outbox dispositions, delayed
      retry tiers, dead-letter handling, classified job results, authoritative-
      state and precondition checks for out-of-order work, and trace propagation.
- [ ] Implement the runtime-tool descriptor/invocation/result contracts,
      immutable injected registry, factory/version rules, policy port, bounded
      executor, audit hooks, safe errors, and fake adapters for independently
      modeled transport, capability, and execution-target combinations.
- [ ] Implement the protected diagnostic-script artifact and review contracts,
      including immutable hashes, provenance/risk metadata, exact-hash approval,
      approval invalidation after edits, explicit unreviewed state, mandatory
      execution denial before human approval, and durable content references
      rather than script bodies in job messages.
- [ ] Implement the out-of-process runner boundary and test fixture with clean
      environment, isolated workspace, explicit interpreter/argv, bounded
      resources/concurrency/output, process-tree cancellation, default-denied
      filesystem/network capabilities, result references, idempotent invocation
      claims, and fail-closed isolation checks. Do not add a production script or
      SSH MCP adapter in this task.
- [ ] Establish the operational-command and developer-tool boundaries; use the
      migrate command, restricted exact-ID `jobctl` outbox inspection/recovery,
      one-at-a-time job replay and external-operation reconciliation, and `sqlc`
      generation as their first concrete examples.
- [ ] Implement the HTTP server, versioned router, request ID, trusted-proxy IP
      resolution, trace middleware, bounded/redacted request and response
      capture, recovery, timeouts, maximum body size, and problem responses.
- [ ] Implement liveness/readiness endpoints and concurrent, bounded,
      process-specific PostgreSQL, Redis, and RabbitMQ readiness probes.
- [ ] Implement API and worker composition roots with ordered construction,
      process-specific dependencies, partial-startup cleanup, signal handling,
      staged bounded draining, RabbitMQ consumer/publisher shutdown, telemetry
      flush, and client closure.
- [ ] Add unit tests for configuration, lifecycle, logging, trace propagation,
      IP attribution, body redaction, error mapping, health behavior, database
      logging, Redis logging, RabbitMQ/job behavior, tool
      registration/execution, script version/review transitions, runner timeout
      and cancellation, output truncation, isolation denial, command boundaries,
      `jobctl` transition/generation/grant/CAS/recording rules, and concurrent
      shutdown.
- [ ] Add opt-in PostgreSQL/Redis/RabbitMQ integration tests that require
      explicit safe test endpoints and verify connectivity, topology,
      confirm/return behavior, outbox recovery, redelivery/idempotency,
      retry/dead-letter behavior, correlation, migration, and resource cleanup.
- [ ] Document architecture, configuration variables, external dependency
      contracts, schema/query/migration rules, RabbitMQ topology and delivery
      semantics, commands, observability integration, test prerequisites, and
      extension rules for later feature packages, jobs, and custom tools.

## Validation Commands

Run from `backend/`:

```bash
gofmt -w .
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/...
```

Run integration tests only with explicitly isolated PostgreSQL, Redis, and
RabbitMQ test endpoints:

```bash
go test -tags=integration ./tests/integration/...
```

The integration command must fail closed when required test endpoint variables
or isolation markers are absent. No validation command provisions, restarts,
or deletes an external service.

## Review Gates

- Verify no mutable package-level clients, hidden environment reads, or
  dependency construction exists outside composition roots.
- Verify package imports follow the documented direction and no domain or
  application API exposes pgx, Redis, AMQP, HTTP, generated SQL, or adapter
  types.
- Verify every logged field has a stable name and no test can find headers,
  credentials, bind arguments, Redis values, or disallowed body fields.
- Verify HTTP, application, worker, database, Redis, RabbitMQ/job, health, and
  tool events use one logger/envelope, inherit the correct correlation fields,
  and keep event names separate from human-readable messages.
- Verify direct, trusted-proxy, spoofed, multi-hop, and malformed IP cases.
- Verify request ID and W3C trace behavior across valid, invalid, sampled, and
  unsampled upstream contexts.
- Verify query and Redis instrumentation remains useful without raw statements,
  arguments, or high-cardinality values.
- Verify database query, transaction, and pool events use stable fields and
  levels, correlate to request/job and trace context, and do not duplicate
  stack-bearing errors across layers.
- Verify readiness degradation/recovery and that liveness is process-only.
- Verify the API has no RabbitMQ client or readiness dependency; the worker
  requires RabbitMQ and reconstructs channels, confirms, topology, and
  consumers after reconnect.
- Verify no application path performs a PostgreSQL/RabbitMQ dual write; every
  durable job originates from a committed outbox row and remains retryable
  until a positive publish confirm.
- Verify outbox claim transactions commit before broker I/O, concurrent relays
  skip active claims, abandoned leases recover using database time, and every
  completion or reschedule update requires the current opaque lease token.
- Verify transient or unknown publication failures remain retryable without an
  automatic discard threshold; deterministic local failures become durable,
  observable blocked rows without preventing unrelated rows from publishing.
- Verify outbox cleanup uses PostgreSQL time and bounded batches, removes only
  published rows after seven days or cancelled rows after 180 days, preserves
  non-terminal rows and 180-day operation history, and is not reused as a
  consumer-deduplication or business-history mechanism.
- Verify consumers use bounded prefetch and manual acknowledgements, acknowledge
  only after durable disposition, tolerate duplicates idempotently, avoid
  immediate requeue loops, and isolate exhausted/poison messages in a dead-letter
  queue.
- Verify every parsed retry/dead-letter disposition is committed to PostgreSQL
  before acknowledgement, permits exactly one outcome per consumer/job/
  generation/attempt, uses at most one publication ID while preserving job ID,
  and cannot be scheduled twice after acknowledgement loss; only an envelope
  without a safe parsed identity may use direct broker dead-lettering.
- Verify concurrent or out-of-order delivery cannot regress authoritative state;
  already-applied work is acknowledged as success, stale work is a classified
  no-op, and no correctness guarantee depends on RabbitMQ FIFO behavior.
- Verify one job ID is preserved across every transport retry/replay path and is
  deduplicated per consumer, while scoped business idempotency accepts only the
  same canonical payload hash and rejects key reuse with different content.
- Verify a database-only handler commits its delivery receipt, scoped
  idempotency record, and business effect in one application-owned transaction;
  rollback removes all three and an unknown commit result is never acknowledged.
- Verify every external-effect adapter declares idempotency-key, lookup, or no
  recovery capability; recovery reuses or reconciles stable identity, while an
  ambiguous capability-free attempt is acknowledged only after becoming durable
  `reconciliation_required` and is never automatically called again.
- Verify receipt retention exceeds the 180-day direct replay horizon by a safety
  grace, over-age jobs require a newly reviewed job, feature-owned idempotency
  records are outside generic cleanup, and unresolved external operations are
  never removed by age.
- Verify consumer ownership and exhaustion use PostgreSQL execution/attempt
  state, reject concurrent or stale leases, persist interrupted attempts, and
  remain unaffected by missing, reset, or conflicting AMQP attempt metadata.
- Verify the complete handler registry validates before consumption, has no
  implicit retry defaults, and rejects incomplete, contradictory, over-ceiling,
  or side-effect-unsafe policies before the worker becomes ready.
- Verify job messages contain only bounded versioned fields and protected record
  references, never secrets, evidence bodies, credentials, or arbitrary headers.
- Verify shutdown ordering and deadlines under partial startup and dependency
  failure.
- Verify runtime tools cannot bypass capability/policy checks, load arbitrary
  in-process code, access database/Redis clients, or leak secret-bearing input.
- Verify file-reading MCP content is never executable, while MCP transport can
  independently advertise `file.read`, `log.read`, and `diagnostic.execute`.
- Verify script approval is bound to an immutable hash, edits invalidate it, and
  unreviewed hosted artifacts cannot be mistaken for reviewed artifacts or
  reach any local or remote execution adapter.
- Verify script bodies, SSH/MCP credentials, user source, logs, and execution
  outputs never enter RabbitMQ envelopes; only protected references do.
- Verify the runner kills the full process tree on cancellation/timeout, truncates
  output deterministically, enforces resource and capability policy, records
  audit/trace provenance, and refuses execution if isolation is unavailable.
- Verify operational CLI commands reuse application services and developer-only
  tool dependencies are absent from production binaries.
- Verify `jobctl` exposes only redacted exact-ID inspection and the declared
  compare-and-set transitions, cannot mutate leased/published rows, records each
  successful mutation durably, and has no bulk, delete, or arbitrary-edit path.
- Verify each authorized replay/reconciliation requeue appends one bounded grant,
  increments execution generation, preserves job ID and all receipts/attempts,
  and creates one publication; manual success/failure requires durable evidence,
  while over-age, unsafe-capability, stale, repeated, or bulk actions fail.
- Verify the repository contains no deployment manifests, bundled
  infrastructure services, trace backend, or cloud-vendor provisioning added
  by this task.

## Rollback Points

- Keep module/package creation separate from persistence and middleware work so
  dependency choices can be reverted before later feature packages consume
  them.
- API and worker never apply migrations. Reverting binaries is independent of
  migration rollback; each future non-empty migration must document its own
  data-safe rollback decision.
- Observability export can be disabled through configuration without disabling
  local structured logs, request IDs, trace IDs, health endpoints, PostgreSQL,
  Redis, or RabbitMQ connectivity.
- RabbitMQ consumption can be paused without deleting PostgreSQL outbox rows or
  durable business state. Restoring the worker resumes confirmed publication
  and at-least-once handling; dead-letter replay is an explicit operational
  action rather than an automatic rollback side effect.
