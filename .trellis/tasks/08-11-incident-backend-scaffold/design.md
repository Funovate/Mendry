# Technical Design: Incident Backend Scaffold

## Scope And Principles

This task creates only the Go backend engineering baseline. PostgreSQL, Redis,
RabbitMQ, an OTLP receiver, and any telemetry UI are external runtime
dependencies. The repository contains connection contracts but no deployment
manifests or infrastructure provisioning.

The scaffold uses explicit constructors and small interfaces at consuming
boundaries. It avoids a dependency-injection framework, mutable global clients,
cloud-vendor telemetry SDKs, and empty abstractions for domain features that do
not exist yet.

## Module Layout

```text
backend/
  cmd/
    api/main.go                 thin shipped command
    worker/main.go              thin shipped command
    migrate/main.go             thin shipped operational command
    jobctl/main.go              restricted outbox operational command
  internal/
    bootstrap/                  dependency construction + process lifecycle
    platform/
      config/                   environment decoding and validation
      observability/            slog, OpenTelemetry, correlation helpers
      postgres/                 pool, tracer, transaction primitives
      redis/                    client, tracer, health
      rabbitmq/                 supervised AMQP connection, topology, telemetry
      httpserver/               server lifecycle and shared middleware
    shared/
      apperror/                 stable application error taxonomy
    modules/
      system/                   liveness/readiness vertical slice
        application/
        adapter/http/
      <feature>/                future feature-owned vertical slice
        domain/
        application/
        port/
        adapter/http/
        adapter/postgres/
    jobruntime/                 job envelope, outbox relay, consumers, retries
    toolruntime/                runtime tool contract, registry, policy, executor
    commands/
      migrate/                  operational command implementation
      jobctl/                   exact-ID outbox inspection/recovery
  migrations/                  ordered SQL migrations embedded by command
  tools/                       developer-only generators/tool dependency pins
  tests/integration/           caller-supplied dependency endpoints
  sqlc.yaml                    typed SQL generation ownership
  go.mod
  Makefile
  README.md
```

Only packages needed by this task are created. The `<feature>` tree is a
placement rule, not a request to commit empty directories.

Dependency direction is enforced as follows:

```text
cmd -> bootstrap -> platform + concrete adapters + job/tool runtimes
inbound adapter -> module application -> module domain
module application -> consuming module ports
outbound adapter -> consuming module ports + platform
job handler -> approved module application contract
tool adapter -> toolruntime contract/executor -> approved application port
```

- Domain packages use the standard library and feature-owned value objects.
- Application packages depend on their domain and declared ports, never pgx,
  Redis, HTTP handlers, environment variables, or generated SQL types.
- Inbound adapters decode and validate transport input, call application use
  cases, and encode results. They do not contain business decisions.
- Outbound adapters implement feature ports with platform clients. Database
  row types stay inside the PostgreSQL adapter and are mapped explicitly.
- Platform packages do not import feature modules. `bootstrap` is the only
  package allowed to know all concrete implementations.
- One feature calls another through an exported application contract or domain
  event, never through the other feature's tables or adapter package.
- Generic `utils`, `common`, `helpers`, `models`, and global singleton packages
  are prohibited. Shared code moves to `shared/` only after it has a stable,
  cross-feature contract.

## Composition And Lifecycle

```text
environment
   -> validated Config
   -> structured Logger
   -> OpenTelemetry providers
   -> PostgreSQL pool ----------------> API and worker
   -> Redis client -------------------> enabled consumers only
   -> RabbitMQ supervised connection -> worker only
                                           |
signal -> stop intake -> drain work -> flush telemetry -> close clients
```

Each command owns a single composition root. Constructors accept dependencies
explicitly and return cleanup functions or closable resources. Construction
fails in reverse order so partially initialized resources are always closed.
Shutdown has one overall upper bound plus stage budgets, continues best-effort
cleanup after a stage timeout, and reports which stage failed.

The API fails startup on invalid configuration or failed connection to its
required dependencies. It writes asynchronous publication intent to the
PostgreSQL outbox and does not connect directly to RabbitMQ, so a broker outage
does not by itself make the API unready. The worker requires PostgreSQL and
RabbitMQ, supervises connection/channel recovery, stops new deliveries before
draining in-flight handlers, and closes consumer then publisher channels during
shutdown. Redis participates in startup/readiness only for a process with an
enabled Redis-backed feature. Dependency outages degrade process-specific
readiness but do not make liveness fail.

## Configuration Contract

Configuration is decoded once at process startup and passed as typed values.
It covers service/build/environment identity, HTTP timeouts and limits,
shutdown deadline, log level/body policy, trusted proxy CIDRs, PostgreSQL pool
limits and timeouts, Redis pool/timeouts/TLS, RabbitMQ endpoints/vhost/TLS,
heartbeat, connection recovery, publisher timeout, prefetch/concurrency and
topology identity, plus standard OTLP endpoint, transport, headers, sampling,
and timeout settings.

Validation reports field names but never connection strings, passwords,
headers, or other secret values. Configuration structs expose only the subset
needed by each constructor. Tests can construct typed config directly without
mutating process-wide environment state.

## Request Data Flow

```text
socket peer
  -> request ID validation/generation
  -> trusted-proxy client IP resolution
  -> W3C trace extraction and server span
  -> bounded metadata/body capture and redaction
  -> panic recovery and typed error mapping
  -> route handler/application call
  -> PostgreSQL and/or Redis child spans
  -> bounded response capture and redaction
  -> access log + trace status
  -> X-Request-ID response header
```

`request_id` identifies one API ingress. `trace_id` identifies the distributed
causal chain and `span_id` identifies one operation. Valid incoming W3C
`traceparent` and `tracestate` are continued; malformed context is ignored and
a new trace is created. `X-Request-ID` is accepted only within strict length
and character rules, otherwise a cryptographically random value is generated.

Network attribution always preserves the actual socket peer. Forwarded client
addresses are considered only when the peer belongs to configured trusted CIDR
ranges. Logs distinguish `peer_ip`, resolved `client_ip`, and the validated
forwarding chain so spoofed headers cannot overwrite the transport fact.

Body capture supports only allowlisted routes with JSON media types. It reads
at most the configured capture limit, recursively replaces denylisted keys,
retains only allowlisted fields, and records truncation or omission reasons.
Authorization, proxy authorization, cookies, set-cookie, secrets, files,
multipart bodies, and binary data are never captured. Capture is observational
and must not change the bytes delivered to handlers or clients. Production body
capture defaults off and requires an explicit route-and-field policy to enable.

## Logging, Tracing, And Metrics

### One Structured Event Model

The service uses one `log/slog` JSON pipeline for HTTP, application, worker,
PostgreSQL access, Redis access, RabbitMQ/job activity, runtime tools, health,
and process lifecycle events. Adapters do not create a second logger or log
format.

Every record uses a common envelope:

| Field group | Stable fields |
|---|---|
| Identity | `timestamp`, `level`, `event`, `message`, `service`, `version`, `environment`, `component` |
| Correlation | `request_id`, `job_id`, `trace_id`, `span_id`, `transaction_id`, `invocation_id` when applicable |
| Operation | `operation`, `route`, `method`, `duration_ms`, `outcome` when applicable |
| Error | `error_class`, `error_code`, safe `error_message`; `stack` only at the owning unexpected-error boundary |
| Network/actor | `peer_ip`, `client_ip`, validated proxy chain, actor ID when later authentication supplies one |

`event` is the stable machine-queryable name; `message` is concise human text
and is not used for dashboards or alerts. Field names and event names are
constants owned by the observability package. Values stay typed in JSON rather
than being interpolated into the message.

Context correlation is attached once and inherited. HTTP middleware adds
request/network/trace fields, worker orchestration adds job/trace fields, a
unit of work adds only transaction correlation metadata, and a tool executor
adds invocation metadata. A transaction object or client is never stored in
context; only immutable logging identifiers are.

### Event Ownership

The system logs boundaries and state changes, not function entry/exit:

- Process events: configuration accepted, startup dependency outcome,
  readiness transition, shutdown stage, and shutdown result.
- HTTP events: one `http.request.completed` record per request plus a separate
  unexpected panic event when applicable.
- Application events: meaningful use-case or domain state transitions, using
  identifiers rather than full entities.
- Dependency events: query, transaction, pool, Redis command, connectivity,
  and latency outcomes.
- Messaging events: outbox publication, broker connection/topology transition,
  publish confirm/return, delivery, retry, acknowledgement, dead-letter, and
  bounded job outcome.
- Tool events: policy decision and bounded invocation outcome.

An error is owned at one layer. Lower adapters record the safe operational
outcome and classification; they do not add a stack or repeat the whole error.
The HTTP/job boundary records the final result. Only the boundary that converts
an unknown failure to `internal` emits its safe error message and stack.

### Levels, Volume, And Output

- `DEBUG`: normal database/Redis operations, detailed lifecycle steps, and
  developer diagnostics.
- `INFO`: startup/shutdown, readiness changes, completed HTTP requests, normal
  use-case transitions, and expected conflicts or cancellations.
- `WARN`: slow operations, dependency degradation, retry, policy denial,
  suspicious client behavior, and recoverable cleanup failure.
- `ERROR`: unexpected dependency failure, failed startup/shutdown guarantees,
  panic, and a request/job ending in an unexpected internal error.

Successful access logs are always emitted. Successful high-volume dependency
logs may be disabled below a configured level, but slow and failed operations
remain visible. Trace sampling and log levels are independent: an unsampled
trace can still contribute its trace ID to an access/error log. The application
writes structured JSON to one stdout stream to preserve ordering; only failures
before logger initialization and runtime-level crashes use stderr. File
rotation, transport, storage, retention, and indexing belong to the external
runtime/log platform.

### Data Minimization

Raw headers and query strings are never logged. A stable allowlist controls
safe headers and query parameters per route. Captured JSON bodies use bounded,
route-specific field allowlists and recursive deny-key redaction; omission and
truncation reasons are explicit. Arbitrary JSON objects are not expanded into
top-level log fields, avoiding sensitive-data and index-mapping explosions.

Secret-bearing configuration types implement redacted log rendering, but this
is defense in depth rather than permission to pass secrets to logging calls.
Tokens, cookies, connection strings, SQL/Redis arguments, entity snapshots,
evidence bodies, and tool secret values are forbidden at the call site and in
captured-record tests.

OpenTelemetry owns trace creation and W3C propagation. OTLP export is enabled
only when configured, but local span creation still supplies correlation IDs.
Sampling is bounded configuration with a parent-based ratio default. An
upstream sampling decision is honored only under the documented ingress trust
policy so an untrusted caller cannot force unbounded production sampling.
Export failures are observable but never fail an application request. Provider
shutdown flushes within its shutdown stage budget.

HTTP, PostgreSQL, Redis, and RabbitMQ instrumentation is created in the platform
layer. Application packages receive standard context values and do not import
vendor exporters. Cloud-specific endpoints, headers, gateways, and protocol
adapters remain runtime configuration outside the application architecture.

### Metrics

OpenTelemetry metrics cover HTTP request count/latency/in-flight work,
PostgreSQL and Redis pool saturation, RabbitMQ connection state, publish
confirmation latency/failure, consumer in-flight work, delivery age, retry and
dead-letter counts, outbox backlog/oldest age/blocked state/cleanup, job
duration/outcome, and telemetry export failures. Metric labels use stable
low-cardinality operation,
job kind, queue class, and outcome values; request, job, trace, actor, project,
and entity IDs never become metric labels. Logs retain state transitions and
diagnostics while metrics carry continuously varying pool and backlog state.

## PostgreSQL Boundary

### Connection And Query Stack

PostgreSQL uses `pgx/v5` and one bounded `pgxpool.Pool` per process. SQL is the
source of truth and `sqlc` generates typed query methods; an ORM and generic
repository are intentionally excluded. Pool configuration has explicit
connect, acquire, statement, idle, lifetime, health-check, minimum, and maximum
settings. Cancellation always flows from the request/job context.

Each feature owns SQL under its `adapter/postgres` package and maps generated
rows into domain values before returning. Domain and application packages see
feature-owned repository ports only. Generated code is never edited and never
used as an API response model.

### Schema Conventions

- Use one dedicated application database and ordinary `public` schema unless a
  later isolation requirement justifies another schema.
- Tables and columns use plural/singular `snake_case` names respectively;
  constraints and indexes have explicit deterministic names.
- Aggregate identifiers are application-generated UUIDv7 values. Foreign keys
  use the same type and declare deliberate update/delete behavior.
- Time is stored as `timestamptz` in UTC and exposed as RFC 3339. Each mutable
  aggregate has `created_at`, `updated_at`, and a monotonic `version` used for
  optimistic concurrency.
- PostgreSQL enums are avoided. Stable finite states use `text` plus a named
  check constraint; administrator-defined values use reference tables.
- `jsonb` is reserved for versioned external configuration/envelopes or
  evidence snapshots whose shape cannot be relationally stable. Queryable core
  fields remain normal columns, and each JSON document carries a schema
  version validated before persistence.
- Soft deletion is not a default. A feature adds `deleted_at` only when product
  semantics require restoration or historical visibility.
- Foreign keys, uniqueness, check constraints, and required indexes enforce
  invariants again at storage boundaries. Indexes are justified by named
  queries; speculative indexes are avoided.
- List APIs use stable keyset pagination, normally `(created_at, id)`, rather
  than unbounded reads or deep offset pagination.

### Repositories And Transactions

A repository interface belongs to the application package that consumes it
and contains use-case-specific methods, not generic CRUD. PostgreSQL adapters
may share generated `sqlc` query objects internally but cannot leak them.

The application use case owns transaction scope. A feature-specific unit of
work supplies transaction-bound repositories to a callback, commits only on a
nil result, and otherwise rolls back with the original error preserved.
Transactions are never hidden in context values and repository methods never
begin their own nested transaction. Cross-feature atomic operations require an
explicit orchestration design rather than direct table access.

The contract is shaped like this (names remain feature-specific):

```go
type TxOptions struct {
    Isolation Isolation
    ReadOnly  bool
    Retry     RetryPolicy
}

type Repositories struct {
    Incidents IncidentRepository
    Evidence  EvidenceRepository
}

type UnitOfWork interface {
    Within(ctx context.Context, opts TxOptions,
        fn func(context.Context, Repositories) error) error
}
```

`Within` begins one `pgx.Tx`, constructs transaction-bound `sqlc.Queries` and
repository adapters from that transaction, then passes only those repositories
to the callback. The callback cannot access the pool or commit/rollback. This
prevents accidental non-transactional queries in a transactional use case.

The state machine is explicit:

```text
begin failed -> return classified begin error
begin ok
  -> callback nil   -> commit -> return nil or classified commit error
  -> callback error -> rollback -> return original callback error
  -> panic          -> rollback -> re-panic for process recovery middleware
```

A rollback failure never replaces the original application error; it is
attached to the transaction log as a cleanup failure. A commit failure is the
operation result because commit outcome is unknown to the caller. Repository
methods must use the callback context so cancellation and deadlines cover the
whole transaction.

Default isolation is PostgreSQL `READ COMMITTED`. A use case must opt into
`REPEATABLE READ`, `SERIALIZABLE`, or read-only mode and document the invariant
that requires it. Nested transactions are prohibited; a rare partial rollback
must use an explicit, locally owned savepoint rather than making `Within`
silently nest.

Automatic retries are disabled by default. A use case may enable a bounded
whole-transaction retry only for classified serialization failures or
deadlocks. The callback must then be retry-safe: no network call, notification,
file write, random externally visible identifier, or other irreversible side
effect. Retry count and backoff are bounded and respect the parent deadline.

External calls do not run while a database transaction is open. An operation
that must atomically persist state and request asynchronous work writes a
versioned job envelope to the transactional outbox in the same transaction. A
worker relay leases committed rows in a short transaction, commits that claim,
then publishes them to RabbitMQ using persistent messages, mandatory routing,
and publisher confirms. It marks a row published only after a positive confirm.
Unknown or negatively confirmed outcomes remain retryable. Duplicate
publication is possible across connection failure, so consumers remain
idempotent.

Optimistic updates include `WHERE id = $1 AND version = $2`, increment the
version, and map zero affected rows to a stable conflict error. Expected unique,
foreign-key, serialization, cancellation, and timeout failures map to the
application error taxonomy; unknown database failures remain internal.

### Migrations

The migrate command embeds ordered, immutable SQL migrations and is the only
command that changes schema. API and worker verify compatibility but never
auto-migrate. Applied migration files are not edited; corrections use a new
migration. Each migration documents lock risk, transaction behavior, forward
compatibility, and whether down migration is data-safe. Destructive column or
constraint changes use expand/backfill/contract across releases.

### Database Observability And Safety

Query tracing records a declared low-cardinality operation name, duration,
outcome, and selected pool statistics. It never records bind arguments or raw
SQL. Missing operation names fall back only to a sanitized SQL verb such as
`SELECT` or `UPDATE`. Slow-query thresholds affect severity, not captured data.
OpenTelemetry spans use the same safe operation identity.

Database observability has three distinct event families:

| Event | Emitted for | Important fields |
|---|---|---|
| `db.query.completed` | one SQL operation | `db.operation.name`, SQL verb, duration, rows affected, outcome, error class, transaction ID |
| `db.transaction.completed` | one unit of work attempt | transaction ID, isolation, read-only, attempt, duration, commit/rollback outcome, error class |
| `db.pool.state` | startup, shutdown, health transition, saturation warning | acquired/idle/total counts, wait duration, health state |

Every event inherits service, environment, request or job ID, trace ID, and
span ID from context. Each transaction receives an internal correlation ID and
a transaction span; query spans are its children. The transaction ID is for
logs only and is never a business or database identifier.

Successful normal queries and transactions log at `DEBUG`; slow successful
operations log at `WARN`; expected conflicts/cancellations use a classified
`INFO` or `WARN` event; unexpected database failures log at `ERROR`. Thresholds
and successful-query logging are configurable, while error and slow-operation
events cannot be disabled.

The PostgreSQL adapter surrounds generated `sqlc` calls with a stable operation
name such as `incident.get_by_id`. The low-level pgx tracer supplies timing and
safe fallback SQL verb data. Neither layer emits SQL text, bind arguments,
connection URLs, credentials, returned row values, or JSON document bodies.
Batch and copy operations log aggregate counts only.

Logging is outcome-oriented rather than repeatedly printing the same error.
The query event records the storage attempt and safe database error class; the
transaction event records commit/rollback outcome; the HTTP/job completion
event records the use-case result. Only the outer recovery/error boundary adds
a stack for an unexpected internal failure.

Application diagnostic logs are separate from durable domain audit records.
When a later use case requires an audit record to be atomic with a state change,
its audit row is inserted through the same transaction-bound repositories; a
log line is never treated as the audit source of truth.

These events are application-side database access logs produced around pgx and
repository calls. PostgreSQL server logs are a separate infrastructure stream.
The connection sets a stable low-cardinality `application_name` such as
`fixthe-api` or `fixthe-worker`, allowing database operators to attribute load
by process. It does not place request IDs, trace IDs, actor IDs, or raw SQL
comments into `application_name` or query text; doing so would leak context,
pollute statement statistics, and create high-cardinality server logs.

Request-to-query correlation therefore lives in the application log and trace
stream. PostgreSQL server logs remain useful for engine-level facts such as
checkpoint, lock, connection, crash, and server slow-statement diagnostics,
but their configuration, shipping, retention, and redaction are outside this
deployment-neutral scaffold.

Integration tests require an explicit database name containing a test marker,
apply migrations from zero, isolate records by a per-test UUID, and clean only
owned records. Tests refuse to run destructive setup against an endpoint that
does not pass the isolation checks.

## Redis Boundary

Redis uses `go-redis/v9` with explicit dial, read, write, pool, idle, retry, TLS,
and database settings. Instrumentation records safe command names, duration,
outcome, and trace correlation but never command arguments or values.

The client is injected for disposable state only. No Redis-backed queue
interface, Redis worker consumer, business repository, or durability promise
is introduced. Later features define narrow cache/session/idempotency
interfaces at their consuming package boundaries and receive an implementation
backed by this client.

## RabbitMQ And Durable Job Boundary

RabbitMQ uses the RabbitMQ-maintained `amqp091-go` AMQP 0.9.1 client. One
instance-owned supervisor manages bounded reconnect backoff, heartbeats,
connection/channel replacement, topology redeclaration, publisher confirms,
consumer re-registration, readiness state, and shutdown. Application and domain
packages never import AMQP types.

The transport uses durable topic exchanges, durable production quorum queues,
persistent messages, mandatory routing, publisher confirms, manual consumer
acknowledgements, and bounded non-zero prefetch. Queue/exchange/binding names and
job routing keys are versioned, low-cardinality contracts. Broker policies own
mutable dead-letter and retention settings where possible; incompatible
pre-existing topology fails readiness with a safe diagnostic instead of being
silently replaced.

The delivery guarantee is at least once:

```text
application transaction
  -> business state + job_outbox row commit
  -> worker outbox relay
  -> persistent mandatory publish
  -> positive publisher confirm
  -> outbox marked published
  -> worker manual-ack delivery
  -> idempotent handler commits durable effect
  -> delivery acknowledged
```

The API never performs a PostgreSQL/RabbitMQ dual write. The outbox record is
the durable publication source until confirmed; RabbitMQ is the live transport,
not the job history or business source of truth. PostgreSQL retains bounded job
execution metadata and domain delivery/audit records where product semantics
require them.

### Outbox Relay Lease

An outbox row has an immutable publication ID and job ID plus its versioned envelope,
`available_at`, `attempt_count`, nullable `lease_token`, nullable `lease_owner`,
nullable `lease_until`, nullable `published_at`, nullable `blocked_at`, nullable
`cancelled_at`, a safe last-error class, and timestamps. Ready, leased, blocked,
cancelled, and published states are derived from these fields rather than
maintained as a second independently mutable status:

```text
ready     = published_at is null
            and blocked_at is null
            and cancelled_at is null
            and available_at <= database_now
            and (lease_until is null or lease_until <= database_now)
leased    = published_at is null and lease_until > database_now
            and blocked_at is null and cancelled_at is null
blocked   = published_at is null and blocked_at is not null
cancelled = published_at is null and cancelled_at is not null
published = published_at is not null
```

Database constraints reject contradictory terminal fields and require lease
fields to be set or cleared together. Published and cancelled rows cannot be
claimed again; a blocked row becomes ready only through an explicit requeue
operation that clears its block metadata after the underlying fault is fixed.

A relay claims a bounded batch in one short transaction. It selects ready IDs in
stable `(available_at, created_at, id)` order with `FOR UPDATE SKIP LOCKED`, then
sets a fresh cryptographically random lease token, instance owner, database-time
lease expiry, and increments `attempt_count`. The transaction returns the rows
and commits before any RabbitMQ call. The relay publishes the bounded batch with
bounded concurrency; configuration validation ensures the lease exceeds the
publisher-confirm deadline plus the maximum bounded local scheduling delay.

After a positive publisher confirm with no mandatory return, the relay sets
`published_at` only with `WHERE id = $1 AND lease_token = $2 AND published_at IS
NULL`. A failed or unknown publication similarly reschedules and clears the
lease only while the same token is current. Zero affected rows means the lease
expired or ownership changed; the stale relay records the safe outcome but does
not mutate the row. An abandoned claim becomes ready after `lease_until` using
PostgreSQL time, so recovery does not depend on synchronized worker clocks.

The unavoidable crash after broker confirmation but before `published_at` is
stored causes the same job ID to be published again after lease expiry. This is
an expected at-least-once window and is closed by consumer idempotency, not by
holding a PostgreSQL transaction open during broker I/O.

Connection loss, channel closure, publisher nack, confirm timeout, an unknown
confirm outcome, and a locally valid mandatory publication returned because the
expected route is temporarily unavailable are retryable. The relay clears its
current lease with compare-and-set, stores only a safe error class, and advances
`available_at` with exponential backoff plus jitter. Backoff is capped, but
attempt count is not: committed publication intent is never discarded because
infrastructure stayed unavailable for an arbitrary number of attempts.

Envelope corruption, an unsupported locally persisted schema version, or a
routing key that violates the application's declared topology contract is a
deterministic local failure. The current lease owner sets `blocked_at` using the
same token guard and stops automatic attempts for that row. Blocked rows remain
durable and observable while other rows continue. They are not broker dead
letters because RabbitMQ has not accepted them. Requeue and cancellation are
explicit operational decisions after inspection; neither happens through an
attempt threshold. Backlog age, retry count, blocked count, and state-transition
logs expose the condition without making one bad record trigger a restart loop.

The shipped `jobctl` command provides the initial recovery surface without
requiring direct SQL or an authenticated management API. `show` accepts one
outbox ID and returns bounded, redacted state and envelope metadata, never the
payload body, secret-bearing headers, or protected referenced content. `requeue`
accepts only a blocked row, clears its block metadata, and makes it ready while
preserving job ID and attempt/error history. `cancel` accepts only a ready or
blocked row and requires explicit confirmation. Neither operation can mutate a
leased or published row, delete history, bulk-select rows, or assign arbitrary
internal fields.

Every mutation requires the observed record version and a bounded operator
reason, executes as one compare-and-set transaction, and appends a durable
outbox-operation record in that transaction. A stale version returns a conflict
without changing either record. Until the later authenticated administration
layer supplies verified actor identity, the command records only declared
operator/process attribution and does not claim that value is authenticated.
It also emits a safe structured state-transition event without envelope content.
The later management API must call the same application service rather than
reimplementing these transition rules.

`jobctl job show` exposes redacted receipt, execution-generation, attempt,
disposition, and publication metadata for one `(consumer_name, job_id)`.
`jobctl job replay` accepts only a dead-lettered or exhausted job strictly inside
the 180-day replay horizon. In one compare-and-set transaction it appends a
single operator retry grant, increments `execution_generation`, and inserts one
new publication outbox row with the original job ID and one additional allowed
attempt. It never deletes the terminal receipt, resets attempt history, or turns
the grant into an unlimited retry override. A uniqueness constraint on the grant
and target generation makes command retry safe.

`jobctl reconcile show` exposes redacted metadata for one external operation.
`jobctl reconcile resolve` accepts only `reconciliation_required`: `requeue`
requires the adapter to now declare idempotency-key or lookup recovery and uses
the same generation/grant mechanism; `succeeded` or `failed` requires a durable
evidence reference and finalizes the feature-owned idempotency outcome. All
mutations require explicit confirmation, observed version, bounded reason, and
immutable action record. They never accept response bodies, credentials, or
free-form replacement state. Until the later security layer supplies verified
identity, command attribution remains declared operational identity rather than
an authenticated application actor.

Published outbox rows are operational diagnostics, not business history, and
are eligible for cleanup seven days after `published_at`. Cancelled rows and
durable outbox-operation records remain for 180 days after their respective
terminal/action times. Ready, leased, and blocked rows are never age-pruned.
Operation records retain immutable outbox and job identifiers for their full
retention even when the published outbox row has already been removed.

The worker performs cleanup with PostgreSQL-time cutoffs in small, rate-limited
batches selected in stable order with `FOR UPDATE SKIP LOCKED`. Each cleanup
transaction has a strict row and time bound, rechecks terminal predicates in
the delete, and cannot select an active lease. Cleanup records aggregate count,
duration, and outcome only. It does not publish a job for each deleted row and
does not provide job history or consumer deduplication; those remain separate
durable contracts.

`JobEnvelope` contains a job ID, execution generation, kind, schema version,
created time, deadline, idempotency key, actor/automation and correlation
metadata, trace propagation headers, and a small typed payload or durable payload
reference. Envelope and header sizes are bounded. Credentials, evidence bodies,
connector payloads, model prompts, and arbitrary user headers are forbidden;
jobs carry opaque references to protected PostgreSQL records instead.

Job identity has two independent layers. `job_id` is the immutable logical job
lineage. Normal outbox publication, delayed retry, and broker redelivery preserve
both job ID and execution generation, which defaults to zero. Only an explicit
operator replay grant may increment the generation while preserving job ID. A
durable consumer receipt is therefore unique by `(consumer_name, job_id,
execution_generation)`: separately named consumers may process fan-out, old
generation redeliveries remain terminal, and an authorized replay is distinguishable
without erasing history.

`idempotency_key` is business-operation identity. The owning application use
case, not transport or consumer infrastructure, creates it before writing the
outbox row. Jobs with an external or otherwise non-repeatable effect require the
key. Its namespace includes job kind, project or aggregate scope, and operation
kind. It is bound to a SHA-256 hash of the schema version plus canonical typed
payload or immutable durable-reference descriptor. Trace fields, timestamps,
attempt counters, broker headers, and other delivery metadata are excluded from
the hash. Payloads never contain secrets, so the hash is not used to legitimize
hashing secret material.

The same scoped key and payload hash is the same requested business operation,
even if a producer accidentally creates another job ID. The same scoped key
with a different hash is a deterministic idempotency conflict and cannot be
reported as an already-completed success. Consumers receive the persisted scope,
key, and hash; they never derive a key from arbitrary payload fields.

### Transactional Consumer Receipts

For a handler whose complete durable effect is in PostgreSQL, the application
use case owns one bounded transaction containing the deduplication checks and
business mutations. `job_delivery_receipts` is unique by `(consumer_name,
job_id, execution_generation)`. `job_idempotency_records` is unique by
`(scope_kind, scope_id, operation_kind, idempotency_key)` and stores the
canonical payload hash plus the terminal safe outcome. Consumer name is not part
of business identity, so a handler rename or routing migration cannot bypass the
invariant; independent fan-out effects declare different operation kinds. The
delivery receipt links to the business-idempotency record when one is required.
Separating the tables preserves each authorized delivery generation and one
business operation shared by accidentally duplicated job IDs or deliberate
replay.

The transaction follows this state machine:

```text
begin
  -> existing delivery receipt
       -> load its terminal outcome -> commit read -> acknowledge
  -> new delivery
       -> existing scoped key + different hash -> conflict -> terminal receipt + dead-letter intent
       -> existing scoped key + same hash      -> already applied -> terminal receipt
       -> new scoped key
            -> validate authoritative state
            -> apply business effect or classify stale
            -> write idempotency result + terminal delivery receipt
commit
  -> known success -> acknowledge
  -> durable terminal failure + dead-letter intent -> acknowledge
  -> rollback/failure/unknown outcome -> do not acknowledge
```

No `processing` receipt is committed before the local effect. A crash or panic
before commit rolls back receipt, idempotency record, and business mutations so
redelivery retries the whole operation. A crash after commit but before broker
acknowledgement leaves a terminal receipt that makes redelivery return the
stored result without repeating the effect. Concurrent duplicates serialize on
database uniqueness. A commit with an unknown result is never acknowledged;
redelivery determines whether the transaction became durable.

The application use case continues to own transaction scope. Its feature-
specific unit of work supplies transaction-bound business repositories and a
narrow job-receipt port to the callback. The worker runtime supplies envelope
metadata and receives only a classified result; neither application nor domain
contracts expose pgx, SQL, AMQP delivery tags, or acknowledgement methods.

### Consumer Execution Leases And Attempts

PostgreSQL, not message metadata, is authoritative for whether a named consumer
may start an attempt and whether its retry budget is exhausted.
`job_consumer_executions` is unique by `(consumer_name, job_id)` and stores the
current execution generation, opaque lease token/owner/database-time expiry,
next eligibility time, configured policy identity, and terminal receipt
reference when present. `job_consumer_attempts` is append-only and unique by
`(consumer_name, job_id, attempt_number)` with execution generation, start/end
time, safe outcome class, duration, and interrupted marker; it contains no
payload, credentials, or provider body.

Before invoking a handler, the worker runs a short transaction that locks or
creates the execution row, checks terminal receipt, replay horizon, next
eligibility, active lease, and the job-kind retry policy, then assigns a fresh
lease and appends the next attempt. It commits before handler work. A concurrent
duplicate cannot acquire a second active lease. An expired lease makes its
attempt durably interrupted before a new attempt is assigned. Every completion
update compares the current lease token, so a stale handler cannot overwrite a
newer attempt.

This execution lease is coordination metadata, not proof that a business effect
started or completed. Database-only handlers still commit effect and terminal
receipt atomically as defined above. External operations reuse the same attempt
and ownership primitive rather than creating a competing second lease. The
handler registry requires a bounded retry policy per job kind; attempt history
cannot be reset by message replay or an ordinary operator action.

There is no implicit handler retry default. Each descriptor declares attempt
limit, per-attempt timeout, retryable safe error classes, one of the platform's
approved fixed-delay tier sequences, maximum original job age and deadline
behavior, exhaustion disposition, whether business idempotency is mandatory,
and external recovery mode when applicable. Platform configuration supplies
hard ceilings, not fallback behavior. Registry construction rejects missing,
unknown, contradictory, or over-ceiling values, including a side-effecting
handler without business idempotency or a recovery mode incompatible with its
exhaustion disposition. The worker validates the complete immutable registry
before declaring readiness or starting any consumer.

Envelope attempt fields and RabbitMQ `x-death` data are retained only as
diagnostic inputs. Missing, reset, duplicated, or conflicting broker values
produce safe telemetry but never select the next attempt number, grant execution
ownership, or override PostgreSQL exhaustion. Retry and dead-letter replay keep
the original job ID and therefore continue the same authoritative history.

### External Side-Effect Operations

An external network or process effect cannot be atomically committed with
PostgreSQL. Such a handler persists an `external_operation` linked to the
delivery receipt and business-idempotency record before performing the call.
The row contains operation ID, consumer/job identity, scoped idempotency key and
payload hash, adapter kind, a stable external key or lookup identity, capability
mode, state, attempt metadata, opaque lease token/expiry, safe error class,
bounded provider metadata, and timestamps. It never contains credentials,
request/response bodies, evidence, prompts, or other protected content.

Every adapter declares exactly one recovery mode:

- `idempotency_key`: every attempt sends the same stable provider-supported key.
- `lookup`: a deterministic external identity can be queried and validated
  before creating or repeating the effect.
- `none`: the provider supports neither safe mechanism; an ambiguous attempt is
  never automatically repeated.

The persisted state machine is:

```text
pending -> in_flight -> succeeded
                     -> failed_retryable -> pending
                     -> failed_terminal
                     -> reconciliation_required
```

Creation of `pending` plus its non-terminal delivery receipt is one short
application-owned transaction. A worker claims the operation using a bounded
database-time lease and commits before network I/O. An idempotency-key operation
whose ownership is lost may be reclaimed and retried with the same external key.
A lookup operation is reclaimed only through a lookup-first reconciliation path.
An in-flight `none` operation whose result is unknown or whose lease expires
moves to `reconciliation_required`; even a crash after claim but before a known
result is treated conservatively because the database cannot prove whether the
external call occurred.

Known success is persisted with the terminal delivery and business-idempotency
outcome before RabbitMQ acknowledgement. A known terminal failure also inserts
its dead-letter publication intent in that transaction. A durable
`reconciliation_required` result is acknowledged as a terminal transport
disposition so broker redelivery cannot cause a blind second call; it remains an
unresolved operational condition exposed through logs and metrics until a later
authorized reconciliation workflow resolves it. A crash after terminal commit
but before acknowledgement is handled by the saved delivery receipt.

External calls never run inside a PostgreSQL transaction. Provider timeouts,
response validation, stable-key reuse, lookup result matching, lease recovery,
and the transition to manual reconciliation are owned by the adapter plus the
application use case, not by generic AMQP acknowledgement code.

### Consumer Retention And Replay Horizon

Terminal `job_delivery_receipts` remain for at least 180 days after completion
plus a configured cleanup grace interval. Retry and dead-letter replay validates
the immutable original job creation time and is permitted only while job age is
strictly less than 180 days. The grace interval ensures a replay accepted near
the boundary still observes its receipt. A job at or beyond the horizon cannot
be directly requeued with its old delivery identity; an authorized business
workflow must review current state and create a new job and idempotency decision.

`job_idempotency_records` are feature-owned durable invariants, not operational
receipts. The generic cleanup loop never deletes them by age. A feature retains
or removes one only with the owning business object and its documented retention
rules, so an expired transport receipt cannot reopen a completed remediation,
notification, or other non-repeatable operation.

Pending, in-flight, and `reconciliation_required` external operations are never
age-pruned. Succeeded and terminal-failed external-operation diagnostics remain
for 180 days, while the feature-owned business invariant survives according to
its own lifecycle. Cleanup uses PostgreSQL time, small batches, terminal-state
predicates, and safe aggregate telemetry; it never infers broker emptiness by
querying RabbitMQ during a database transaction.

The runtime provides no global, project, incident, aggregate, routing-key, or
queue-level FIFO guarantee. Relays and consumers may run concurrently, retries
may overtake newer work, and topology recovery may redeliver work in a different
order. A handler therefore loads authoritative PostgreSQL state and validates an
expected business version or explicit precondition before applying an effect.
Already-applied work is an idempotent success; stale work is a classified no-op;
conflicting current work uses database uniqueness or optimistic concurrency.
Ordering required by a future use case must be designed in that feature rather
than inferred from RabbitMQ delivery order.

Handlers are registered explicitly in the worker composition root. A handler
validates envelope version and deadline, loads authoritative state, performs an
idempotent application operation, and returns a classified result. It
acknowledges only after the corresponding durable success, retry, dead-letter,
or reconciliation disposition commits. A crash or channel loss before
acknowledgement can redeliver the message, so the `redelivered` flag is only a
hint and correctness never depends on it.

For every safely parsed delivery, the application transaction records one
classified disposition before the original message is acknowledged:

```text
success / already_applied / stale
  -> terminal receipt -> commit -> acknowledge
retryable
  -> attempt result + next eligibility + retry publication outbox -> commit -> acknowledge
permanent / exhausted
  -> terminal receipt + dead-letter publication outbox -> commit -> acknowledge
```

`job_dispositions` is unique by `(consumer_name, job_id, execution_generation,
attempt_number)` and stores exactly one classified disposition. Retry or dead-
letter outcomes reference at most one publication intent; every such intent
receives a new publication ID while retaining the original job ID, execution
generation, trace relationship, idempotency scope, and canonical payload hash.
Duplicate delivery after acknowledgement loss reads the saved disposition and
acknowledges without inserting another intent. The ordinary outbox relay
publishes retry intents to bounded fixed-delay tiers and
dead-letter intents to the durable dead-letter route using the same mandatory,
persistent, confirmed protocol as initial jobs. The delayed-message plugin is
not required.

If PostgreSQL cannot persist a disposition, the consumer does not acknowledge or
immediately hot-requeue the message. It stops or degrades intake so broker
redelivery occurs after recovery. Only a message whose bounded envelope identity
cannot be parsed has no safe database key; it is rejected without requeue to the
broker-configured dead-letter exchange and emits metadata-only diagnostics.
Retry tiers, attempt limits, backoff, per-kind timeout, prefetch, and concurrency
remain bounded configuration.

Periodic schedules remain durable application state in PostgreSQL. A
single-active scheduler claim creates normal outbox jobs when work becomes due;
RabbitMQ is not treated as a calendar or long-term schedule database.

The RabbitMQ principal is restricted to the configured vhost and application
topology. TLS verification stays enabled when TLS is configured. Connection
URLs, credentials, message bodies, headers, and payload references are never
logged. Traces propagate through allowlisted AMQP headers, and logs/metrics
correlate publish, delivery, attempt, acknowledgement, and dead-letter outcomes.

## Runtime Custom Tool Extension Model

Runtime tools and incident connectors are related but not identical. The tool
runtime owns safe invocation; a connector owns translation between a tool's
provider-specific result and the product's `Observation` or `Evidence`
contracts. This keeps provider mechanics out of incident-domain code.

Transport, capability, and execution target are modeled independently. MCP is a
transport adapter, not a synonym for read-only access: one MCP server may expose
`file.read`, another may expose `log.read`, and a later SSH MCP may expose both
`log.read` and `diagnostic.execute`. Policy authorizes each capability against a
project, environment, target, actor, and script review state. There is no generic
`shell.execute` capability.

```text
orchestrator/application use case
  -> capability + policy check
  -> ToolExecutor
  -> injected ToolRegistry
  -> versioned ToolFactory
  -> built-in adapter OR MCP/other external protocol adapter
  -> local sandbox OR remote execution target when execution is requested
  -> bounded typed ToolResult
  -> connector maps result to domain evidence
```

The kernel defines:

- `ToolDescriptor`: stable kind, version, transport, capabilities, input/output
  schema versions, supported execution targets, and operational limits.
- `ToolInvocation`: invocation ID, project/environment scope, operation,
  validated typed input, deadline, idempotency key when meaningful, actor or
  automation identity, and trace context.
- `ToolResult`: typed payload, provider metadata, truncation markers, safe
  diagnostics, and provenance needed by later evidence records.
- `ToolFactory`: validates non-secret configuration and builds one adapter from
  injected transports and secret references.
- `ToolRegistry`: an immutable instance-owned map keyed by kind and major
  version. Duplicate registration and ambiguous version resolution fail at
  startup; there is no global registry.
- `ToolPolicy`: independently decides whether a capability, project, endpoint,
  operation, and resource scope is allowed before any network or process call.
- `ToolExecutor`: applies deadlines, concurrency/output limits, tracing,
  stable error mapping, audit callbacks, and cancellation around the adapter.

Initial built-in adapters are compiled into the service. Declarative custom
HTTP/MCP definitions can later use vetted generic drivers and versioned JSON
configuration. Arbitrary user or AI-generated code is never loaded into the API
or worker via Go plugins, dynamic libraries, reflection hooks, or sourced shell
fragments. User application source returned by a file-reading MCP is inert input
for analysis and cannot be routed to an executor.

### Diagnostic Script Artifacts And Review

Diagnostic scripts vary by customer environment, so they are protected,
versioned artifacts rather than members of a fixed script allowlist. A script
artifact contains an ID and version, immutable content hash, protected content
reference, interpreter, typed parameter schema, compatible target descriptors,
generator/provenance metadata, risk flags, and review state. Review records the
reviewer, reviewed hash, policy version, decision, and timestamp. Editing script
content creates a new version in `unreviewed` state; approval can never float to
new bytes with the same display name.

Unreviewed artifacts may be hosted so an operator can inspect, compare, and
review AI output. They are visibly marked and isolated from approved versions.
Hosting is not proof of safety: generated scripts may destroy or corrupt data,
expose credentials, install persistence, exfiltrate information, or enable
lateral movement. The initial policy has no execution override for unreviewed
artifacts: execution requires human approval of the exact hash. A later policy
relaxation would require a separately reviewed security change rather than a
configuration toggle hidden inside the runner.

### Script Execution Boundary

An approved `diagnostic.execute` invocation passes only an artifact reference,
typed parameters, target reference, policy decision, deadline, and correlation
metadata to an injected out-of-process runner. The API and general worker never
interpret, source, or dynamically load the script. The runner retrieves the
authorized artifact through a protected channel and executes it using a clean
environment, isolated ephemeral workspace, least-privilege identity, explicit
interpreter/argument vector, and bounded CPU, memory, wall time, concurrency,
stdout/stderr, and result size. Timeout or cancellation terminates the complete
process tree. Filesystem writes and network access are denied by default and
require independently authorized capabilities; an implementation that cannot
enforce a required control fails closed.

RabbitMQ manages durable execution requests and outcomes but is not an artifact
or secret store. Its envelopes contain opaque references and bounded metadata,
never script bytes, SSH/MCP credentials, user source, log bodies, or execution
output. The runner stores bounded results through the protected persistence
boundary and returns a result reference. At-least-once delivery requires an
idempotent invocation claim so duplicate messages cannot start parallel copies
of the same execution unless the operation explicitly permits a new attempt.

A future SSH MCP adapter implements the same contracts. It can read logs and
execute the selected script on a remote target under a restricted account, with
host-key verification, target/egress policy, a per-invocation workspace,
resource and output limits, cancellation, and audit correlation. The MCP tool
accepts structured fields and an artifact reference rather than an arbitrary
shell command string. Uploading or staging script bytes occurs only inside the
authorized adapter/runner boundary and must verify the expected hash before
execution. The current MVP reserves this capability but does not implement the
SSH MCP executor.

Secrets are stored and resolved by the later security layer as opaque
references. They are supplied directly to the adapter transport and never enter
tool configuration responses, invocation payloads, logs, traces, audit values,
or model prompts. External-tool health is reported as integration status and
does not make the core API process unready.

An LLM cannot invoke the registry directly. It may propose a structured action;
an application use case validates it, applies policy, creates the invocation,
and retains normal audit/provenance behavior.

## Internal CLI And Developer Tools

Shipped operational commands use `cmd/<name>/main.go` only for signal/exit-code
handling and delegate to `internal/commands/<name>`. The command implementation
loads the same typed configuration, constructs only required dependencies, and
calls the same application services used by HTTP or worker entry points. It
does not duplicate SQL/business rules, import another `cmd`, or call the
service's private HTTP API merely to reuse behavior.

Commands declare stable exit codes, emit structured logs and traces, accept a
context deadline, and support dry-run/explicit confirmation before any future
destructive action. The migrate and restricted `jobctl` commands are the first
concrete examples.

Developer-only generators, linters, and schema/code generation dependencies
live under `tools/` and Makefile targets. They cannot be imported by production
packages or linked into API/worker binaries. Generated files identify their
source command and are checked for a clean regeneration diff in CI.

## HTTP And Error Contract

The initial router uses Go `net/http` and versioned `/api/v1` routing. System
endpoints expose `/livez` and `/readyz`. Readiness runs process-specific
dependency probes concurrently under a strict timeout and returns component
status without connection details or secrets. RabbitMQ is a worker readiness
dependency; the API exposes outbox backlog through metrics but does not probe or
depend directly on the broker.

Successful JSON uses the endpoint's typed representation rather than a generic
envelope. Errors use `application/problem+json` with stable type, title, HTTP
status, safe detail, instance, and request ID fields. Internal error causes are
logged once at the transport boundary and are not returned to clients. Unknown
routes, unsupported methods, malformed JSON, oversized bodies, timeouts, and
panics follow the same contract.

## Testing Strategy

Unit tests use constructor-injected fakes and in-memory OpenTelemetry exporters.
HTTP tests use `httptest` and captured slog records to prove correlation,
redaction, proxy trust, error mapping, and response behavior. PostgreSQL, Redis,
and RabbitMQ integration tests run only against explicit test endpoints supplied
by the caller; the repository does not provision those services.

Integration tests use unique namespaces and clean up only records/keys they
created. They must refuse non-test database names, Redis key prefixes, or
RabbitMQ vhosts/queue prefixes to avoid accidental destructive execution. Race
tests cover lifecycle, outbox relay, consumer, and middleware concurrency.

## Compatibility And Rollback

The repository is a single Git monorepo with `frontend/` and `backend/` package
roots. Establishing that layout moves the existing prototype package to
`frontend/` without changing its application behavior. The scaffold adds the
new `backend/` tree and does not couple either package's build toolchain to the
other.
Database migrations are forward-only during normal application startup because
only the explicit migrate command changes schema. Rollback consists of reverting
the backend binary/configuration and, when a migration was run, following that
migration's reviewed rollback procedure separately.

RabbitMQ consumption can be paused without deleting outbox rows or durable
business state. After broker/worker recovery, unpublished outbox rows resume
confirmed publication and consumers resume at-least-once handling. Dead-letter
replay is an explicit operational action with the original job ID and audit
metadata preserved, an incremented execution generation, and all prior receipts
and attempts left intact.

No deployment format is selected by this design. A later task may package the
same commands for its chosen runtime without changing application packages.
