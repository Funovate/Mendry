# Mendry Backend

This directory contains the Go Agent Harness foundation and the existing incident
application backend. `internal/modules/agentcore` defines and implements neutral
Run, model/tool, policy, completion, artifact, history, and persistence contracts
without importing incidents, projects, accounts, PostgreSQL, or Redis. The
account-free Local composition and adapters are still being validated; their
source presence is not a supported walkthrough or release claim.

The existing incident API remains a separate compatibility application.
PostgreSQL is its source of truth for the single login identity, projects, collection
configuration, encrypted credentials, observations, and incidents.
Redis stores only revocable login sessions. Its remediation coordinator uses
selected shared model/history mechanics but still owns the incident loop,
evidence gates, checkpoints, and lifecycle. RabbitMQ, background workers,
durable jobs/outbox, and remote telemetry export are deferred until a product
use case requires them.

## Layout

```text
cmd/                         thin process entry points; agentcore-local remains in validation
internal/bootstrap/          existing incident API composition and process lifecycle
internal/commands/migrate/   explicit incident-service schema migration runtime
internal/platform/           shared incident-service infrastructure: PostgreSQL, Redis, local telemetry
internal/modules/agentcore/  neutral Harness domain/application plus adapters in staged validation
internal/modules/auth/       local users, bcrypt passwords, Redis sessions, auth HTTP adapter
internal/modules/projects/   project lookup, configuration, credentials
internal/modules/observations/ project-owned Event Stream
internal/modules/incidents/  project-owned incident lifecycle
internal/modules/system/     liveness and readiness vertical slice
tests/integration/           opt-in tests for caller-supplied dependency endpoints
tools/                       developer-only tool dependency module
```

Dependencies point inward: commands construct the process, inbound adapters
call module application code, and module code does not import process or
transport packages. The neutral Harness core stays independent of incident
business modules and concrete infrastructure. Do not add generic `utils`,
`common`, or global client packages.

## Commands

Copy `.env.example` to `.env` in this directory and edit the local values.
Commands overlay that file onto the process environment when started from
`backend/`. A missing `.env` is ignored. Keys already exported in the shell
keep their process values.

The `MENDRY_*` names are the preferred configuration interface. During the
transition, every key also accepts its `FIXTHE_*` predecessor; a `MENDRY_*`
value wins even when it is explicitly empty. This keeps existing deployments
bootable while allowing new manifests and documentation to use Mendry names.

Development defaults to the compact colored console logger. ANSI colors are
enabled only when stdout is a terminal, so redirected output stays color-free.
The console view uses component prefixes and short correlation IDs to keep each
record scannable; use JSON when complete trace, span, request, and stable event
identifiers are needed.
`test`, `staging`, and `production` default to JSON; set
`MENDRY_LOG_FORMAT=console` or `MENDRY_LOG_FORMAT=json` to override the
environment default explicitly. Set `MENDRY_LOG_FILE=./logs/mendry.log` to
append the same records to a private local file while retaining stdout. The
parent directory is created automatically; rotate or remove the file through
the local process supervisor when needed.

```bash
cp .env.example .env
make run-api
make run-migrate
make bootstrap-admin USERNAME=admin
make seed
```

The API listens on `127.0.0.1:8080` by default:

```bash
curl http://127.0.0.1:8080/livez
curl http://127.0.0.1:8080/readyz
curl http://127.0.0.1:8080/api/v1/system/status
```

### Account-free local Agent Core runner

`agentcore-local` is an independent local composition. It does not require
PostgreSQL, Redis, an account, the incident API, or a background service. It
stores one durable JSON snapshot per run under the selected state directory and
uses an exclusive lock plus atomic fsync/rename updates.

Build both local binaries from `backend/`:

```bash
make build-agentcore-local build-mcp-workspace
```

The deterministic fixture provider is explicit and is intended for offline
onboarding and boundary tests; it is not real-provider validation. A report
example is available in `examples/agentcore-local/`:

```bash
./bin/agentcore-local run \
  --config examples/agentcore-local/fixture-report.config.json \
  --event examples/agentcore-local/report.event.json \
  --state-dir .agentcore/runs
```

The original flag-only form remains supported. The explicit subcommands are:

```bash
# Run, or require an existing run with --resume.
./bin/agentcore-local run --config config.json --event event.json --state-dir .agentcore/runs
./bin/agentcore-local run --resume --config config.json --event event.json --state-dir .agentcore/runs

# Read a validated snapshot without taking the execution lock.
./bin/agentcore-local inspect --state-dir .agentcore/runs --run-id <32-hex-run-id>

# Record an operator-confirmed result without loading or executing the tool.
./bin/agentcore-local resolve --state-dir .agentcore/runs \
  --run-id <32-hex-run-id> --invocation-id <invocation-id> \
  --state succeeded --code operator_confirmed \
  --output-json '{"status":"confirmed"}'
```

`config.json` is operator-supplied trusted composition. It selects a versioned
profile/completion contract, provider mode, restrictive policy, budgets, and
MCP bindings. `event.json` is bounded input only: version, source, external ID,
goal, payload, context, and optional occurrence time. Event data cannot select a
command, endpoint, credential, effect, policy, evaluator, or executor.
`maxElapsed` is encoded as a Go duration integer in nanoseconds when using JSON.
Equivalent effective configurations and events are normalized before hashing;
collection order, omitted restrictive defaults, nil/empty maps, and timestamp
formatting do not create identity drift.

The default restrictive policy advertises read tools only. Write effects require
an explicit `allowedEffects` entry and, for this example, a configured path root.
`examples/agentcore-local/restrictive-refusal.config.json` records a rejected
write without starting a write dispatch. The explicit allow-all example is
`allow-all-workspace.config.json`; it still uses trusted tool registrations,
intent persistence, budgets, and the real filesystem boundary:

```bash
./bin/agentcore-local run \
  --config examples/agentcore-local/allow-all-workspace.config.json \
  --event examples/agentcore-local/workspace.event.json \
  --state-dir .agentcore/runs
```

The workspace MCP server is a real stdio child in `examples/mcp-workspace`.
Its operator-selected `--root` owns traversal, symlink, size, parent-directory,
and atomic-write checks. Stdio children receive exactly the configured `env`
entries (plus no inherited process environment); `envRefs` and OpenAI `apiKeyEnv`
resolve through the injected process-boundary lookup. HTTP MCP `authEnv` is
resolved only for the trusted HTTP binding and is sent as a bearer header. No
secret is selected by event or model arguments.

The output envelope retains the run lifecycle, budget counters, ordered
invocations/results, provider-neutral paired messages, artifacts, and
provenance. A write whose result cannot be durably known becomes
`waiting`/`unknown_write_outcome` and is never replayed automatically. Inspect
the invocation, confirm the real-world result out of band, then use `resolve`
with only `succeeded` or `failed`. Resolution atomically appends the paired
operator observation, records the result, increments the run version, and
moves a waiting run to `running`; it never calls the configured tool. Running
`--resume` afterward continues only from that durable boundary.

### Local extension points

The neutral adapters are composition-only and do not know projects, accounts,
incident records, PostgreSQL, Redis, or secret stores:

- `adapter/openai`: inject an `openai.BindingLoader` into `openai.NewClient`.
  It maps logical names and exact `vN` versions to provider-safe function names,
  bounds history/schemas/arguments/responses, retries only model transport or
  temporary HTTP failures, and clears copied API-key buffers.
- `adapter/mcp`: inject an `mcp.BindingLoader` into `mcp.NewRuntime`, call
  `Discover`, and pass trusted `mcp.ToolBindingConfig` values to `BindTools`.
  The resulting executors perform one `tools/call`; MCP SDK reconnect retries
  are disabled. `mcp.ArtifactBinding` projects only successful structured
  responses into observed or explicitly linked verified artifacts.
- `application`: register a custom `domain.Profile`, `ToolExecutor`, or
  `CompletionEvaluator` in the instance-owned registries. Completion modes and
  versions are composition identities, not values supplied by an event.
- `adapter/file`: `file.Store` implements the durable local `RunStore` contract
  and exposes validated `Inspect`/snapshot data for operator tooling.

These APIs are intentionally neutral so a future service composition can map
its own credential and policy systems into them without changing the local
runner or importing business ownership models.

The API and migration commands require `MENDRY_POSTGRES_URL`. The API also
requires `MENDRY_ENCRYPTION_KEY`, a standard-base64 encoding of 32 random bytes:

```bash
export MENDRY_ENCRYPTION_KEY="$(openssl rand -base64 32)"
```

Keep this key stable for a database. Losing or changing it makes existing
credentials unusable by future connectors. The migration and administrator
bootstrap commands do not read it. The API opens a
bounded pool, verifies startup connectivity, exposes PostgreSQL through
readiness, and closes the pool during bounded shutdown.

The API also requires `MENDRY_REDIS_URL`. Redis must pass startup `PING`, is
included in readiness, and closes before PostgreSQL. The migration command does
not read or connect to Redis. Redis is reserved for authenticated server sessions
in this MVP; it is not a queue or business source of truth.

Only `cmd/migrate` changes schema. It takes a session advisory lock, verifies
the SHA-256 checksum of immutable migration history, and applies each pending
embedded migration atomically with its history row. The API never runs migrations
automatically. Apply migrations explicitly before starting a new binary that
requires them:

```bash
MENDRY_POSTGRES_URL='postgres://...' make run-migrate
```

Optional prototype data is kept outside migrations and API startup. The repository
ships a Go seed command, so no external `psql` client is required. Load it explicitly
after migrating; repeated execution is safe:

```bash
MENDRY_POSTGRES_URL='postgres://...' make seed
```

Migration `000004` adds the project boundary and deterministically moves any
legacy global incidents into a legacy project. The unreleased scaffold previously
used migration version `000002` for an outbox table. Recreate any local development
database that applied that old version before running the current migrations. The
seed creates a complete demo project/configuration and project-owned incidents but
never creates users, passwords, or credentials.

## Authentication

After migration, create the first local administrator explicitly. The password
must contain 12 to 72 bytes and is read only by this one-shot command:

```bash
MENDRY_POSTGRES_URL='postgres://...' \
MENDRY_BOOTSTRAP_ADMIN_PASSWORD='replace-with-a-long-random-password' \
make bootstrap-admin USERNAME=admin
```

Running the command again for the same enabled administrator is safe and does
not replace its password hash. It does not connect to Redis. Normal API startup
never creates users or reads `MENDRY_BOOTSTRAP_ADMIN_PASSWORD`.

The API stores only bcrypt password hashes in PostgreSQL. It stores a SHA-256
digest of each random Session token as the Redis key; the browser receives the
raw token only in the `mendry_session` cookie. A legacy `fixthe_session` cookie
remains readable and is cleared during login/logout migration. The cookie is
`HttpOnly` and `SameSite=Lax`. It is also `Secure` in `staging` and `production`,
so those environments must be served over HTTPS. `MENDRY_AUTH_SESSION_TTL`
defaults to 24 hours. Legacy Redis session keys are read and deleted, but new
sessions are written only under the `mendry:session:v1:` namespace.

Authentication endpoints are:

| Method | Path | Behavior |
|---|---|---|
| `POST` | `/api/v1/auth/login` | Verify local credentials, rotate Session, set cookie |
| `GET` | `/api/v1/auth/me` | Return `id` and `username` for the current Session |
| `POST` | `/api/v1/auth/logout` | Revoke the Session and clear the cookie |

The application supports one login identity and no roles or memberships. The
logged-in user can create and manage every project. A database unique index
prevents bootstrap-admin from creating a second account, including concurrent
bootstrap calls with different usernames.

Migration 000020 removes membership and audit data and the user role column.
It refuses databases with multiple users; select the account to retain before
upgrading. Existing single-account credentials remain unchanged.

All authentication responses use `Cache-Control: no-store`. Login and logout
accept JSON objects; logout uses an empty object. A local smoke flow is:

```bash
curl -c /tmp/mendry-cookie.txt \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"replace-with-a-long-random-password"}' \
  http://127.0.0.1:8080/api/v1/auth/login

curl -b /tmp/mendry-cookie.txt http://127.0.0.1:8080/api/v1/auth/me

curl -b /tmp/mendry-cookie.txt \
  -H 'Content-Type: application/json' -d '{}' \
  http://127.0.0.1:8080/api/v1/auth/logout
```

## Projects, collection configuration, and Event Stream

Projects scope configuration and operational data. Every authenticated user
request can access every project; an unknown project returns 404.

The current MVP stores at most one row for each configuration component per project. The editor saves environment, Git repository, source, trigger, and optional LLM provider independently; the complete configuration read is available once the required environment, repository, source, and trigger rows exist:

| Resource | Persisted fields |
|---|---|
| Environment | stable key, name, optional service |
| Git repository | remote URL, SCM provider, `https`/`ssh` transport, credential reference, production branch, deployed commit |
| Source | `ssh`, `cloud`, or `mcp`; typed config, credential reference, capabilities, enabled state |
| Trigger | `signed_webhook` or `custom_rule`; typed config, optional signing-secret reference, enabled state; signed webhook also stores a hashed inbound token |
| LLM provider | OpenAI-compatible base URL, credential reference, and selected model |
| Credential | stable ID/name/kind and AES-256-GCM ciphertext/nonce; reads expose metadata only |

The editor reads partial state from `GET /configuration/draft`. Each component write uses `PUT /configuration/{component}` and sends only that component's fields. The legacy complete `PUT /configuration` remains available for clients that already submit a full snapshot.
SSH configuration stores host, port, user, project folder, log path, and
`tail`/`snapshot` mode. MCP configuration stores endpoint, transport, safe headers,
evidence profile, query scope, and capabilities. Trigger configuration stores
webhook event types/deduplication key or a custom match expression/grouping window.
These records survive restart. This milestone still does not connect to SSH, Cloud,
or MCP, execute custom rules, or poll logs. Signed webhook ingress is live:
`POST /hooks/{token}` accepts the alert body without a Session, validates the
token, and returns `202 {"accepted":true}` before AI fingerprint extraction.
Normalization, Observation persistence, and `P2` incident ingestion continue
in the API process background. Authenticated `POST observations` remains the
temporary connector/development ingestion boundary.

Set `MENDRY_PUBLIC_URL` to the absolute public origin used to display
`{publicURL}/hooks/{token}`. The logged-in user copies that URL from configuration;
rotate it with `POST /api/v1/projects/{projectKey}/configuration/webhook-token`.

Main project routes are:

```text
GET|POST /api/v1/projects
GET      /api/v1/projects/{projectKey}
GET|POST /api/v1/projects/{projectKey}/secrets
GET      /api/v1/projects/{projectKey}/configuration
GET      /api/v1/projects/{projectKey}/configuration/draft
PUT      /api/v1/projects/{projectKey}/configuration
PUT      /api/v1/projects/{projectKey}/configuration/{environment|repository|source|trigger|llm}
POST     /api/v1/projects/{projectKey}/configuration/webhook-token
GET|POST /api/v1/projects/{projectKey}/observations
GET|POST /api/v1/projects/{projectKey}/incidents
GET      /api/v1/projects/{projectKey}/incidents/{incidentId}
PATCH    /api/v1/projects/{projectKey}/incidents/{incidentId}/status
POST     /hooks/{token}
```

After logging in, create a project:

```bash
curl -b /tmp/mendry-cookie.txt \
  -H 'Content-Type: application/json' \
  -d '{"key":"checkout-api","name":"Checkout API","description":"Production checkout service"}' \
  http://127.0.0.1:8080/api/v1/projects

```

Persist each configuration component independently. For example, saving a signed webhook does not require an LLM model:

```bash
curl -X PUT -b /tmp/mendry-cookie.txt \
  -H 'Content-Type: application/json' \
  -d '{"kind":"signed_webhook","signingSecretId":null,"config":{"schemaVersion":1,"eventTypes":["alarm"],"deduplicationKey":"title"},"enabled":true}' \
  http://127.0.0.1:8080/api/v1/projects/checkout-api/configuration/trigger
```

Create credentials separately, then use the returned ID as
`credentialSecretId` or `signingSecretId` in a configuration update. The response
never contains the value, ciphertext, or nonce:

```bash
curl -b /tmp/mendry-cookie.txt \
  -H 'Content-Type: application/json' \
  -d '{"name":"git-http-prod","kind":"git_credential","value":"replace-with-token"}' \
  http://127.0.0.1:8080/api/v1/projects/checkout-api/secrets
```

Read the saved setup and project-owned operational data with:

```bash
curl -b /tmp/mendry-cookie.txt http://127.0.0.1:8080/api/v1/projects/checkout-api/configuration
curl -b /tmp/mendry-cookie.txt http://127.0.0.1:8080/api/v1/projects/checkout-api/observations
curl -b /tmp/mendry-cookie.txt http://127.0.0.1:8080/api/v1/projects/checkout-api/incidents
```

The old global `/api/v1/incidents` routes are intentionally absent.

Configuration is loaded once at process startup. See `.env.example` for the
supported variables and defaults. Commands optionally overlay a cwd `.env`
through `config.WithOptionalDotEnv` before `LoadAPI` and the other loaders.
The overlay never calls `os.Setenv`, so later hidden `os.Getenv` reads still
cannot see file-only values. Invalid diagnostics name the field or file line
without printing its value.

The API boundary returns JSON for all errors, adds `X-Request-ID`, recovers handler
panics without exposing internal values, rejects request bodies over 1 MiB by
default, and strictly decodes JSON. Same-origin browser requests work by default;
set `MENDRY_HTTP_CORS_ALLOWED_ORIGIN` to one exact console origin for cross-origin
development with authenticated cookies.

## PostgreSQL and sqlc

`internal/platform/postgres` owns the process-local `pgxpool`, safe query
tracing, pool metrics, readiness probe, bounded close, and transaction runner.
Feature packages own their SQL and map generated rows before returning to
application code; application and domain contracts must not expose `pgx` or
generated types. There is no ORM or generic repository.

Migration SQL is embedded from
`internal/commands/migrate/migrations/*.up.sql`. Applied files are immutable;
fixes use a new monotonically numbered file. Query definitions live beside
their owner and generated files are never edited manually.

The production module stays on Go 1.25. The isolated `tools` module pins sqlc
and its toolchain separately. Regenerate and verify typed queries with:

```bash
make generate
make generate-check
```

## Redis

`internal/platform/redis` owns the standalone go-redis client, hard connection
pool cap, socket/context timeouts, bounded retries, TLS 1.2 minimum, startup and
readiness `PING`, command tracing, pool metrics, and shutdown. The client is for
disposable authenticated sessions. It is not a queue, cache layer, or source of
business truth in the current MVP.

Command telemetry uses only the Redis protocol command name and batch count. It
never reads command arguments or formatted command strings, so keys, values,
credentials, and server error messages do not enter logs, spans, or metrics.
The client disables go-redis maintenance notifications because endpoint
handoff and the library-level logger are outside this standalone contract.

The authentication feature defines the narrow session port at its own package
boundary. Application and domain code must not import go-redis types.

## Local Telemetry

Local OpenTelemetry providers and HTTP spans remain active so structured logs can
carry trace and span IDs. The MVP does not configure or connect to a remote OTLP
collector. Remote export can be added when there is an actual operational target.

## Validation

```bash
make check
make build
make generate-check
```

`make check` formats the module, runs static analysis, unit tests, race tests,
and verifies every shipped command builds.

PostgreSQL integration tests are opt-in and fail closed. They never provision a
database. The database name in `MENDRY_TEST_POSTGRES_URL` must contain a
distinct `test` token, and `MENDRY_TEST_POSTGRES_ISOLATION` must exactly equal
that database name. The test resets only the scaffold-owned migration history,
so supply a dedicated disposable database:

```bash
MENDRY_TEST_POSTGRES_URL='postgres://.../mendry_test?sslmode=disable' \
MENDRY_TEST_POSTGRES_ISOLATION=mendry_test \
go test -tags=integration ./tests/integration/...
```

Redis integration tests use the same tagged command and also fail closed. Set
`MENDRY_TEST_REDIS_URL` plus a bounded `MENDRY_TEST_REDIS_PREFIX` that contains
a distinct `test` token and ends in `:`. The test creates one random key and
deletes only that key; it never flushes or scans the selected database:

```bash
MENDRY_TEST_POSTGRES_URL='postgres://.../mendry_test?sslmode=disable' \
MENDRY_TEST_POSTGRES_ISOLATION=mendry_test \
MENDRY_TEST_REDIS_URL='redis://:password@127.0.0.1:6379' \
MENDRY_TEST_REDIS_PREFIX='mendry:test:' \
go test -tags=integration ./tests/integration/...
```
