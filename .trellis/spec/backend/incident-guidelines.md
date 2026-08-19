# Incident Guidelines

> Established incident domain, application, PostgreSQL, and REST contracts.

---

## Scenario: Incident MVP Vertical Slice

### 1. Scope / Trigger

Use this contract when changing incident identifiers, lifecycle values, validation,
repository queries, protected REST routes, inbound fingerprint ingest, or API
composition. Authenticated create/list/detail/status remain Session-gated.
Public webhook ingest opens or bumps incidents through `IngestInbound` without
a principal. Evidence collection stays in the remediation harness.

Dependency direction is:

```text
Authenticated HTTP -> incident application -> incident domain
POST /hooks/{token} -> hooks application -> IngestInbound / CreateInbound
                         |
                         +-> repository contract <- PostgreSQL adapter -> sqlc
```

### 2. Signatures

```go
func application.NewService(application.Options) (*application.Service, error)
func (*application.Service).List(context.Context, authdomain.User, string, int32)
    (application.ListResult, error)
func (*application.Service).Get(context.Context, authdomain.User, string, string)
    (domain.Incident, error)
func (*application.Service).Create(context.Context, authdomain.User, string,
    application.CreateInput) (domain.Incident, error)
func (*application.Service).UpdateStatus(context.Context, authdomain.User,
    string, string, string) (domain.Incident, error)
func (*application.Service).IngestInbound(context.Context, string, string,
    string, string, time.Time) (domain.Incident, bool, error)

func postgres.NewRepository(incidentdb.DBTX) (*postgres.Repository, error)
func http.NewHandler(http.HandlerOptions) (*http.Handler, error)
func (*http.Handler).Register(*net/http.ServeMux)
```

Routes:

```text
GET   /api/v1/projects/{projectKey}/incidents
POST  /api/v1/projects/{projectKey}/incidents
GET   /api/v1/projects/{projectKey}/incidents/{id}
PATCH /api/v1/projects/{projectKey}/incidents/{id}/status
```

### 3. Contracts

- The public incident `id` is canonical `INC-<positive-number>`; an internal UUIDv7
  never enters JSON. Lowercase prefixes, signs, leading zeroes, zero, and raw UUIDs
  are invalid.
- Status is exactly `Open`, `Recovered`, or `Closed`. Priority is exactly `Info`,
  `P2`, or `P1`.
- List accepts only optional `limit=1..100`, defaults to 50, and returns the
  shared success envelope with the incident array in `data` and a server-derived
  `meta.total`. Empty results are `data: []`, never `null`; `total` is not the
  current page length unless the database result says so.
- Create accepts `title`, `fingerprint`, `sourceId`, and optional `priority`,
  `firstSeen`, `lastSeen`, `occurrenceCount`, `hostCount`, `muted`, and
  `notificationSummary`. It always creates status `Open`; defaults are current UTC
  time, counts 1/1, priority `Info`, unmuted, and `Lifecycle default`.
- `IngestInbound` is the unauthenticated webhook path. It looks up
  `(project_id, fingerprint)`:
  - none → create `Open` / `P2` / counts 1, null audit actor, then
    `RemediationTrigger.Emit` when priority qualifies;
  - `Open` → `last_seen` and `occurrence_count + 1`, no second Emit, no
    priority change;
  - `Closed` or `Recovered` → leave the incident unchanged. The Observation
    was already written by hooks.
  Concurrent first inserts retry a unique-fingerprint conflict as a bump.
- Detail and status-update return an incident object in the shared success
  envelope. Create returns 201 with `Location:
  /api/v1/projects/<projectKey>/incidents/<id>`; update returns 200.
- Authenticated incident routes require a verified Redis-backed Session and
  resolved project access. Project `admin`, `operator`, and `viewer` may read.
  Only project `admin` and `operator` may create or update. System
  administrators can administer every project. HTTP middleware and application
  use cases both enforce authorization. `IngestInbound` does not take a
  principal; project identity comes from the webhook token lookup.
- Every repository call includes `project_id`. The source must belong to the same
  project; repository resolution supplies environment ID and safe source display
  name. Fingerprint uniqueness is per project, not global.
- The old global `/api/v1/incidents` routes are intentionally absent.
- The application layer owns validation, defaults, authorization, and UUIDv7
  generation. The PostgreSQL adapter owns sqlc values, stable operation names,
  database error classification, and row mapping.
- `pgx.ErrNoRows` maps to `ErrNotFound`. Only the fingerprint unique constraint maps
  to `ErrConflict`; internal UUID/number collisions remain internal errors.
- Stored rows are revalidated for UUIDv7, generated positive number/version,
  timestamps, enums, text limits, and count ordering before crossing the adapter.

Successful incident JSON fields are:

```json
{
  "id": "INC-2049",
  "title": "Database latency",
  "fingerprint": "pg:latency",
  "status": "Open",
  "priority": "P2",
  "source": "production-logs",
  "sourceId": "019...",
  "environmentId": "019...",
  "firstSeen": "2026-08-13T01:02:03Z",
  "lastSeen": "2026-08-13T01:02:03Z",
  "occurrenceCount": 2,
  "hostCount": 1,
  "muted": false,
  "notificationSummary": "Lifecycle default",
  "version": 1,
  "createdAt": "2026-08-13T01:02:03Z",
  "updatedAt": "2026-08-13T01:02:03Z"
}
```

### 4. Validation & Error Matrix

| Condition | Status / category |
|---|---|
| Missing/invalid Session | 401 / `authentication_required` |
| Unknown project or non-member project | 404 / `project_not_found` |
| Viewer attempts create/update | 403 / `forbidden` |
| Malformed ID, enum, query, text, time, or count | 400 / `invalid_request` |
| Unknown incident | 404 / `incident_not_found` |
| Source belongs to another project | 404 / `source_not_found` |
| Existing fingerprint on authenticated create | 409 / `incident_conflict` |
| Existing Open fingerprint on inbound ingest | 202 / bump occurrence; `created=false` |
| Closed or Recovered fingerprint on inbound ingest | 202 / incident unchanged; Observation already stored |
| Invalid JSON/media type/body size | Shared 400/415/413 HTTP boundary error |
| UUID generation, PostgreSQL, or corrupt-row failure | 500 / `internal_error` |

HTTP messages never contain repository errors, SQL, identifiers, or database
diagnostics. Feature JSON 404 errors retain their feature code through the shared
mux normalization middleware.

### 5. Good/Base/Bad Cases

- Good: HTTP authenticates an operator, application independently authorizes the
  write, repository declares `incident.update_status`, and the response exposes
  only `INC-2049`.
- Base: an authorized project viewer lists an empty project and receives a 200
  success envelope with `data: []` and `meta.total: 0`.
- Bad: exposing the UUID, accepting `INC-+1`, trusting UI-only authorization,
  passing sqlc rows to HTTP, using a global query, omitting `project_id`, using an
  unbounded list, or mapping every unique violation to a public fingerprint conflict.

### 6. Tests Required

- Domain: exhaustive status/priority parsing, canonical ID round trip/rejections,
  text/time/count invariants.
- Application: project role matrix and non-member masking, defaults, invalid input
  before persistence/ID creation, bounded list, external ID parsing, preserved
  repository categories, inbound P2 create emitting remediation, Open bump not
  re-emitting, and Closed/Recovered not reopening.
- PostgreSQL adapter: UUID/time parameter mapping, safe error wrapping, exact
  fingerprint conflict mapping, not-found mapping, and corrupt-row rejection.
- HTTP: authentication and viewer denial, strict JSON, list query bounds, success
  envelope metadata and total, no internal UUID, Location, status update, and
  exact error codes.
- Integration: migration plus project-scoped repository create/get/list/update,
  cross-project incident/source denial, and audit writes against an explicit
  isolated PostgreSQL database.
- Quality: `make check`, `make generate-check`, integration build-tag compilation,
  and a real read-only login/list/not-found/logout smoke test when local services are
  available.

### 7. Wrong vs Correct

#### Wrong

```go
row, _ := incidentdb.New(pool).GetIncidentByNumber(ctx, number)
json.NewEncoder(writer).Encode(row) // leaks sqlc values and internal UUID

// Do not reopen a Closed incident from webhook ingest.
if current.Status != domain.StatusOpen {
    return s.UpdateStatus(ctx, systemUser, key, id, "Open")
}
```

#### Correct

```go
incident, err := service.Get(ctx, principal,
    request.PathValue("projectKey"), request.PathValue("id"))
if err != nil {
    writeApplicationError(writer, request, err)
    return
}
writeIncident(writer, request, http.StatusOK, incident)
```
