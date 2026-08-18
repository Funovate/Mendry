# Database Guidelines

> Established PostgreSQL, typed-query, transaction, and migration conventions.

---

## Scenario: PostgreSQL Persistence Infrastructure

### 1. Scope / Trigger

Use this contract when adding or changing a PostgreSQL pool, health dependency,
query adapter, transaction boundary, migration, sqlc definition, or database
integration test.

- PostgreSQL uses `pgx/v5` and one bounded `pgxpool.Pool` per process.
- `internal/platform/postgres` owns pool lifecycle, safe tracing, pool metrics,
  health probes, and transaction primitives. It does not own business schema or
  repositories.
- Each feature owns SQL and generated code under its `adapter/postgres` package.
  The migrate command currently owns its metadata queries under
  `internal/commands/migrate/migratedb`.
- SQL is the source of truth and `sqlc` generates typed access methods. An ORM,
  generic repository, and mutable package-level database client are prohibited.
- Generated query and row types remain inside outbound adapters. Application
  and domain contracts must not expose `pgx`, `pgxpool`, or generated types.

### 2. Signatures

Current shared and operational contracts are:

```go
func config.LoadPostgreSQL(config.Lookup) (config.PostgreSQL, error)
func postgres.Open(context.Context, postgres.PoolOptions) (*postgres.Pool, error)
func postgres.NewQueryTracer(*slog.Logger, trace.Tracer, metric.Meter, time.Duration, bool) (*postgres.QueryTracer, error)
func (*postgres.Pool) Health(context.Context) error
func (*postgres.Pool) Close(context.Context) error

func postgres.NewTransactionRunner(postgres.transactionBeginner,
    *slog.Logger, trace.Tracer, time.Duration) (*postgres.TransactionRunner, error)
func (*postgres.TransactionRunner) Within(context.Context,
    postgres.TxOptions, func(context.Context, pgx.Tx) error) error

func migrate.NewRunner(migrate.RunnerOptions) (*migrate.Runner, error)
func (*migrate.Runner) Run(context.Context) error

func (*authdb.Queries).CreateUser(context.Context, authdb.CreateUserParams) (authdb.User, error)
func (*authdb.Queries).GetUserByID(context.Context, pgtype.UUID) (authdb.User, error)
func (*authdb.Queries).GetUserByUsername(context.Context, string) (authdb.User, error)

func (*incidentdb.Queries).CreateIncident(context.Context,
    incidentdb.CreateIncidentParams) (incidentdb.CreateIncidentRow, error)
func (*incidentdb.Queries).GetIncidentByNumber(context.Context,
    incidentdb.GetIncidentByNumberParams) (incidentdb.GetIncidentByNumberRow, error)
func (*incidentdb.Queries).ListIncidents(context.Context,
    incidentdb.ListIncidentsParams) ([]incidentdb.ListIncidentsRow, error)
func (*incidentdb.Queries).UpdateIncidentStatus(context.Context,
    incidentdb.UpdateIncidentStatusParams) (incidentdb.UpdateIncidentStatusRow, error)
```

`TransactionRunner.Within` is an infrastructure primitive. A feature adapter
wraps it and binds transaction-scoped repositories; application code receives
only feature repository contracts, not `pgx.Tx`.

### 3. Contracts

#### Configuration

| Key | Default | Constraint |
|---|---|---|
| `FIXTHE_POSTGRES_URL` | none | required single-line pgx connection string; never echoed |
| `FIXTHE_POSTGRES_CONNECT_TIMEOUT` | `5s` | 100 ms through 1 minute |
| `FIXTHE_POSTGRES_ACQUIRE_TIMEOUT` | `2s` | 100 ms through 1 minute |
| `FIXTHE_POSTGRES_STATEMENT_TIMEOUT` | `30s` | 100 ms through 10 minutes |
| `FIXTHE_POSTGRES_HEALTH_TIMEOUT` | `2s` | 100 ms through 30 seconds |
| `FIXTHE_POSTGRES_MIN_CONNS` | `1` | 0 through 100 and not above max |
| `FIXTHE_POSTGRES_MAX_CONNS` | `10` | 1 through 100 and not below min |
| `FIXTHE_POSTGRES_MAX_CONN_LIFETIME` | `30m` | 1 minute through 24 hours |
| `FIXTHE_POSTGRES_MAX_CONN_IDLE_TIME` | `5m` | 30 seconds through 1 hour |
| `FIXTHE_POSTGRES_HEALTH_CHECK_PERIOD` | `30s` | 1 second through 10 minutes |
| `FIXTHE_POSTGRES_SLOW_QUERY_THRESHOLD` | `500ms` | 10 ms through 1 minute |
| `FIXTHE_POSTGRES_QUERY_DEBUG` | `false` | `true` or `false`; when true, emitted query logs include one interpolated SQL statement |
| `FIXTHE_POSTGRES_MIGRATION_LOCK_TIMEOUT` | `30s` | 1 second through 10 minutes; migrate only |

All process configuration validates before pool construction. The API performs a
startup ping, includes PostgreSQL in readiness, and closes the pool in a
bounded stage before telemetry shutdown. Liveness remains process-only.

#### Queries And Observability

Normal feature queries use the pool's sqlc-compatible `DBTX` methods. Direct
connection acquisition is reserved for boundaries that require session
ownership, such as the migration advisory lock.

Adapters declare a stable low-cardinality operation with
`postgres.WithOperation`. The pgx tracer records operation, sanitized SQL verb
fallback, duration, outcome, rows affected, and trace correlation. By default
it never records SQL text, bind values, connection strings, database error
messages, returned rows, or high-cardinality identifiers.

`FIXTHE_POSTGRES_QUERY_DEBUG=true` is the only exception. `LoadPostgreSQL`
parses it with `enumValue` (`true`/`false`, default `false`) into
`config.PostgreSQL.QueryDebug`. `buildPoolConfig` passes that flag to
`NewQueryTracer`. When enabled, `TraceQueryStart` interpolates `$n`
placeholders into one executable statement and `TraceQueryEnd` attaches it as
`db.query.text`. Console writes that text on following physical lines; JSON
keeps the same string with escaped newlines. Levels stay unchanged, and
spans/metrics stay statement-free.

Interpolation replaces the complete placeholder (`$10` is not truncated by
`$1`). Strings are quoted and single-quotes doubled; `nil` and invalid
`pgtype` values become `NULL`; booleans become `TRUE`/`FALSE`; UUIDs and
timestamps become quoted literals. Bind values are not redacted.

PostgreSQL `safeError` values capture an `errtrace.Trace` when pool acquisition,
health, parse, ping, or close operations fail. Feature `repositoryError` values do
the same when adding repository operation context. These traces preserve the
active driver/pool/repository call path for the final HTTP/process boundary; lower
packages still return the error without logging it. Constructors are mandatory so
a direct struct literal cannot silently omit the trace.

#### Transactions

The application use case owns transaction scope. Repositories do not begin
nested transactions and transactions are not stored in context values.

```text
begin failure       -> return begin error
callback error      -> rollback, preserve callback error
callback panic      -> rollback, then re-panic
callback success    -> commit, return commit error if outcome is unknown
```

`READ COMMITTED` is the default. Stronger isolation and read-only mode are
explicit. Automatic retry is disabled unless the caller marks the whole
callback `RetrySafe`; only serialization and deadlock errors are retried, with
bounded attempts/backoff and parent-context cancellation. Network calls and
other irreversible effects never run inside a retried database transaction.

#### Migrations And Generation

`cmd/migrate` is the only schema-changing command. API never
auto-migrate. The runner embeds ordered `NNNNNN_name.up.sql` files, holds one
session advisory lock, checks immutable SHA-256 history, and commits each
migration together with its metadata row.

The history bootstrap is idempotent. Once applied, a migration file is
immutable; corrections use the next contiguous version. Each file documents
lock risk, transaction behavior, compatibility, and rollback safety. Schema
rollback is never inferred from binary rollback.

Every application-owned table and column has a semantic PostgreSQL comment.
Comments explain business meaning and non-obvious generation, sensitivity,
state, time, or concurrency semantics rather than translating identifiers.
Schema comments are the source of truth for comments on sqlc-generated models;
generated Go files are never patched to add or revise field documentation.
sqlc v1.31.1 propagates table and column comments to generated table models,
but not to query-specific `*Params` or `*Row` structs. Those structs are
adapter-internal generated transport types, not a second documentation source;
do not add a post-generation rewrite or manually patch them.

`backend/sqlc.yaml` owns generation. `backend/tools` pins sqlc and its newer Go
toolchain without raising the production module's Go version. `make generate`
regenerates queries; `make generate-check` compares before/after content hashes
and works before the repository has a Git baseline. Generated files are never
edited manually. Their standard English headers are an exception to the Chinese
source-comment rule; the package still has a hand-written Chinese `doc.go`.
Each target enables `omit_unused_structs`, so an auth query package does not also
export incident rows, or vice versa.

#### MVP Users, Project Scope, Incidents, And Seed

Migration `000002_create_mvp_data.up.sql` owns the first product schema:

- `users` uses an application-generated UUIDv7 primary key, unique normalized
  username, adaptive `password_hash`, role check for `admin`, `operator`, or
  `viewer`, enabled flag, monotonic version, and timestamps. It does not store a
  session token, plaintext password, email, reset state, or tenant fields.
- `incidents` uses an application-generated UUIDv7 primary key and a non-null
  identity `incident_number`. It stores only the current MVP list/detail fields:
  title, fingerprint, status, priority, source, seen timestamps, occurrence and
  host counts, mute state, notification summary, version, and timestamps.
- Incident status is `Open`, `Recovered`, or `Closed`; priority is `Info`, `P2`,
  or `P1`. Named checks enforce non-empty bounded text, ordered timestamps,
  positive counts, `host_count <= occurrence_count`, and positive versions.
- Username and incident number are globally unique; incident fingerprints are unique
  per project. The project list index follows `project_id, last_seen DESC,
  incident_number DESC`; no speculative indexes are added.

Migration `000004_project_scope.up.sql` adds projects, environments, memberships,
encrypted secrets, repositories, sources, triggers, Observations, and audit events.
It adds non-null project/environment/source ownership to incidents, backfills any
legacy rows through a deterministic legacy scope, and replaces global incident
queries and fingerprint uniqueness with project-scoped contracts. Migrations
`000001` through `000003` remain immutable.

Migrations `000005` through `000007` add incident coupling columns and the
remediation series/run/decision/plan/artifact/tool-invocation tables, then the
review-surface columns. Those files remain immutable after apply. Migration
`000008_document_remediation_schema.up.sql` adds only table and column comments
for the remediation relations; it does not change types, constraints, or
queries. Schema comments stay the source of truth for sqlc-generated models.

`dev/seed.sql` is an explicit, idempotent developer action invoked by `make seed`
after migrations. It may insert a stable demo project, configuration, and
project-owned incident fixtures but must never insert a user, membership, password,
or credential. `cmd/seed` embeds and executes this SQL in one transaction through
the existing PostgreSQL runtime, so no system `psql` binary is required. API and
migrate startup never execute it. Because the old,
unreleased outbox migration also used version 000002, a local database that
applied that file must be recreated rather than accepting mismatched history.

#### Schema And Integration Isolation

- Tables and columns use `snake_case`; constraints and indexes have explicit,
  deterministic names.
- Future aggregate IDs are application-generated UUIDv7; timestamps use
  `timestamptz`; mutable aggregates add a monotonic `version`.
- Stable states use `text` plus named checks, not PostgreSQL enums. `jsonb` is
  limited to versioned external or otherwise non-relational data.
- Lists use bounded keyset pagination. Storage constraints enforce invariants;
  speculative indexes and default soft deletion are avoided.
- Integration tests use the `integration` build tag and explicit
  `FIXTHE_TEST_POSTGRES_URL`. Its database name contains a distinct `test`
  token, and `FIXTHE_TEST_POSTGRES_ISOLATION` exactly matches that name.
- Missing integration settings fail instead of skip. Tests never provision a
  service and clean only schema or rows they explicitly own.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Missing/invalid PostgreSQL setting | Fail startup naming only the key and constraint |
| `FIXTHE_POSTGRES_QUERY_DEBUG` unset | Default `false`; query logs stay statement-free |
| `FIXTHE_POSTGRES_QUERY_DEBUG` is `true`/`TRUE` | Emitted `db.query.completed` records include interpolated `db.query.text` |
| `FIXTHE_POSTGRES_QUERY_DEBUG` is any other value | Fail `LoadPostgreSQL` naming the key; never echo the raw value |
| `FIXTHE_LOG_LEVEL=debug` with query debug off | Successful queries stay `DEBUG` and still omit SQL |
| URL parse/connect/ping failure | Return a stable safe wrapper; never echo URL or database message |
| Pool acquire or health timeout | Respect the earlier caller deadline and return a classified safe error |
| PostgreSQL error reaches an unknown HTTP 500 | Private log selects the deepest captured pool/repository stack |
| Callback returns an error | Roll back and preserve the callback error even if rollback also fails |
| Callback panics | Roll back with an independent cleanup deadline, then re-panic |
| Commit fails | Return commit error because durability is unknown |
| Retry requested without `RetrySafe` | Reject before beginning a transaction |
| Non-serialization/deadlock failure | Never retry automatically |
| Migration lock times out | Fail without applying a migration |
| Applied checksum differs | Fail before applying later migrations |
| UUID is not version 7 | Reject application-owned aggregate rows at the database boundary |
| Unknown user role, incident status, or incident priority | Reject the row through a named check constraint |
| Incident timestamps/counts violate ordering or positivity | Reject the row through a named check constraint |
| Seed is run repeatedly | Keep existing stable fixtures and complete successfully |
| Integration endpoint lacks test marker or exact isolation match | Fail before destructive SQL |

### 5. Good/Base/Bad Cases

- Good: a feature adapter wraps generated typed queries with a stable operation,
  maps rows into domain values, and exposes a use-case-specific repository.
- Base: API loads explicit PostgreSQL configuration, passes startup
  health, reports readiness, and closes its own pool on shutdown.
- Base: `make seed` loads a demo project/configuration and incident fixtures after
  an explicit migration and can be repeated without changing users or credentials.
- Good: `FIXTHE_POSTGRES_QUERY_DEBUG=true` plus `FIXTHE_LOG_LEVEL=debug` prints
  one interpolated statement after the query event so it can be pasted into
  `psql`.
- Bad: application code imports generated rows, a query logs raw SQL/arguments
  without the debug switch, API runs migrations on startup, or an integration
  test silently skips because isolation proof is missing.
- Bad: generated `models.go` is hand-edited to add field comments, or a custom
  post-generation script duplicates comments into query-specific structs.
- Bad: migration/API startup seeds rows, a seed creates a default password, or a
  feature query package generates models owned by another feature.

### 6. Tests Required

- Configuration: defaults, overrides, bounds, required URL, min/max relation,
  and proof that raw secret values never enter diagnostics.
- Pool: explicit limits/runtime params, startup health, acquire/health bounds,
  safe errors with captured call sites, readiness recovery, and bounded idempotent
  close.
- Tracing: declared operation and verb fallback, trace correlation, latency and
  outcome, rows affected, error class, and absence of SQL/args/error messages
  when query debug is off. When it is on: interpolated `db.query.text`, no
  separate args field, success stays `DEBUG`, and spans/metrics stay
  statement-free. Console asserts physical newlines and no quoted `sql=`.
- Query debug config: default `false`, `true`/`TRUE` accepted, invalid values
  fail without echoing the raw input, and `.env.example` documents the key
  once.
- Transactions: begin/commit/rollback, panic cleanup, callback-error priority,
  isolation/read-only mapping, retry classification/attempt limits, and parent
  cancellation.
- Migrations: filename ordering/gaps, checksums, advisory locking, typed history
  list/record, atomic migration metadata, rollback, connection release, four
  embedded versions, required MVP tables/checks, complete table/column comments,
  and absence of `job_outbox`.
- Seed: contains idempotent demo project/configuration/incident insertion and no
  user/membership/password/credential insertion.
- Integration: explicit test target validation, empty-history migration,
  idempotent rerun, typed history read, all 11 business relations, outbox absence,
  config/Observation/incident/audit behavior, project isolation, and owned cleanup
  in dependency order.
- Quality: `go vet ./...`, `go test ./...`, `go test -race ./...`,
  `go build ./cmd/...`, and `make generate-check`.

### 7. Wrong vs Correct

#### Wrong

```go
// Hidden global construction, raw SQL in application code, and leaked pgx type.
var pool = mustOpen(os.Getenv("FIXTHE_POSTGRES_URL"))

func (s *Service) Load(ctx context.Context) (pgx.Row, error) {
    return pool.QueryRow(ctx, "SELECT * FROM incidents WHERE id = $1", s.id), nil
}
```

#### Wrong

```text
sql="-- name: GetProjectAccess :one\nSELECT ... WHERE token = $1" args=[secret]
```

Quoted `sql=` hides newlines and leaves `$n` for the operator to substitute.

#### Correct

```text
2026-08-18 09:52:40 DBG [postgres] query completed operation=project.resolve_access
-- name: GetProjectAccess :one
SELECT ... WHERE token = 'secret'
```

Console writes the interpolated statement on following physical lines.

#### Correct

```go
// Composition root owns the pool; the adapter owns typed SQL and maps the row.
pool, err := postgres.Open(ctx, postgres.PoolOptions{
    Configuration: cfg.PostgreSQL,
    Application:   "fixthe-api",
    Logger:        logger,
    Tracer:        telemetry.Tracer("fixthe/backend/postgres"),
    MeterProvider: telemetry.MeterProvider(),
})

row, err := queries.GetIncident(
    postgres.WithOperation(ctx, "incident.get_by_id"), id,
)
return mapIncident(row), err
```

Development data follows the same explicit boundary:

```sql
-- Wrong: hidden startup credentials create an unsafe implicit account.
INSERT INTO users (username, password_hash) VALUES ('admin', 'plaintext');

-- Correct: dev/seed.sql contains stable project-owned demo data and remains repeatable.
INSERT INTO incidents (...) VALUES (...)
ON CONFLICT DO NOTHING;
```

## Common Mistakes

- Do not log raw SQL, bind arguments, connection strings, credentials, database
  error messages, or row values unless `FIXTHE_POSTGRES_QUERY_DEBUG` is
  explicitly enabled, and then only as one interpolated `db.query.text`.
- Do not replace `$1` before `$10`; match the complete placeholder.
- Do not leave debug SQL in a tint-quoted attribute. Console must write
  physical newlines so the statement can be copied.
- Do not construct a pool outside a composition root or keep mutable global
  database clients.
- Do not expose generated or pgx types through application/domain contracts.
- Do not edit an applied migration or generated sqlc file.
- Do not add a table or column without a semantic PostgreSQL comment; do not
  duplicate generated model documentation in a manually maintained file.
- Do not run API migrations implicitly or use a non-test endpoint for
  destructive integration setup.
- Do not make `incident_number` nullable: sqlc would then propagate pointer types
  into every generated incident query despite the number being a required API key.
- Do not put users, password hashes, or session material in development seed data.
- Do not retry a callback that performs network or irreversible external
  effects.
