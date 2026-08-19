# FixThe Backend

This directory contains the Go backend package. PostgreSQL is the source of truth
for users, projects, memberships, collection configuration, encrypted credentials,
observations, incidents, and audit events. Redis stores only revocable login
sessions. RabbitMQ, background workers, connector execution, durable jobs/outbox,
and remote telemetry export are deferred until a product use case requires them.

## Layout

```text
cmd/                         thin process entry points
internal/bootstrap/          composition roots and process lifecycle
internal/commands/migrate/   explicit schema migration runtime
internal/platform/           shared runtime infrastructure: PostgreSQL, Redis, local telemetry
internal/modules/auth/       local users, bcrypt passwords, Redis sessions, auth HTTP adapter
internal/modules/projects/   project access, members, configuration, credentials, audit
internal/modules/observations/ project-owned Event Stream
internal/modules/incidents/  project-owned incident lifecycle
internal/modules/system/     liveness and readiness vertical slice
tests/integration/           opt-in tests for caller-supplied dependency endpoints
tools/                       developer-only tool dependency module
```

Dependencies point inward: commands construct the process, inbound adapters
call module application code, and module code does not import process or
transport packages. Do not add generic `utils`, `common`, or global client
packages.

## Commands

Copy `.env.example` to `.env` in this directory and edit the local values.
Commands overlay that file onto the process environment when started from
`backend/`. A missing `.env` is ignored. Keys already exported in the shell
keep their process values.

Development defaults to the compact colored console logger. ANSI colors are
enabled only when stdout is a terminal, so redirected output stays color-free.
The console view uses component prefixes and short correlation IDs to keep each
record scannable; use JSON when complete trace, span, request, and stable event
identifiers are needed.
`test`, `staging`, and `production` default to JSON; set
`FIXTHE_LOG_FORMAT=console` or `FIXTHE_LOG_FORMAT=json` to override the
environment default explicitly.

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

The API and migration commands require `FIXTHE_POSTGRES_URL`. The API also
requires `FIXTHE_ENCRYPTION_KEY`, a standard-base64 encoding of 32 random bytes:

```bash
export FIXTHE_ENCRYPTION_KEY="$(openssl rand -base64 32)"
```

Keep this key stable for a database. Losing or changing it makes existing
credentials unusable by future connectors. The migration and administrator
bootstrap commands do not read it. The API opens a
bounded pool, verifies startup connectivity, exposes PostgreSQL through
readiness, and closes the pool during bounded shutdown.

The API also requires `FIXTHE_REDIS_URL`. Redis must pass startup `PING`, is
included in readiness, and closes before PostgreSQL. The migration command does
not read or connect to Redis. Redis is reserved for authenticated server sessions
in this MVP; it is not a queue or business source of truth.

Only `cmd/migrate` changes schema. It takes a session advisory lock, verifies
the SHA-256 checksum of immutable migration history, and applies each pending
embedded migration atomically with its history row. The API never runs migrations
automatically. Apply migrations explicitly before starting a new binary that
requires them:

```bash
FIXTHE_POSTGRES_URL='postgres://...' make run-migrate
```

Optional prototype data is kept outside migrations and API startup. The repository
ships a Go seed command, so no external `psql` client is required. Load it explicitly
after migrating; repeated execution is safe:

```bash
FIXTHE_POSTGRES_URL='postgres://...' make seed
```

Migration `000004` adds the project boundary and deterministically moves any
legacy global incidents into a legacy project. The unreleased scaffold previously
used migration version `000002` for an outbox table. Recreate any local development
database that applied that old version before running the current migrations. The
seed creates a complete demo project/configuration and project-owned incidents but
never creates users, memberships, passwords, or credentials.

## Authentication

After migration, create the first local administrator explicitly. The password
must contain 12 to 72 bytes and is read only by this one-shot command:

```bash
FIXTHE_POSTGRES_URL='postgres://...' \
FIXTHE_BOOTSTRAP_ADMIN_PASSWORD='replace-with-a-long-random-password' \
make bootstrap-admin USERNAME=admin
```

Running the command again for the same enabled administrator is safe and does
not replace its password hash. It does not connect to Redis. Normal API startup
never creates users or reads `FIXTHE_BOOTSTRAP_ADMIN_PASSWORD`.

The API stores only bcrypt password hashes in PostgreSQL. It stores a SHA-256
digest of each random Session token as the Redis key; the browser receives the
raw token only in the `fixthe_session` cookie. The cookie is `HttpOnly` and
`SameSite=Lax`. It is also `Secure` in `staging` and `production`, so those
environments must be served over HTTPS. `FIXTHE_AUTH_SESSION_TTL` defaults to
24 hours.

Authentication endpoints are:

| Method | Path | Behavior |
|---|---|---|
| `POST` | `/api/v1/auth/login` | Verify local credentials, rotate Session, set cookie |
| `GET` | `/api/v1/auth/me` | Return `id`, `username`, and `role` for the current Session |
| `POST` | `/api/v1/auth/logout` | Revoke the Session and clear the cookie |
| `POST` | `/api/v1/users` | System admin only: create an enabled local login with system role `viewer` |

System role and project role are separate. Only `users.role=admin` is a system
administrator. Accounts created through `/api/v1/users` receive system role
`viewer`; their project access is granted independently through membership APIs.
This prevents creating a project operator from accidentally granting global
administration.

All authentication responses use `Cache-Control: no-store`. Login and logout
accept JSON objects; logout uses an empty object. A local smoke flow is:

```bash
curl -c /tmp/fixthe-cookie.txt \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"replace-with-a-long-random-password"}' \
  http://127.0.0.1:8080/api/v1/auth/login

curl -b /tmp/fixthe-cookie.txt http://127.0.0.1:8080/api/v1/auth/me

curl -b /tmp/fixthe-cookie.txt \
  -H 'Content-Type: application/json' \
  -d '{"username":"oncall.operator","password":"replace-with-a-different-long-password"}' \
  http://127.0.0.1:8080/api/v1/users

curl -b /tmp/fixthe-cookie.txt \
  -H 'Content-Type: application/json' -d '{}' \
  http://127.0.0.1:8080/api/v1/auth/logout
```

## Projects, collection configuration, and Event Stream

Project is the data and authorization boundary. A non-member receives the same
`404 project_not_found` response as an unknown project, so project keys cannot be
enumerated. Project responses contain server-derived capabilities; clients must
not infer permissions from a local role switch.

| Project role | Read project/config/events/incidents/audit | Create/update incidents | Manage members/config/credentials |
|---|---:|---:|---:|
| `admin` | yes | yes | yes |
| `operator` | yes | yes | no |
| `viewer` | yes | no | no |

The current MVP persists one coherent configuration snapshot per project:

| Resource | Persisted fields |
|---|---|
| Environment | stable key, name, optional service |
| Git repository | remote URL, SCM provider, `https`/`ssh` transport, credential reference, production branch, deployed commit |
| Source | `ssh`, `cloud`, or `mcp`; typed config, credential reference, capabilities, enabled state |
| Trigger | `signed_webhook` or `custom_rule`; typed config, optional signing-secret reference, enabled state; signed webhook also stores a hashed inbound token |
| Credential | stable ID/name/kind and AES-256-GCM ciphertext/nonce; reads expose metadata only |

SSH configuration stores host, port, user, project folder, log path, and
`tail`/`snapshot` mode. MCP configuration stores endpoint, transport, safe headers,
evidence profile, query scope, and capabilities. Trigger configuration stores
webhook event types/deduplication key or a custom match expression/grouping window.
These records survive restart. This milestone still does not connect to SSH, Cloud,
or MCP, execute custom rules, or poll logs. Signed webhook ingress is live:
`POST /hooks/{token}` accepts the alert body without a Session and opens or
updates a `P2` incident. Authenticated `POST observations` remains the
temporary connector/development ingestion boundary.

Set `FIXTHE_PUBLIC_URL` to the absolute public origin used to display
`{publicURL}/hooks/{token}`. Project admins copy that URL from configuration;
rotate it with `POST /api/v1/projects/{projectKey}/configuration/webhook-token`.

Main project routes are:

```text
GET|POST /api/v1/projects
GET      /api/v1/projects/{projectKey}
GET|PUT|DELETE /api/v1/projects/{projectKey}/members[/{username}]
GET|POST /api/v1/projects/{projectKey}/secrets
GET|PUT  /api/v1/projects/{projectKey}/configuration
POST     /api/v1/projects/{projectKey}/configuration/webhook-token
GET|POST /api/v1/projects/{projectKey}/observations
GET|POST /api/v1/projects/{projectKey}/incidents
GET      /api/v1/projects/{projectKey}/incidents/{incidentId}
PATCH    /api/v1/projects/{projectKey}/incidents/{incidentId}/status
GET      /api/v1/projects/{projectKey}/audit-events
POST     /hooks/{token}
```

After logging in as the system administrator, create a project and grant the
previously created account an operator membership:

```bash
curl -b /tmp/fixthe-cookie.txt \
  -H 'Content-Type: application/json' \
  -d '{"key":"checkout-api","name":"Checkout API","description":"Production checkout service"}' \
  http://127.0.0.1:8080/api/v1/projects

curl -X PUT -b /tmp/fixthe-cookie.txt \
  -H 'Content-Type: application/json' -d '{"role":"operator"}' \
  http://127.0.0.1:8080/api/v1/projects/checkout-api/members/oncall.operator
```

Persist an initial Git, Cloud log, and custom-rule configuration. The IDs in the
response are stable references used by observations and incidents:

```bash
curl -X PUT -b /tmp/fixthe-cookie.txt \
  -H 'Content-Type: application/json' \
  -d '{
    "environment":{"key":"production","name":"Production","service":"checkout-backend"},
    "repository":{"remoteUrl":"https://git.example.internal/platform/checkout-api.git","scmProvider":"github","transport":"https","credentialSecretId":null,"productionBranch":"main","deployedCommit":"4f9c2b7"},
    "source":{"name":"production-logs","kind":"cloud","credentialSecretId":null,"config":{"schemaVersion":1,"provider":"tencent-cls","region":"ap-shanghai","resource":"checkout-logset"},"capabilities":["pull_collection"],"enabled":true},
    "trigger":{"name":"backend-errors","kind":"custom_rule","signingSecretId":null,"config":{"schemaVersion":1,"groupingWindowSeconds":900,"matchExpression":"level=ERROR service=checkout-backend"},"enabled":true}
  }' \
  http://127.0.0.1:8080/api/v1/projects/checkout-api/configuration
```

Create credentials separately, then use the returned ID as
`credentialSecretId` or `signingSecretId` in a configuration update. The response
never contains the value, ciphertext, or nonce:

```bash
curl -b /tmp/fixthe-cookie.txt \
  -H 'Content-Type: application/json' \
  -d '{"name":"git-http-prod","kind":"git_credential","value":"replace-with-token"}' \
  http://127.0.0.1:8080/api/v1/projects/checkout-api/secrets
```

Read the saved setup and project-owned operational data with:

```bash
curl -b /tmp/fixthe-cookie.txt http://127.0.0.1:8080/api/v1/projects/checkout-api/configuration
curl -b /tmp/fixthe-cookie.txt http://127.0.0.1:8080/api/v1/projects/checkout-api/observations
curl -b /tmp/fixthe-cookie.txt http://127.0.0.1:8080/api/v1/projects/checkout-api/incidents
curl -b /tmp/fixthe-cookie.txt http://127.0.0.1:8080/api/v1/projects/checkout-api/audit-events
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
set `FIXTHE_HTTP_CORS_ALLOWED_ORIGIN` to one exact console origin for cross-origin
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
database. The database name in `FIXTHE_TEST_POSTGRES_URL` must contain a
distinct `test` token, and `FIXTHE_TEST_POSTGRES_ISOLATION` must exactly equal
that database name. The test resets only the scaffold-owned migration history,
so supply a dedicated disposable database:

```bash
FIXTHE_TEST_POSTGRES_URL='postgres://.../fixthe_test?sslmode=disable' \
FIXTHE_TEST_POSTGRES_ISOLATION=fixthe_test \
go test -tags=integration ./tests/integration/...
```

Redis integration tests use the same tagged command and also fail closed. Set
`FIXTHE_TEST_REDIS_URL` plus a bounded `FIXTHE_TEST_REDIS_PREFIX` that contains
a distinct `test` token and ends in `:`. The test creates one random key and
deletes only that key; it never flushes or scans the selected database:

```bash
FIXTHE_TEST_POSTGRES_URL='postgres://.../fixthe_test?sslmode=disable' \
FIXTHE_TEST_POSTGRES_ISOLATION=fixthe_test \
FIXTHE_TEST_REDIS_URL='redis://:password@127.0.0.1:6379' \
FIXTHE_TEST_REDIS_PREFIX='fixthe:test:' \
go test -tags=integration ./tests/integration/...
```
