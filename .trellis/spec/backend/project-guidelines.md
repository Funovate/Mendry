# Project Boundary Guidelines

> Established project authorization, configuration, credential, Observation, and audit contracts.

---

## Scenario: Project-Scoped Operational Data

### 1. Scope / Trigger

Use this contract when changing projects, memberships, project discovery,
credentials, Git/source/trigger configuration, Observations, audit events, or any
resource that can reveal one customer's operational data.

- Project is the mandatory ownership and authorization boundary.
- PostgreSQL stores project configuration and business data; Redis never stores it.
- The authenticated user's system role and membership role are independent.
- Connector execution, polling, public webhook ingress, and automatic rule evaluation
  are not implemented yet. The stored configuration is future runtime input.

### 2. Signatures

Application access is resolved before a project-owned repository is called:

```go
func (*projects.Service).ListProjects(context.Context, authdomain.User, int32)
    (application.ListResult[projectdomain.Project], error)
func (*projects.Service).GetProject(context.Context, authdomain.User, string)
    (projectdomain.Project, error)
func (*projects.Service).UpdateProjectName(context.Context, authdomain.User,
    string, string) (projectdomain.Project, error)
func (*projects.Service).UpsertMember(context.Context, authdomain.User,
    string, string, string) (projectdomain.Member, error)
func (*projects.Service).CreateSecret(context.Context, authdomain.User,
    string, string, string, []byte) (projectdomain.Secret, error)
func (*projects.Service).UpdateSecret(context.Context, authdomain.User,
    string, string, string, []byte) (projectdomain.Secret, error)
func (*projects.Service).PutConfiguration(context.Context, authdomain.User,
    string, projectdomain.Configuration) (projectdomain.Configuration, error)
func (*projects.Service).ProbeRepositoryRefs(context.Context, authdomain.User,
    string, string, string, string) (application.RepositoryRefs, error)

func (*observations.Service).List(context.Context, authdomain.User, string, int32)
    (observations.ListResult, error)
func (*observations.Service).Create(context.Context, authdomain.User, string,
    observations.CreateInput) (observationdomain.Observation, error)
```

HTTP routes:

```text
GET|POST /api/v1/projects
GET|PATCH /api/v1/projects/{projectKey}
GET      /api/v1/projects/{projectKey}/members
PUT|DELETE /api/v1/projects/{projectKey}/members/{username}
GET|POST /api/v1/projects/{projectKey}/secrets
PATCH    /api/v1/projects/{projectKey}/secrets/{secretId}
GET|PUT  /api/v1/projects/{projectKey}/configuration
GET|POST /api/v1/projects/{projectKey}/observations
GET      /api/v1/projects/{projectKey}/audit-events
```

### 3. Contracts

#### Authorization

`users.role=admin` is the system administrator created by bootstrap. System admins
can create and administer every project. Other enabled users discover a project only
through `project_memberships`.

| Capability | project admin | operator | viewer |
|---|---:|---:|---:|
| Read project/configuration/Observations/incidents/audit | yes | yes | yes |
| Create Observations/incidents and update incident status | yes | yes | no |
| Manage members, configuration, and credentials | yes | no | no |

Every use case resolves the stable project key to an internal project UUID and checks
access in the application layer. HTTP authentication is defense in depth. Unknown
projects and projects without membership both return `404 project_not_found` for a
non-system user. Responses contain server-derived `capabilities`; clients do not
derive them from local state.

#### Persistence

Migration `000004_project_scope.up.sql` owns `projects`, `project_environments`,
`project_memberships`, `project_secrets`, `project_repositories`, `project_sources`,
`project_triggers`, `observations`, and `audit_events`, and adds project/environment/
source ownership to `incidents`. Existing incidents are backfilled into a
deterministic legacy project only when legacy rows exist.

The MVP storage constraints and configuration endpoint intentionally keep one
environment, one repository, one source, and one trigger snapshot per project.
Supporting multiple environments or connectors requires a later forward migration
plus item-addressed API contracts; table names alone do not imply that support.

- Repository: HTTPS or `ssh://` URL, SCM provider, credential reference, production
  branch, and immutable deployed commit.
- SSH source: host, port, user, project folder, log path, and `tail`/`snapshot` mode.
- Cloud source: provider, region, and resource.
- MCP source: HTTP endpoint, transport, allowlisted non-secret headers, evidence
  profile, query scope, and capabilities.
- Signed webhook: signing-secret reference, event types, and deduplication key.
- Custom rule: grouping window and match expression.

Source and trigger JSON is strictly decoded into connector-specific version-1
structures. Secret references are relational columns and must belong to the same
project. Arbitrary request JSON and secret values never enter config documents.

#### Credentials And Event Stream

`FIXTHE_ENCRYPTION_KEY` is required by API startup and must decode from standard
base64 to exactly 32 bytes. AES-256-GCM uses a fresh random nonce per write and
associated data containing key version, project ID, secret ID, and kind. Read APIs
return only ID, name, kind, key version, record version, and timestamps.

`UpdateSecret` treats a nil value as name-only and a non-nil value as rotation.
HTTP must pass nil when `value` is omitted and reject an explicit empty string
before calling the service. Rotation re-encrypts with the same project ID,
secret ID, and kind. Name-only keeps the loaded ciphertext, nonce, and key
version. Kind is not accepted on the update request.

`UpdateProjectName` keeps the stable project key unchanged. The same SQL
statement updates `project_environments.name` only when that environment name
still equals the old project name.

Observations are immutable project Event Stream records owned by project,
environment, and source. Lists are bounded to 1..100 and newest first. Attributes
are a bounded scalar object with allowlisted keys. Authenticated Observation POST is
temporary connector/development ingestion, not a public webhook.

Project creation, rename, member mutation, credential creation/update,
configuration replacement, incident creation, and incident status changes create
project audit events. Summaries are application-owned and metadata is
allowlisted; request bodies are never copied. `project.renamed` metadata is
`{projectKey}`. `project.secret.updated` metadata is `{name, kind, rotated}`.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Missing/invalid Session | `401 authentication_required` |
| Unknown project or non-member project | `404 project_not_found` for non-system users |
| Viewer writes; operator administers | `403 forbidden` |
| Invalid role/config/reference/query | `400 invalid_request` |
| Member username has no enabled local account | `404 member_not_found` |
| Duplicate project key/resource name | `409 conflict` |
| Last project-admin removal/demotion | Reject without mutation or audit event |
| Secret read/list/update response | Never return value, ciphertext, nonce, or password material |
| Credential PATCH omits `value` | Name-only; ciphertext/nonce/key version stay unchanged |
| Credential PATCH supplies empty `value` | `400 invalid_request`; do not call the service |
| Unknown secret ID on update | `400 invalid_request` |
| Environment name equals old project name | Rename updates that environment name |
| Environment name is distinct | Rename leaves the environment name unchanged |
| Source ID belongs to another project | Not found; never infer its existence |
| Missing/invalid encryption key | Fail API startup naming only the environment key |

### 5. Good/Base/Bad Cases

- Good: resolve membership, derive capabilities on the server, query by `project_id`,
  and append an allowlisted audit event in the same SQL statement/transaction.
- Base: an authorized viewer reads an empty Event Stream and receives a success
  envelope with `data: []` and `meta.total: 0`.
- Bad: a global query, UI-only role switch, credential value in JSON, connector
  headers copied blindly, or distinguishing a private project from an unknown key.

### 6. Tests Required

- Domain: project keys/roles, URL/commit rules, each typed source/trigger payload,
  same-project UUID references, secret kinds, and Observation attribute allowlist.
- Application: system-admin and membership matrices, non-member masking, last-admin
  guard, secret encryption context, generated IDs, name-only vs rotation, and
  preserved project key / secret ID / kind.
- HTTP: route nesting, capabilities, strict JSON, success envelope metadata and
  list totals, role denials, stable errors, omitted vs empty secret `value`, and
  credential non-disclosure.
- PostgreSQL integration: all 11 business tables and comments, configuration
  round-trip, encrypted-secret metadata, members, Observations, incidents, audit
  events, cross-project source/Observation/incident isolation, project rename,
  conditional environment-name sync, and credential name-only vs rotation.
- Quality: `make check`, `make build`, `make generate-check`, and integration build
  tag compilation. Run real integration tests only against the fail-closed dedicated
  test database contract.

### 7. Wrong vs Correct

#### Wrong

```go
// A missing project filter leaks operational data across memberships.
rows, err := queries.ListIncidents(ctx, limit)

// An explicit empty value is not name-only. []byte("") is non-nil and would rotate.
secret, err := service.UpdateSecret(ctx, principal, projectKey, secretID, name, []byte(*payload.Value))
```

#### Correct

```go
project, err := projects.ResolveProject(ctx, principal, projectKey)
if err != nil { return err }
rows, err := repository.List(ctx, project.ID, limit)

// Omit value => nil => name-only. Reject empty replacement before the service.
var value []byte
if payload.Value != nil {
    if strings.TrimSpace(*payload.Value) == "" {
        return application.ErrInvalidInput
    }
    value = []byte(*payload.Value)
}
secret, err := service.UpdateSecret(ctx, principal, projectKey, secretID, name, value)
```

## Common Mistakes

- Do not confuse a system `viewer` account with a project viewer membership. A user
  can have different roles in different projects.
- Do not add a membership for an arbitrary username. Create the controlled local
  account first, then grant its project role.
- Do not add global fallback queries when project context is absent.
- Do not log source config, Observation messages, secret values, encrypted material,
  request headers, or audit request bodies.
- Do not describe persisted connector configuration as an active connector runtime.
- Do not treat an explicit empty credential `value` as preserve. Only a missing
  field is name-only. Incomplete Git replacement drafts must not omit `value`.
- Do not accept `kind` on credential update. Secret ID and kind stay unchanged so
  repository, source, and webhook references remain valid.
