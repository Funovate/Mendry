# Authentication Guidelines

> Established local-account, password, Redis Session, HTTP cookie, and role authorization contracts.

---

## Scenario: Local MVP Authentication

### 1. Scope / Trigger

Use this contract when changing local users, password verification, Session
storage, auth HTTP routes, protected handlers, role authorization, or the first
administrator command.

- `internal/modules/auth/domain` owns `User` and `Role`.
- `internal/modules/auth/application` owns credential/session use cases and
  application authorization.
- Auth adapters own bcrypt, sqlc user queries, Redis encoding, and HTTP cookies.
- PostgreSQL stores users/password hashes. Redis stores disposable Sessions only.
- Account management UI, password reset, SSO, refresh tokens, and rate limiting
  are not implemented in this MVP. A system-admin-only API creates local accounts
  so project memberships can target real users.

### 2. Signatures

```go
func application.NewService(application.Options) (*application.Service, error)
func (*application.Service).Login(context.Context, string, []byte, string)
    (application.LoginResult, error)
func (*application.Service).Authenticate(context.Context, string) (domain.User, error)
func (*application.Service).Logout(context.Context, string) error
func (*application.Service).CreateUser(context.Context, domain.User,
    string, []byte) (domain.User, error)
func application.RequireRoles(domain.User, ...domain.Role) error

func application.NewBootstrapper(application.BootstrapOptions)
    (*application.Bootstrapper, error)
func (*application.Bootstrapper).BootstrapAdmin(context.Context, string, []byte)
    (domain.User, bool, error)

func (*http.Handler).RequireAuthentication(http.Handler) http.Handler
func (*http.Handler).RequireRoles([]domain.Role, http.Handler) http.Handler
func http.CurrentUser(context.Context) (domain.User, bool)

func bootstrap.RunBootstrapAdmin(context.Context, bootstrap.BootstrapAdminOptions) error
```

Commands and routes:

```text
FIXTHE_BOOTSTRAP_ADMIN_PASSWORD=... make bootstrap-admin USERNAME=admin
POST /api/v1/auth/login
GET  /api/v1/auth/me
POST /api/v1/auth/logout
POST /api/v1/users
POST /hooks/{token}
```

`POST /hooks/{token}` is the only business route registered without
`RequireAuthentication`. Project identity is the path token. Do not reuse
this exception for Observation or incident REST routes.

### 3. Contracts

#### Configuration

| Key | Default | Constraint |
|---|---|---|
| `FIXTHE_AUTH_SESSION_TTL` | `24h` | 5 minutes through 30 days; API only |
| `FIXTHE_BOOTSTRAP_ADMIN_PASSWORD` | none | command-only secret; 12 through 72 bytes |

`bootstrap-admin` also requires `FIXTHE_POSTGRES_URL` and a `--username` flag.
It does not load Redis or HTTP configuration. Repeating it for the same enabled
admin succeeds without replacing the password hash. API startup never reads the
bootstrap password or creates users implicitly.

#### User And Password

Usernames are trimmed, lowercased, and match
`^[a-z][a-z0-9._-]{2,63}$`. Roles are exactly `admin`, `operator`, or `viewer`.
Passwords use bcrypt cost 12 and are never stored as plaintext. Default
inbound logs also omit them. The only log exception is
`FIXTHE_HTTP_REQUEST_DEBUG=true`, which writes the raw login body and
`fixthe_session` cookie on `http.request.completed`. See
`.trellis/spec/backend/logging-guidelines.md`.
Unknown users, disabled users, malformed login names, and wrong passwords all
perform a bcrypt comparison and return the same `invalid_credentials` response.

Only an authenticated system administrator may call `POST /api/v1/users`. The
request contains `username` and a 12..72-byte password. Created users are enabled
with system role `viewer`; project roles are granted separately through project
memberships. The application service rechecks the system-admin role, so HTTP
middleware is not the only authorization boundary.

#### Session And Cookie

- Session tokens contain 32 random bytes encoded as unpadded base64url.
- Redis key: `fixthe:session:v1:<sha256(raw-token)>`; the raw token never appears
  in a Redis key or value.
- Redis value contains UUIDv7 user ID, normalized username, role, and absolute
  expiry. Redis TTL and application absolute-expiry checks both apply.
- Login creates a new Session, then revokes any previous cookie Session. Rotation
  failure removes the new Session and fails closed.
- Logout deletes the Session and clears the cookie; missing/expired Sessions are
  idempotent.
- Cookie name is `fixthe_session`, path `/`, `HttpOnly`, `SameSite=Lax`; `Secure`
  is required in `staging` and `production` and disabled in local/test environments.
- Default inbound logs, spans, and metrics never contain the raw session
  cookie or `Authorization` header. `FIXTHE_HTTP_REQUEST_DEBUG=true` is
  the only exception, and it writes those values only on
  `http.request.completed`.
- Auth responses use `Cache-Control: no-store`.

#### HTTP JSON

Login request:

```json
{"username":"admin","password":"..."}
```

Login and current-user response:

```json
{"code":"ok","message":"OK","data":{"id":"019...","username":"admin","role":"admin"},"meta":{"requestId":"...","durationMs":1}}
```

Logout requires `{}` and returns `204`. Auth responses never expose a password
hash, Session token, expiry, enabled flag, or internal database data.

User creation request and response:

```json
{"username":"oncall.operator","password":"..."}
{"code":"ok","message":"OK","data":{"id":"019...","username":"oncall.operator","role":"viewer"},"meta":{"requestId":"...","durationMs":1}}
```

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Invalid JSON/media type/body size | Existing shared `invalid_request`, `unsupported_media_type`, or `request_too_large` JSON error |
| Unknown/disabled user or wrong credentials | `401 invalid_credentials`; same public message |
| Missing, malformed, expired, or revoked Session | `401 authentication_required` |
| Authenticated role is not allowed | `403 forbidden` |
| PostgreSQL/Redis/password/internal failure | Client gets `500 internal_error` with no dependency diagnostic; private server log retains the original error chain |
| Login with an existing Session | Rotate; old token no longer works |
| Redis Session value has invalid UUIDv7/username/role/expiry | Reject as infrastructure corruption, never authenticate |
| Existing bootstrap username is non-admin or disabled | Fail with user conflict; do not alter account |
| Non-admin creates a local user | `403 forbidden` |
| Invalid username/password for user creation | `400 invalid_request` |
| Duplicate local username | `409 user_conflict`; never replace password hash |
| staging/production cookie | Set `Secure`; deployment must use HTTPS |

### 5. Good/Base/Bad Cases

- Good: incident write handler uses HTTP `RequireRoles(admin, operator)` and the
  incident application use case independently calls `application.RequireRoles`.
- Base: login creates one Redis Session, returns safe user JSON, and stores the
  raw token only in an HttpOnly cookie.
- Bad: JWT/localStorage auth, Redis plaintext token keys, passwords in
  default-off logs, API-startup admin creation, or UI-only authorization.

### 6. Tests Required

- Domain: username normalization and exhaustive role parsing.
- Application: credential non-disclosure, disabled-user bcrypt comparison,
  Session creation/expiry/logout/rotation cleanup, idempotent bootstrap, and roles.
- Password: bcrypt round trip and wrong-password rejection.
- Redis: token hashing, safe value, exact TTL, malformed token, invalid Session
  value, expiry, deletion, and collision behavior.
- HTTP: strict JSON, stable 400/401/403/409/500 mapping, safe response fields,
  no-store, cookie flags, current user, logout clearing, user creation, and
  viewer/operator middleware.
- Configuration: defaults, bounds, `.env.example`, bootstrap Redis independence.
- Quality: `make generate-check`, `make check`, and `make build`.

### 7. Wrong vs Correct

#### Wrong

```go
// Raw token is searchable in Redis and the UI is trusted to authorize writes.
redis.Set(ctx, "session:"+token, user, ttl)
if request.Role == "operator" { updateIncident() }
```

#### Correct

```go
// Adapter hashes the token; both HTTP and application layers enforce the role.
key := "fixthe:session:v1:" + sha256Hex(token)
handler := authHandler.RequireRoles([]domain.Role{domain.RoleAdmin, domain.RoleOperator}, next)
if err := application.RequireRoles(principal, domain.RoleAdmin, domain.RoleOperator); err != nil {
    return err
}
```

## Common Mistakes

- Do not short-circuit disabled accounts before bcrypt comparison; that creates a
  credential timing distinction.
- Do not reuse the API Service for bootstrap if doing so makes the PostgreSQL-only
  command require Redis.
- Do not trust Redis Session values without validating UUIDv7, normalized username,
  known role, and absolute expiry.
- Do not protect business writes only in HTTP or only in the frontend; application
  authorization remains mandatory.
- Do not assign a project operator by changing `users.role`; create a system-viewer
  account and grant an operator membership in the target project.
