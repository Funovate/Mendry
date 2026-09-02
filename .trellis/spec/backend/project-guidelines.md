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
- Connector execution, polling, and custom-rule evaluation are not implemented
  yet. Public signed-webhook ingress is implemented: a path token identifies the
  project and opens or updates a `P2` incident. Log search stays in the
  remediation harness.

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
func (*projects.Service).GetConfigurationDraft(context.Context, authdomain.User, string)
    (projectdomain.ConfigurationDraft, error)
func (*projects.Service).PutConfigurationEnvironment(context.Context, authdomain.User, string, projectdomain.Environment)
    (projectdomain.Environment, error)
func (*projects.Service).PutConfigurationRepository(context.Context, authdomain.User, string, projectdomain.Repository)
    (projectdomain.Repository, error)
func (*projects.Service).PutConfigurationSource(context.Context, authdomain.User, string, projectdomain.Source)
    (projectdomain.Source, error)
func (*projects.Service).PutConfigurationTrigger(context.Context, authdomain.User, string, projectdomain.Trigger)
    (projectdomain.Trigger, error)
func (*projects.Service).PutConfigurationLLMProvider(context.Context, authdomain.User, string, projectdomain.LLMProvider)
    (projectdomain.LLMProvider, error)
func (*projects.Service).ProbeRepositoryRefs(context.Context, authdomain.User,
    string, string, string, string) (application.RepositoryRefs, error)
func (*projects.Service).ProbeLLMModels(context.Context, authdomain.User,
    string, string, string) (application.LLMModels, error)
func (*projects.Service).RotateWebhookToken(context.Context, authdomain.User,
    string) (string, error)
func (*projects.Service).LookupWebhookToken(context.Context, string)
    (application.WebhookIngress, error)

func (*observations.Service).List(context.Context, authdomain.User, string, int32)
    (observations.ListResult, error)
func (*observations.Service).Create(context.Context, authdomain.User, string,
    observations.CreateInput) (observationdomain.Observation, error)
func (*observations.Service).CreateInbound(context.Context, string, string,
    string, string, time.Time) (observationdomain.Observation, error)

func (*hooks.Service).Ingest(context.Context, string, string)
    (hooks.Result, error)
```

HTTP routes:

```text
GET|POST /api/v1/projects
GET|PATCH /api/v1/projects/{projectKey}
GET      /api/v1/projects/{projectKey}/members
PUT|DELETE /api/v1/projects/{projectKey}/members/{username}
GET|POST /api/v1/projects/{projectKey}/secrets
PATCH    /api/v1/projects/{projectKey}/secrets/{secretId}
GET      /api/v1/projects/{projectKey}/configuration
GET      /api/v1/projects/{projectKey}/configuration/draft
PUT      /api/v1/projects/{projectKey}/configuration
PUT      /api/v1/projects/{projectKey}/configuration/{environment|repository|source|trigger|llm}
POST     /api/v1/projects/{projectKey}/configuration/webhook-token
POST     /api/v1/projects/{projectKey}/llm/models
POST     /api/v1/projects/{projectKey}/llm/chat
GET|POST /api/v1/projects/{projectKey}/observations
GET      /api/v1/projects/{projectKey}/audit-events
POST     /hooks/{token}
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

The MVP storage constraints keep at most one environment, repository, source, trigger, and LLM provider row per project. The editor persists those component rows independently through the draft/component configuration routes; the complete configuration read becomes available once the required environment, repository, source, and trigger rows exist. Supporting multiple environments or connectors requires a later forward migration plus item-addressed API contracts; table names alone do not imply that support.

- Repository: HTTPS or `ssh://` URL, SCM provider, credential reference, production
  branch, and immutable deployed commit.
- SSH source: host, port, user, project folder, log path, and `tail`/`snapshot` mode.
- Cloud source: provider, region, and resource.
- MCP source: HTTP endpoint, transport, allowlisted non-secret headers, evidence
  profile, query scope, and capabilities.
- Signed webhook: server-generated inbound path token. `signingSecretId` is
  optional leftover HMAC metadata and is not required to save or ingest.
  Compatibility config may still store `eventTypes` / `deduplicationKey`;
  runtime ignores them. The inbound URL is derived as
  `{FIXTHE_PUBLIC_URL}/hooks/{token}` and is never a user-supplied field.
- Custom rule: grouping window and match expression.
- LLM provider: OpenAI-compatible `baseUrl`, same-project `http_bearer`
  credential reference, and a required model ID selected from `/v1/models`.

Source and trigger JSON is strictly decoded into connector-specific version-1
structures. Secret references are relational columns and must belong to the same
project. Arbitrary request JSON and secret values never enter config documents.

#### Credentials And Event Stream

`FIXTHE_ENCRYPTION_KEY` is required by API startup and must decode from standard
base64 to exactly 32 bytes. AES-256-GCM uses a fresh random nonce per write and
associated data containing key version, project ID, secret ID, and kind. Read APIs
return only ID, name, kind, key version, record version, and timestamps.

Trusted remediation adapters decrypt the same rows internally and wipe
plaintext after use. They must not add a public plaintext-secret method on
`projects.Service`. The OpenAI-compatible key is a same-project `http_bearer`
secret referenced by `project_llm_providers.credential_secret_id`; do not add
an `openai_api_key` kind. See
[Remediation Adapter Guidelines](./remediation-adapter-guidelines.md).

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
are a bounded scalar object with allowlisted keys. Authenticated Observation POST
remains the temporary connector/development ingestion boundary. Public ingest is
`POST /hooks/{token}` only.

#### Inbound webhook token

`FIXTHE_PUBLIC_URL` is required by API startup. It must be an absolute
`http`/`https` URL without credentials, query, fragment, or a trailing slash;
an optional path prefix is allowed. Missing or invalid values fail `LoadAPI`
naming only the key. migrate / seed / bootstrap-admin do not read it.

A `signed_webhook` trigger stores three columns together or not at all:
`ingress_token_hash` (SHA-256 of the 43-character raw-url token),
`ingress_token_ciphertext`, and `ingress_token_nonce`. AES-256-GCM associated
data is `projectID + triggerID + "webhook_token"`. Do not store the token in
`project_secrets` or reuse `webhook_hmac`.

`PutConfigurationTrigger` generates a token when kind is `signed_webhook` and the row has none. Later component saves keep the existing token. Switching to `custom_rule` clears the three columns. `RotateWebhookToken` creates or replaces the token and writes audit `project.trigger.webhook_token` with metadata `{rotated}`. It reads the saved trigger only: a draft `signed_webhook` that has not been saved is still `400 invalid_request`.

`GetConfiguration` sets `trigger.inboundUrl` only for a project admin. Operator
and viewer receive `null`. List, log, and audit records never include the
plaintext token. `LookupWebhookToken` hashes the path value and returns
project ID plus the enabled same-project source, or not found.

`POST /hooks/{token}` has no Session. The body is bounded opaque UTF-8
(`text/plain`, `application/json`, form, or omitted Content-Type). Empty or invalid
UTF-8 is `400 invalid_request`. The token is resolved before the request is accepted.
Success is `202` with `{accepted: true}`; it does not wait for an incident ID. Semantic
normalization, Observation persistence, and incident ingestion then run in the API
process background with request cancellation detached. Generic JSON payloads are
sanitized and sent to the project LLM provider through a narrow classifier port with
no tools and a bounded timeout. The classifier returns only a bounded title and scalar
`grouping_fields`; the backend computes the final scoped `ai:v1` fingerprint from
canonical fields. Tencent CLS still uses the classifier for its title, but its final
fingerprint is always `tencent-cls:v1:<sha256>` over the project/source scope and a
fixed whitespace-normalized `{alarm: Alarm, topic: Topic}` map. Model-selected fields,
`TopicId`, `DetailUrl`, UIN, conditions, and trigger-count metadata never enter that
provider fingerprint. Missing configuration, timeout, provider failure, or invalid
model output uses a deterministic fallback: canonical JSON with volatile identifiers
removed, or the legacy first non-empty line for plain text. The raw body becomes the
Observation message (`error`, attributes `{}`). Source and environment come from the
saved project configuration. Background failures emit a safe `webhook.ingest.failed`
diagnostic without the token or raw payload. This in-process delivery is best effort;
durable retry remains a future queue/outbox concern.

Unknown hash, disabled trigger, wrong kind, missing source, and missing configuration
all return `404 webhook_not_found`. Do not distinguish those cases. Hooks do not call
Git, SSH, or CLS adapters and do not let the model diagnose or trigger remediation; a
new `P2` incident uses the existing `RemediationTrigger`. Model input is bounded and
redacted, and raw webhook bodies are not added to application logs.
Project creation, rename, member mutation, credential creation/update,
configuration replacement, webhook-token rotate, incident creation, incident
occurrence, and incident status changes create project audit events.
Summaries are application-owned and metadata is allowlisted; request bodies
are never copied. `project.renamed` metadata is `{projectKey}`.
`project.secret.updated` metadata is `{name, kind, rotated}`.
`project.trigger.webhook_token` metadata is `{rotated}`. Inbound incident
audits use a null `actor_user_id`.

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
| Missing/invalid `FIXTHE_PUBLIC_URL` | Fail API startup naming only the environment key |
| Empty `PublicURL` in `projects.NewService` | Constructor fails; do not assemble a blank inbound URL |
| Non-admin reads configuration | `trigger.inboundUrl` is null |
| Unknown, disabled, or incomplete webhook token | `404 webhook_not_found`; no Observation or Incident |
| Empty or invalid UTF-8 webhook body | `400 invalid_request` |

### 5. Good/Base/Bad Cases

- Good: resolve membership, derive capabilities on the server, query by `project_id`,
  and append an allowlisted audit event in the same SQL statement/transaction.
- Base: an authorized viewer reads an empty Event Stream and receives a success
  envelope with `data: []` and `meta.total: 0`.
- Good: first `signed_webhook` save generates a token; admin copy shows
  `{publicURL}/hooks/{token}`; rotate invalidates the previous hash.
- Base: an operator GET configuration returns the trigger without `inboundUrl`.
- Bad: a global query, UI-only role switch, credential value in JSON, connector
  headers copied blindly, distinguishing a private project from an unknown key,
  logging the raw `/hooks/{token}` path, requiring HMAC to save a webhook, or
  returning a different error for a disabled trigger than for an unknown token.

### 6. Tests Required

- Domain: project keys/roles, URL/commit rules, each typed source/trigger payload,
  same-project UUID references, secret kinds, and Observation attribute allowlist.
- Application: system-admin and membership matrices, non-member masking, last-admin
  guard, secret encryption context, generated IDs, name-only vs rotation,
  admin reveal vs operator omit, rotate invalidating the previous hash, and webhook
  semantic normalization: token lookup before acceptance/model calls, immediate
  return while the model is blocked, canonical JSON fallback
  with volatile-field removal, strict model output validation, redacted bounded model
  input, project/source scope isolation, legacy plain-text compatibility, and Tencent
  CLS deterministic provider grouping: changing model fields, `TopicId`, URL, or
  trigger-count metadata preserves the fingerprint, while changing `Alarm` or `Topic`
  changes it.
- HTTP: route nesting, capabilities, strict JSON, success envelope metadata and
  list totals, role denials, stable errors, omitted vs empty secret `value`,
  credential non-disclosure, admin-only `inboundUrl`, and unauthenticated
  `POST /hooks/{token}` accepted acknowledgement and 202 / 404 collapsing.
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
- Do not describe SSH / Cloud / MCP configuration as an active connector
  runtime. Signed webhook is the one trigger that now has a public ingest
  route.
- Do not require `webhook_hmac` to save or receive a signed webhook. Do not
  put the inbound token in `project_secrets` or add a public plaintext-secret
  method for it.
- Do not log, audit, or return the raw webhook token except as the admin-only
  derived `inboundUrl`. AccessLog must use `POST /hooks/{token}`, never
  `RequestURI`.
- Do not use model-selected `grouping_fields` as the final Tencent CLS incident
  identity. Even at temperature zero, equivalent prompts can produce different field
  names, field counts, or abstraction levels; keep its fixed `Alarm`/`Topic` hash in
  the hooks application boundary.
- Do not treat an explicit empty credential `value` as preserve. Only a missing
  field is name-only. Incomplete Git replacement drafts must not omit `value`.
- Do not accept `kind` on credential update. Secret ID and kind stay unchanged so
  repository, source, and webhook references remain valid.
