# Error Handling

> Backend error ownership and shared HTTP boundary contracts.

---

## Scenario: API JSON Boundary

### 1. Scope / Trigger

Use this contract for every API handler, shared HTTP middleware, request decoder,
or transport error mapping. Lower packages return wrapped errors; the HTTP boundary
maps only safe application categories and never serializes concrete error text.

### 2. Signatures

```go
func httpserver.Boundary(httpserver.BoundaryOptions) (http.Handler, error)
func httpserver.AccessLog(*slog.Logger, int64, bool, http.Handler) http.Handler
func httpserver.WriteJSON(http.ResponseWriter, int, any) error
func httpserver.WriteListJSON(http.ResponseWriter, int, any, int64) error
func httpserver.WriteError(http.ResponseWriter, *http.Request,
    httpserver.Error)
func httpserver.WriteInternalError(http.ResponseWriter, *http.Request, error)
func httpserver.DecodeJSON(*http.Request, any) *httpserver.Error
func httpserver.RequestID(context.Context) string
func errtrace.Capture(skip int) errtrace.Trace
func errtrace.FromError(error) (errtrace.Trace, bool)
```

### 3. Contracts

Every JSON success response is written through `WriteJSON` and uses this envelope:

```json
{
  "code": "ok",
  "message": "OK",
  "data": {},
  "meta": {
    "requestId": "8a0b1b0f7567f11440f392ada3988b0d",
    "durationMs": 12
  }
}
```

`meta.requestId` equals `X-Request-ID`; `meta.durationMs` is a non-negative
HTTP-boundary duration in milliseconds. List endpoints use `WriteListJSON` and
include a server-derived `meta.total`; the value is never inferred from the
current page by the HTTP adapter. Empty lists serialize as `data: []`. `204 No
Content` remains bodyless and does not use an envelope. Errors retain the
envelope below and do not gain success metadata.

Except for a canceled request whose client has already gone away, every error
response has `Content-Type: application/json`, an `X-Request-ID` header, and
exactly this envelope:

```json
{
  "error": {
    "code": "invalid_request",
    "message": "Request body contains invalid JSON.",
    "requestId": "8a0b1b0f7567f11440f392ada3988b0d"
  }
}
```

- `code` is a stable machine value; `message` is safe user-facing English.
- `requestId` equals the response header. A client value is reused only when it is
  8-64 ASCII alphanumeric, dash, underscore, or dot characters; otherwise a random
  128-bit hex value is generated.
- `FIXTHE_HTTP_MAX_BODY_BYTES` defaults to 1 MiB and accepts 1 KiB through 10 MiB.
  The same ceiling bounds inbound request-debug body capture.
- `FIXTHE_HTTP_CORS_ALLOWED_ORIGIN` is empty for same-origin use or one exact
  `http`/`https` origin without credentials, path, query, or fragment.
- `FIXTHE_HTTP_REQUEST_DEBUG` defaults to `false`. When true, AccessLog
  attaches the unredacted request/response dump to INFO
  `http.request.completed` and skips `http.request.failed`. Client
  envelopes are unchanged. See
  `.trellis/spec/backend/logging-guidelines.md`.
- CORS permits credentials and only the configured origin. With no configured
  cross-origin value, a browser `Origin` must match request scheme and host.
- `DecodeJSON` requires `application/json`, rejects unknown fields, empty bodies,
  malformed JSON, multiple JSON values, and bodies over the configured limit.
- `net/http` mux default plaintext 404 and 405 responses are normalized into this
  envelope while preserving headers such as `Allow`. A feature handler that has
  already declared an `application/json` 404/405 keeps its feature-specific code
  and body; normalization must not rewrite `incident_not_found` or
  `webhook_not_found` into `not_found`. Unknown, disabled, and incomplete
  webhook tokens all use `webhook_not_found`.
- An HTTP adapter maps expected application categories with `WriteError`. Its
  unknown/default branch calls `WriteInternalError` with the original `error`.
  The client still receives only `500 internal_error`; the shared access boundary
  emits one `http.request.error` record containing the original error type,
  message, unwrapped cause chain, `error_stack`, and `error_stack_source`.
  When both the cause chain and `request.Context()` report `context.Canceled`,
  `WriteInternalError` instead records a bodyless `499` client-closed outcome.
  That expected cancellation emits only the INFO completion record: it does not
  emit an internal-error record or a failure request snapshot. A dependency
  cancellation while the request context is still active remains a real `500`.
- Standard Go errors do not retain call stacks. PostgreSQL/Redis `safeError` and
  feature repository errors therefore call `errtrace.Capture` when they wrap a
  cause and expose that trace through `errtrace.Provider`. The HTTP boundary
  selects the deepest captured trace in the cause graph and labels it
  `wrapped_error`. If no cause captured a trace, `WriteInternalError` records a
  handler-side fallback labeled `http_boundary`; that fallback locates the
  reporting adapter, not the original dependency failure.
- `debug.Stack()` at `WriteInternalError` and slog `AddSource` are not substitutes
  for wrap-time capture: repository/dependency frames have already returned, and
  logger source identifies only the logging call.
- Panic recovery logs a stable event plus request ID, original panic value/type,
  and stack. It writes a generic 500 only if no response has already been
  committed; none of the panic diagnostics enter the response.

Middleware order is fixed by `Boundary`: request ID -> access log -> recovery ->
CORS -> body limit -> mux error normalization -> application mux. OpenTelemetry may
wrap the completed boundary at composition time.

### 4. Validation & Error Matrix

| Condition | Status / code |
|---|---|
| Unsupported or missing JSON media type | 415 / `unsupported_media_type` |
| Empty, malformed, unknown-field, or multi-value JSON | 400 / `invalid_request` |
| Body exceeds configured limit | 413 / `request_too_large` |
| Unknown route | 404 / `not_found` |
| Known path with unsupported method | 405 / `method_not_allowed` |
| Origin not same-origin or explicitly allowed | 403 / `origin_not_allowed` |
| Cause and HTTP request context are both canceled | Bodyless 499; completion log only |
| Handler panic before response commit | 500 / `internal_error` |
| Unknown application/dependency error | 500 / `internal_error`; original cause only in server log |
| Unknown error with a stack-aware cause | Server log uses deepest captured stack / `wrapped_error` |
| Unknown error without a captured stack | Server log uses adapter fallback / `http_boundary` |
| Invalid HTTP boundary config | Startup fails before listener creation |

### 5. Good/Base/Bad Cases

- Good: a handler calls `DecodeJSON`, validates domain fields, maps an application
  category to `httpserver.Error`, maps an unknown error with `WriteInternalError`,
  and uses `WriteJSON` or `WriteListJSON` for success. Dependency/repository
  wrappers capture an `errtrace.Trace` when adding operation context.
- Base: a bodyless GET uses the boundary and inherits request ID, recovery, access
  logging, CORS, and normalized mux behavior without local helpers.
- Bad: a handler calls `http.Error`, returns `err.Error()`, accepts arbitrary JSON
  fields, discards an unknown error into a generic `WriteError`, generates its own
  request ID, treats a boundary `debug.Stack()`/slog source as an origin trace, or
  writes CORS wildcard with credentials.

### 6. Tests Required

- Exact status, content type, envelope, and header/body request ID equality.
- `WriteInternalError` tests assert that console/JSON server logs contain the
  original error, unwrapped causes, stack, and stack-source label while the response
  contains only the generic code/message/request ID. Expected 4xx errors must not
  emit `http.request.error`.
- Cancellation tests assert that a canceled cause plus canceled request context
  produces bodyless 499 with only an INFO completion record, while a canceled
  dependency cause under an active request still produces the generic 500.
- `errtrace` tests assert formatted function/file/line frames and deepest-cause
  selection. PostgreSQL and repository tests assert constructors capture their
  call sites. A non-stack-aware error must exercise the labeled HTTP fallback.
- Panic tests assert the server event contains value/type/stack while the response
  contains none of those diagnostics.
- Strict JSON tests for valid object, media type, empty/syntax/unknown/multiple values,
  and body limits including trailing oversized data.
- CORS tests for configured allow, denied origin, same-origin default, credential
  header, preflight methods/headers, and `Vary`.
- Mux tests for JSON 404/405, preserved `Allow` header, and preservation of a
  feature-owned JSON 404 code.
- Full `make check` and command build after boundary/config changes.

### 7. Wrong vs Correct

#### Wrong

```go
if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
    http.Error(w, err.Error(), http.StatusBadRequest)
}
```

#### Correct

```go
if apiError := httpserver.DecodeJSON(r, &input); apiError != nil {
    httpserver.WriteError(w, r, *apiError)
    return
}
```

Unknown application errors use the separate server/client boundary:

```go
if err != nil {
    httpserver.WriteInternalError(w, r, err)
    return
}
```

Capture origin frames while the lower call path is still active:

```go
func newRepositoryError(operation string, cause error) repositoryError {
    return repositoryError{
        operation: operation,
        cause: cause,
        stack: errtrace.Capture(1),
    }
}
```

Do not try to reconstruct the origin after unwinding:

```go
// Wrong: this stack starts at the HTTP/logging boundary.
logger.Error("request failed", "stack", debug.Stack())
```

## Non-HTTP Errors

- Wrap returned errors with an operation using `fmt.Errorf("operation: %w", err)`.
  Infrastructure and repository wrappers that can reach an unknown 500 also retain
  an `errtrace.Trace` at that wrapping point; do not replace it with a later log-site
  stack.
- A lower package does not log and return the same failure; the owning transport or
  process boundary records the final outcome.
- Configuration errors name the environment key and rule, never the raw value.
- Only `cmd/<name>/main.go` calls `os.Exit`; cleanup remains reachable below it.
- Expected context cancellation during shutdown is not an error log.
