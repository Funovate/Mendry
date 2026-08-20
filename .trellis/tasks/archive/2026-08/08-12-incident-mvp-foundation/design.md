# Technical Design: Project-Scoped Incident MVP Foundation

## Boundary

The runnable MVP has one Go API, explicit migration and administrator-bootstrap
commands, PostgreSQL, Redis, and the existing React prototype. PostgreSQL is the
authoritative store for project configuration and project-owned business data.
Redis stores only authenticated sessions. Connector execution remains deferred.

```text
React UI -> auth session -> project resolver -> membership authorization
                                          |-> project configuration -> PostgreSQL
                                          |-> observations          -> PostgreSQL
                                          |-> incidents             -> PostgreSQL
                                          +-> audit events          -> PostgreSQL
```

No observation, incident, source, trigger, repository, secret, or audit query may
run without a resolved project identifier. The stable project key is accepted at
HTTP boundaries and resolved once to an internal UUID.

## Authorization Model

`users.role=admin` remains the system-administrator capability created by the
bootstrap command. A system administrator can create, discover, and administer
all projects. Other authenticated users require a `project_memberships` row.

Project roles are independent values with these capabilities:

| Capability | project admin | operator | viewer |
|---|---:|---:|---:|
| Read project/configuration/events/incidents/audit | yes | yes | yes |
| Create incidents and update lifecycle | yes | yes | no |
| Create/update project configuration and secrets | yes | no | no |
| Add/change/remove project members | yes | no | no |

Application services receive an authenticated principal and call a project access
repository before executing a use case. HTTP middleware authenticates sessions but
is not the sole authorization boundary. For non-system users, an unknown project
and a project without membership both map to the same not-found response.

The project creator receives an `admin` membership in the same transaction. Member
changes cannot remove or demote the last project admin unless a system administrator
is performing a corrective action and another admin already exists.

## Data Model

All aggregate UUIDs are application-generated UUIDv7 values. Timestamps use
`timestamptz`, mutable records use monotonic versions, and stable state sets use
named text check constraints.

```text
projects
  id, project_key, name, description, version, created_at, updated_at

project_environments
  id, project_id, environment_key, name, service, version, created_at, updated_at

project_memberships
  project_id, user_id, role, version, created_at, updated_at

project_secrets
  id, project_id, name, kind, ciphertext, nonce, key_version, version, created_at, updated_at

project_repositories
  id, project_id, remote_url, scm_provider, transport, credential_secret_id?,
  production_branch, deployed_commit, version, created_at, updated_at

project_sources
  id, project_id, environment_id, name, kind, credential_secret_id?, config jsonb,
  capabilities text[], enabled, version, created_at, updated_at

project_triggers
  id, project_id, environment_id, name, kind, signing_secret_id?, config jsonb,
  enabled, version, created_at, updated_at

observations
  id, project_id, environment_id, source_id, service?, occurred_at, level, message,
  host?, request_id?, fingerprint, attributes jsonb, ingested_at

incidents
  existing fields + project_id, environment_id, source_id

audit_events
  id, project_id, actor_user_id?, action, target_type, target_id?, summary,
  metadata jsonb, occurred_at
```

The source and trigger `config` documents are versioned connector-specific payloads.
Their adapters decode into typed request structures before persistence; arbitrary
client JSON is not copied blindly. Secret references are relational columns and are
validated to belong to the same project. Secret values never enter connector JSON.

Repository configuration is one row per project for this MVP. Environments, sources,
triggers, members, observations, incidents, and audit events can be plural. Source
and trigger names are unique inside a project. Incident fingerprints are unique per
project rather than globally.

## Migration Strategy

Migrations `000001` through `000003` may already be recorded by checksum and are
immutable. Migration `000004_project_scope.up.sql` creates the project-owned tables,
adds nullable ownership columns to `incidents`, backfills any existing incidents
into a deterministic `legacy` project/environment/source, changes the columns to
`NOT NULL`, adds foreign keys, and replaces the global fingerprint index with a
project-scoped unique index.

The legacy project is created only when old incident rows exist. Existing enabled
global administrators receive membership so migrated rows remain reachable. A clean
database receives no synthetic project. The migration is forward-only; rollback of
encrypted credentials and ownership relationships is not inferred from binary
rollback.

## Secret Handling

`FIXTHE_ENCRYPTION_KEY` supplies a base64-encoded 32-byte deployment key to API
startup. The API uses AES-256-GCM with a fresh random nonce for every secret write.
Additional authenticated data binds ciphertext to project ID, secret ID, kind, and
key version. PostgreSQL stores ciphertext and nonce but never plaintext.

Secret write requests accept a value; read/list responses expose only ID, name, kind,
version, and timestamps. There is no decrypt/read HTTP endpoint in this task.
Connector execution will later decrypt through a worker-owned internal interface.
Errors and audit metadata contain only secret IDs and names.

## HTTP Contract

The API surface is nested by stable project key:

```text
GET    /api/v1/projects
POST   /api/v1/projects
GET    /api/v1/projects/{projectKey}
GET    /api/v1/projects/{projectKey}/members
PUT    /api/v1/projects/{projectKey}/members/{username}
DELETE /api/v1/projects/{projectKey}/members/{username}
GET    /api/v1/projects/{projectKey}/configuration
PUT    /api/v1/projects/{projectKey}/configuration
GET    /api/v1/projects/{projectKey}/secrets
POST   /api/v1/projects/{projectKey}/secrets
GET    /api/v1/projects/{projectKey}/observations
POST   /api/v1/projects/{projectKey}/observations
GET    /api/v1/projects/{projectKey}/incidents
POST   /api/v1/projects/{projectKey}/incidents
GET    /api/v1/projects/{projectKey}/incidents/{incidentId}
PATCH  /api/v1/projects/{projectKey}/incidents/{incidentId}/status
GET    /api/v1/projects/{projectKey}/audit-events
```

Observation POST is an authenticated development/connector-boundary input in this
MVP and requires project admin/operator. It is not the future public signed webhook.
List endpoints use bounded limits and deterministic newest-first ordering. The old
global `/api/v1/incidents` routes are removed.

`PUT configuration` writes one coherent setup snapshot: environment, repository,
one source, and one trigger. The application validates cross-references and performs
the replacement in one database transaction so readers never observe a partially
updated setup. Secret creation/replacement is separate and supplies IDs referenced
by the snapshot.

Successful responses use resource JSON; errors keep the existing envelope:

```json
{"error":{"code":"invalid_request","message":"...","requestId":"..."}}
```

## Audit Contract

Project creation, membership changes, secret metadata changes, configuration writes,
incident creation, and incident status changes append an audit event in the same
transaction as the business mutation where practical. Audit summaries are fixed
application-owned phrases. Metadata uses allowlisted identifiers and state values;
request bodies and connector config documents are not copied into audit rows.

## Module Shape

`internal/modules/projects` owns project domain values, membership authorization,
configuration contracts, secret encryption orchestration, PostgreSQL adapters, and
project HTTP routes. `internal/modules/incidents` retains its domain/service/adapter
shape but all repository and service methods require resolved project access.
`internal/modules/observations` owns Event Stream persistence and HTTP mapping.

Shared `platform/httpserver` continues to own only cross-cutting mechanics. The
composition root constructs repositories, transaction runners, the secret cipher,
services, and handlers explicitly; there are no global clients.

## Frontend Sequencing

Frontend API integration pauses until project resolution, membership authorization,
configuration persistence, nested incidents, and Event Stream are complete and
tested. Once resumed, the active project comes from `GET /projects`; setup reads and
writes the configuration resource, incident screens use nested routes, and role
capabilities derive from the server response rather than a local role switch.

## Rollout And Rollback

Run migration `000004` before deploying the project-scoped API. API binaries that
still call global incident queries remain compatible with the added columns only
after backfill, but deployment should replace them immediately because global access
is an authorization defect. Rollback disables the new API binary while retaining
the forward schema; no destructive down migration is provided.
