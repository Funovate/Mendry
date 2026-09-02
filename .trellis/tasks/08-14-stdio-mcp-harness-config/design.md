# Technical Design: stdio MCP Harness Configuration

## Scope And Boundary

This task extends the persisted project source contract. It does not execute a
command or implement an MCP client. The resulting configuration is inert until
a separately owned harness runtime loads it.

```text
Configuration Studio / JSON import
  -> typed frontend draft
  -> encrypted credential writes for Secret env entries
  -> PUT project configuration
  -> domain + same-project reference validation
  -> PostgreSQL JSONB config + relational secret-reference projection
  -> typed configuration response

Future task only:
  configuration -> trusted stdio launcher -> MCP session -> harness tools
```

The production app remains independent of developer Codex MCP settings and does
not import prototype fixtures or components.

## Versioned MCP Contract

`schemaVersion: 1` remains valid because this is an additive transport variant
and existing remote records require no rewrite. The backend first decodes the
version and transport, then strictly decodes exactly one variant.

Existing remote shape remains unchanged:

```json
{
  "schemaVersion": 1,
  "transport": "streamable_http",
  "endpoint": "https://mcp.example.internal/mcp",
  "headers": {"X-Tenant": "payments"},
  "evidenceProfile": "errors-context",
  "queryScope": "project"
}
```

The normalized `stdio` shape is:

```json
{
  "schemaVersion": 1,
  "transport": "stdio",
  "command": "npx",
  "args": ["-y", "@example/log-mcp"],
  "cwd": "/srv/fixthe-tools",
  "env": {"LOG_PROJECT": "production"},
  "secretEnv": {"LOG_TOKEN": "019f..."},
  "evidenceProfile": "errors-context",
  "queryScope": "project"
}
```

`command` is required. `args`, `cwd`, `env`, and `secretEnv` are optional in
accepted input; the frontend normalizes arrays/maps to stable empty values and
omits an empty `cwd`. Remote-only fields (`endpoint`, `headers`) and stdio-only
fields cannot coexist. A stdio source also clears the source-level HTTP
credential reference; its secrets are represented per environment variable.

Validation is structural, not executable: bounded strings and collections,
valid environment-variable names, no NUL bytes, valid UUIDv7 secret references,
no overlap between `env` and `secretEnv`, and strict rejection of unknown fields.
It does not resolve executables, inspect the filesystem, start a process, or
claim that a connection works.

Recommended bounds are 2,048 bytes for command/cwd, 128 arguments with each
argument at most 4,096 bytes, and 64 combined environment entries with names
matching `[A-Za-z_][A-Za-z0-9_]*` and values at most 4,096 bytes. Existing
evidence-profile and query-scope bounds remain unchanged.

## Secret Contract

Add the credential kind `mcp_env`. Each secret environment variable references
one same-project credential by ID. The encrypted value remains available only
inside the credential repository; configuration reads expose ID/name/kind
metadata but never plaintext.

To retain the database-enforced project boundary, migration `000005` adds:

```text
project_source_secret_env
  project_id
  source_id
  variable_name
  secret_id
```

The table has one mapping per source and variable name, a composite foreign key
to the same-project source, and a composite foreign key to the same-project
credential. It is a referential-integrity projection of `config.secretEnv`; the
configuration JSON remains the API/runtime representation. Configuration upsert
replaces the source's projection in the same SQL statement/transaction as the
source snapshot. Reads compare the stored JSON mapping with the relational
projection and fail closed on drift.

The migration also replaces the named `project_secrets_kind_known` check with a
check that includes `mcp_env`; previously applied migrations remain immutable.
Application validation additionally requires every referenced credential to
have kind `mcp_env` before persistence.

Credential creation is intentionally separate from the transactional project
configuration update, matching the existing write-only credential flow. A
credential created during editing may remain unreferenced if configuration save
later fails; its metadata remains visible and reusable rather than being
silently deleted.

## JSON Import And Frontend State

The import surface accepts a common JSON document with exactly one
`mcpServers` entry. A missing `type` is inferred as `stdio` when `command` is
present; explicit `type` must be `stdio`. A URL-based entry is not converted to
stdio. Invalid JSON, multiple servers, command/url conflicts, non-string args,
or non-string environment values remain in the import view with an actionable
error and do not mutate the current Studio draft.

The outer server key becomes the project source name. `command`, `args`, and
`cwd` populate Studio directly. Every imported `env` entry becomes an unresolved
Secret draft by default. The administrator must review every row:

- Secret: store the transient value as an `mcp_env` credential through the
  existing credential API, clear plaintext immediately after success, and place
  the returned ID in `secretEnv`.
- Non-secret: explicitly switch the row and place its value in `env`.

Configuration Save stays disabled while an imported row is unreviewed or a
Secret row lacks a credential reference. Closing/resetting import, switching
source kind, or navigating away clears transient plaintext. Review renders
non-secret values and credential metadata only.

The production source editor gains `Local stdio` in the existing transport
control and transport-dependent fields. A focused MCP editor component owns
Studio/import mode and environment rows; it extends the existing
`CredentialField` for secret creation/selection rather than introducing a third
credential editor. No connection-test button or `allowedTools` editor is added.

## Backend Data Flow

1. The HTTP adapter strictly decodes the project configuration request.
2. Domain validation selects remote or stdio config by `transport` and validates
   only that variant.
3. The application service loads same-project credential metadata once when
   `secretEnv` is non-empty and verifies every ID and `mcp_env` kind.
4. The PostgreSQL adapter extracts the sorted mapping from the validated config,
   upserts the normal project snapshot, replaces the mapping projection, and
   writes the existing metadata-only audit event atomically.
5. The read query returns the config and projection; the adapter checks they
   match before returning the domain configuration.
6. The HTTP response returns raw versioned config containing credential IDs,
   never decrypted values.

Sorting map entries before SQL parameters and comparisons makes tests and
generated query behavior deterministic.

## Compatibility And Rollout

- Existing remote MCP JSON is accepted and returned byte-for-byte apart from
  existing PostgreSQL JSONB normalization.
- Existing rows have empty secret-env projections; no data backfill is needed.
- Deploy migration `000005` before the application binary. The migration is
  additive except for replacing the named secret-kind check.
- Before any `mcp_env` credentials are created, binary rollback is safe. After
  the new kind is in use, an older binary cannot decode that credential kind;
  rollback requires retaining the new binary contract or a deliberate data
  remediation, so normal recovery should forward-fix or disable stdio editing.
- Disabling/removing the UI does not delete saved configuration or credentials.

## Verification

Backend unit tests cover both strict union variants, bounds, field conflicts,
environment overlap, reference syntax, same-project/wrong-kind credential
validation, and remote compatibility. Migration/source tests and PostgreSQL
integration tests cover the new kind, mapping projection, transactional replace,
reload, and cross-project foreign-key rejection.

Frontend unit tests cover import normalization, default-Secret classification,
payload building, unresolved-draft gating, and secret clearing. Playwright covers
manual stdio entry, JSON import, secret storage, save/reload, review redaction,
remote/stdio switching, and absence of plaintext in configuration writes and
rendered post-save UI.
