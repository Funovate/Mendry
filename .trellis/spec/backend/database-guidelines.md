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
    incidentdb.CreateIncidentParams) (incidentdb.Incident, error)
func (*incidentdb.Queries).GetIncidentByNumber(context.Context, int64) (incidentdb.Incident, error)
func (*incidentdb.Queries).ListIncidents(context.Context, int32) ([]incidentdb.Incident, error)
func (*incidentdb.Queries).UpdateIncidentStatus(context.Context,
    incidentdb.UpdateIncidentStatusParams) (incidentdb.Incident, error)
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
fallback, duration, outcome, rows affected, and trace correlation. It never
records SQL text, bind values, connection strings, database error messages,
returned rows, or high-cardinality identifiers.

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

#### MVP Users, Incidents, And Seed

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
- Username, incident number, and fingerprint are unique. The list index follows
  `last_seen DESC, incident_number DESC`; no speculative indexes are added.

`dev/seed.sql` is an explicit, idempotent developer action invoked by `make seed`
after migrations. It may insert stable incident fixtures but must never insert a
user or credential. API and migrate startup never execute it. Because the old,
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
| URL parse/connect/ping failure | Return a stable safe wrapper; never echo URL or database message |
| Pool acquire or health timeout | Respect the earlier caller deadline and return a classified safe error |
| Callback returns an error | Roll back and preserve the callback error even if rollback also fails |
| Callback panics | Roll back with an independent cleanup deadline, then re-panic |
| Commit fails | Return commit error because durability is unknown |
| Retry requested without `RetrySafe` | Reject before beginning a transaction |
| Non-serialization/deadlock failure | Never retry automatically |
| Migration lock times out | Fail without applying a migration |
| Applied checksum differs | Fail before applying later migrations |
| UUID is not version 7 | Reject the user or incident row at the database boundary |
| Unknown user role, incident status, or incident priority | Reject the row through a named check constraint |
| Incident timestamps/counts violate ordering or positivity | Reject the row through a named check constraint |
| Seed is run repeatedly | Keep existing stable fixtures and complete successfully |
| Integration endpoint lacks test marker or exact isolation match | Fail before destructive SQL |

### 5. Good/Base/Bad Cases

- Good: a feature adapter wraps generated typed queries with a stable operation,
  maps rows into domain values, and exposes a use-case-specific repository.
- Base: API loads explicit PostgreSQL configuration, passes startup
  health, reports readiness, and closes its own pool on shutdown.
- Base: `make seed` loads only incident fixtures after an explicit migration and
  can be repeated without changing credentials.
- Bad: application code imports generated rows, a query logs raw SQL/arguments,
  API runs migrations on startup, or an integration test silently skips because
  isolation proof is missing.
- Bad: generated `models.go` is hand-edited to add field comments, or a custom
  post-generation script duplicates comments into query-specific structs.
- Bad: migration/API startup seeds rows, a seed creates a default password, or a
  feature query package generates models owned by another feature.

### 6. Tests Required

- Configuration: defaults, overrides, bounds, required URL, min/max relation,
  and proof that raw secret values never enter diagnostics.
- Pool: explicit limits/runtime params, startup health, acquire/health bounds,
  safe errors, readiness recovery, and bounded idempotent close.
- Tracing: declared operation and verb fallback, trace correlation, latency and
  outcome, rows affected, error class, and absence of SQL/args/error messages.
- Transactions: begin/commit/rollback, panic cleanup, callback-error priority,
  isolation/read-only mapping, retry classification/attempt limits, and parent
  cancellation.
- Migrations: filename ordering/gaps, checksums, advisory locking, typed history
  list/record, atomic migration metadata, rollback, connection release, three
  embedded versions, required MVP tables/checks, complete table/column comments,
  and absence of `job_outbox`.
- Seed: contains idempotent incident insertion and no user/password insertion.
- Integration: explicit test target validation, empty-history migration,
  idempotent rerun, typed history read, users/incidents existence, outbox absence,
  generated create/update query behavior, and owned cleanup in dependency order.
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

-- Correct: dev/seed.sql contains stable incidents only and remains repeatable.
INSERT INTO incidents (...) VALUES (...)
ON CONFLICT DO NOTHING;
```

## Common Mistakes

- Do not log raw SQL, bind arguments, connection strings, credentials, database
  error messages, or row values.
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
