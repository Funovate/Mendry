# Directory Structure

> Current backend process, package, and configuration contracts.

---

## Scenario: API And Migration Scaffold

### 1. Scope / Trigger

Use this contract when changing a backend command, composition root, environment
setting, shared runtime package, or health endpoint.

The current runtime contains only the API and explicit migration commands. The API
owns PostgreSQL, Redis, HTTP, structured logging, and local OpenTelemetry providers.
RabbitMQ, workers, durable jobs/outbox, and remote telemetry export are deferred and
must not be added without a concrete product consumer.

### 2. Signatures

```go
func bootstrap.RunAPI(context.Context, bootstrap.Options) error
func bootstrap.RunMigrate(context.Context, bootstrap.Options) error
func bootstrap.RunSeed(context.Context, bootstrap.Options) error
func bootstrap.RunBootstrapAdmin(context.Context, bootstrap.BootstrapAdminOptions) error

func config.LoadAPI(config.Lookup) (config.API, error)
func config.LoadMigrate(config.Lookup) (config.Migrate, error)
func config.LoadSeed(config.Lookup) (config.Seed, error)
func config.LoadBootstrapAdmin(config.Lookup) (config.BootstrapAdmin, error)
func config.LoadPostgreSQL(config.Lookup) (config.PostgreSQL, error)
func config.LoadRedis(config.Lookup) (config.Redis, error)
func config.WithOptionalDotEnv(config.Lookup, string) (config.Lookup, error)

func observability.NewTelemetry(context.Context,
    observability.TelemetryOptions) (*observability.Telemetry, error)
```

`bootstrap.Options` injects environment lookup, log output, and build identity. Tests
do not mutate process environment or global loggers.

Command `main` packages wrap `os.LookupEnv` with `config.WithOptionalDotEnv(..., ".env")`
before constructing bootstrap options. The overlay reads a dotenv file from the process
cwd, ignores a missing file, and never overwrites a key already present in the base
lookup. It does not call `os.Setenv`. Tests pass an explicit lookup or temp path and do
not depend on a repository `.env`.

### 3. Contracts

Commands:

| Command | Contract |
|---|---|
| `go run ./cmd/api` | Requires PostgreSQL and Redis; serves HTTP and drains Redis, PostgreSQL, and local telemetry in bounded stages |
| `go run ./cmd/migrate` | Requires PostgreSQL only; applies embedded migrations under an advisory lock |
| `go run ./cmd/seed` | Requires PostgreSQL only; atomically applies the embedded idempotent development seed |
| `go run ./cmd/bootstrap-admin --username <name>` | Requires PostgreSQL and command-only password; creates or confirms one admin without Redis |

System routes:

| Method/path | Success |
|---|---|
| `GET /livez` | Success envelope with `data.status = "alive"` |
| `GET /readyz` | Success envelope with `data.status = "ready"` when PostgreSQL and Redis pass |
| `GET /api/v1/system/status` | Same readiness report and success envelope |

Environment groups:

- Common: `FIXTHE_ENVIRONMENT`, `FIXTHE_LOG_LEVEL`, `FIXTHE_LOG_FORMAT`, and
  bounded shutdown timeout.
- API HTTP: bind address plus read-header/read/write/idle timeouts.
- HTTP boundary: `FIXTHE_HTTP_MAX_BODY_BYTES` (default 1 MiB) and optional exact
  `FIXTHE_HTTP_CORS_ALLOWED_ORIGIN`.
- Auth: bounded `FIXTHE_AUTH_SESSION_TTL`; command-only
  `FIXTHE_BOOTSTRAP_ADMIN_PASSWORD` is never read by API startup.
- Project credentials: API-required `FIXTHE_ENCRYPTION_KEY` decodes from standard
  base64 to exactly 32 bytes; migrate and bootstrap-admin do not read it.
- PostgreSQL: required secret-bearing URL plus explicit pool and health bounds,
  including optional `FIXTHE_POSTGRES_QUERY_DEBUG`.
- Redis: API-required secret-bearing URL plus explicit pool, retry, and health bounds.
- Migration: PostgreSQL migration lock timeout; no Redis connection.
- No RabbitMQ, worker, outbox, or OTLP exporter environment keys are supported.

Local telemetry is instance-owned. It continues W3C trace context and provides
in-process trace/metric APIs for HTTP, PostgreSQL, and Redis instrumentation. It does
not mutate OpenTelemetry globals or connect to a collector.

Directory layout:

```text
backend/
  cmd/{api,migrate,seed,bootstrap-admin}/main.go
  internal/
    bootstrap/
    commands/migrate/{migrations,migratedb}/
    platform/{buildinfo,config,errtrace,httpserver,observability,postgres,redis}/
    modules/{auth,projects,observations,incidents,remediation,system}/
  tests/integration/
  tools/
```

Create feature packages only with current behavior under
`internal/modules/<feature>/{domain,application,adapter/...}`. Do not commit
placeholder packages, generic `utils`, or mutable global clients.

`internal/platform/errtrace` is the narrow shared exception to feature ownership:
it captures and formats bounded error stacks without importing HTTP, logging, or a
feature. PostgreSQL, Redis, and feature adapters may import it; application/domain
packages do not depend on transport diagnostics.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Missing PostgreSQL URL | API and migrate fail before resource construction; raw value is never echoed |
| Missing Redis URL | API fails with a safe error naming `FIXTHE_REDIS_URL` |
| Invalid HTTP/pool/timeout value | Fail before opening clients or listener |
| Invalid body limit or CORS origin | Fail before opening clients or listener |
| PostgreSQL or Redis startup health fails | Close partial resources and fail safely |
| Dependency fails after startup | Readiness returns 503 naming only the stable dependency; liveness stays 200 |
| API context cancels | Stop HTTP, then close Redis, PostgreSQL, and local telemetry with independent bounds |
| Migrate/seed context cancels or fails | Roll back active work and close PostgreSQL/local telemetry |

### 5. Good/Base/Bad Cases

- Good: API receives explicit PostgreSQL and Redis endpoints, validates all config,
  opens one client of each, and reports readiness only after both pass.
- Base: migrate receives only PostgreSQL configuration and never reads Redis.
- Bad: adding a worker command, broker dependency, exporter, or placeholder package
  before a current product flow requires it.

### 6. Tests Required

- Config tests cover required API Redis, PostgreSQL requirements, typed bounds, and
  diagnostics that omit raw secret values.
- Dotenv tests cover missing files, process-environment precedence, comments, quotes,
  empty values, and malformed lines that omit raw assignment values.
- Lifecycle tests cover partial startup cleanup, readiness, cancellation, and
  Redis-before-PostgreSQL shutdown.
- Migration tests cover ordering, checksums, locks, atomic history, and integration
  isolation.
- Quality gate: `make check`, `make build`, and `make generate-check`.

### 7. Wrong vs Correct

#### Wrong

```go
var client = newClient(os.Getenv("FIXTHE_REDIS_URL"))
```

#### Correct

```go
cfg, err := config.LoadAPI(options.Lookup)
if err != nil {
    return fmt.Errorf("load API configuration: %w", err)
}
client, err := openRedis(ctx, logger, telemetry, "fixthe-api", cfg.Redis)
```

## Module Organization

- Commands import bootstrap; substantive behavior stays under `internal`.
- Bootstrap alone constructs complete processes.
- Only `cmd/migrate` imports the schema-change workflow; API never auto-migrates.
- Application/domain packages do not import HTTP, environment, pgx, go-redis, or
  command packages.
- Feature adapters own concrete persistence clients and map concrete rows/results
  before returning through feature-owned contracts.
