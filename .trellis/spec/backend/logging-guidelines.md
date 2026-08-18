# Logging Guidelines

> How logging is done in this project.

---

## Overview

Backend processes use one injected `log/slog` logger writing to stdout.
`internal/platform/observability` owns logger construction, stable field names,
and event-name constants. Packages do not create default/global loggers.
`FIXTHE_LOG_FORMAT` accepts `console` or `json`: `development` defaults to the
human-readable console handler, while `test`, `staging`, and `production`
default to the stable JSON handler. Console output enables ANSI colors only
when the injected writer is a terminal, so redirects remain color-free.

## Log Levels

- `DEBUG`: detailed normal lifecycle and successful database operations.
- `INFO`: startup/shutdown, completed HTTP requests, and normal state changes.
- `WARN`: degraded but recoverable operation or policy denial.
- `ERROR`: unexpected failure that ends an operation or violates a lifecycle
  guarantee.

The configured minimum level is independent per process and is one of `debug`,
`info`, `warn`, or `error`.

## Structured Logging

Every JSON record contains `timestamp`, `level`, `message`, `event`, `service`,
`version`, `commit`, and `environment`. Boundary-specific records add
`component`, `outcome`, `duration_ms`, method, route, or status as typed fields.
When the supplied context contains a valid span, the logging helper also adds
`trace_id` and `span_id`; callers do not copy these identifiers manually.

```go
observability.Log(ctx, logger, slog.LevelInfo,
    observability.EventProcessStarted, "process started",
    slog.String(observability.FieldComponent, "api"),
)
```

The message is for people; `event` is the stable query key. Console records are
a compact but diagnosable development status stream: timestamps use
`YYYY-MM-DD HH:MM:SS`; component becomes a message prefix such as `[postgres]`;
trace, request, and transaction identifiers use the display-only keys `trace`,
`req`, and `tx` and are shortened to eight characters;
span, event, and process identity fields remain hidden. Bounded operation,
outcome, pool, and timing diagnostics remain visible with shorter display-only
keys such as `command`, `count`, and `took`. Switch to JSON when complete IDs or
the stable machine contract are needed. New stable event and field names must be
constants in the observability package and covered by a JSON record test.

Diagnostic stacks and debug SQL are the two intentional multi-line console
projections. The console handler removes top-level `error_stack`, panic
`stack`, and `db.query.text` from tint's quoted attribute rendering, writes
the normal event line first, and then writes those texts verbatim. Source
frames must remain physical lines in `path/file.go:line` form so GoLand and
other Go-aware consoles can hyperlink them. Debug SQL must remain physical
newlines so the statement can be copied into `psql`. JSON keeps the same
values as structured string fields, where escaped newlines are required by
the JSON format. All handler derivatives share one output mutex so an event
and its following stack or SQL are not interleaved with another slog record.

## What to Log

- Process start, ready, stopping, and stopped boundaries.
- One completion record per HTTP request with method, matched route pattern,
  status, and duration. Log the route pattern, never the raw URL/query string.
- One additional failure snapshot record when the final status is `>= 400`,
  except the expected `499` client-closed outcome. See
  [Error Request Snapshots](#error-request-snapshots).
- One `http.request.error` record when an HTTP adapter maps an unknown error to
  `500 internal_error`. See [Internal Server Error Diagnostics](#internal-server-error-diagnostics).
- PostgreSQL query, transaction, pool-state, migration, and lock-cleanup
  outcomes with stable operation names and classifications.
- Redis command/pipeline and pool-state outcomes with stable command names,
  aggregate counts, duration, and classifications.
- Outbound LLM HTTP calls emit one `llm.request.completed` record with
  operation, host, path, optional model, optional status, duration, outcome,
  a redacted provider response body when one was read, and a
  low-cardinality `error_class` on failure. See
  [Outbound LLM Requests](#outbound-llm-requests).
- Outbound Git `ls-remote` probes emit one `git.request.completed` record with
  operation, public host, public path, duration, outcome, a redacted public
  command identity, captured stdout/stderr when present, and a
  low-cardinality `error_class` on failure. See
  [Outbound Git Requests](#outbound-git-requests).
- Later adapters log bounded operation identity and outcome, not payloads.

## What NOT to Log

Never log raw environment values, secrets, credentials, headers, cookies, query
strings, request/response bodies, database arguments, user source, evidence, or
arbitrary high-cardinality identifiers. A redacted string representation is
defense in depth, not permission to pass a secret to the logger.

There are three explicit exceptions:

- The failure-only request snapshot described in
  [Error Request Snapshots](#error-request-snapshots) covers a redacted,
  truncated body/query snapshot on `status >= 400` except client-closed `499`.
- The original Go error and panic diagnostics described in
  [Internal Server Error Diagnostics](#internal-server-error-diagnostics) are
  retained in private server logs. By explicit project decision, these fields
  are not sanitized even though a third-party error string may itself contain
  sensitive dependency detail.
- `FIXTHE_POSTGRES_QUERY_DEBUG=true` attaches one interpolated SQL statement
  to emitted `db.query.completed` log records. `$n` placeholders are replaced
  with PostgreSQL literals so the field can be copied into `psql`. The switch
  is independent of `FIXTHE_LOG_LEVEL`, does not change query levels, and
  never copies SQL onto spans or metrics. Bind values are not redacted.

Neither of the first two exceptions permits independently attaching headers,
cookies, response bodies, raw SQL, bind values, Redis key/value data, or
success-path payloads. The query-debug switch is the only permission to log
raw SQL or bind values, and it applies only to `db.query.text`.

PostgreSQL query records otherwise never contain raw SQL, bind values, returned
rows, connection URLs, credentials, or database error messages. Query events
use a declared operation name or a sanitized SQL verb fallback. Transaction IDs
are internal log correlation only and are never business identifiers. When
query debug is on, JSON stores the interpolated statement in `db.query.text`;
console writes that same text on following physical lines. Successful queries
remain `DEBUG`, so default `info` still hides successful SQL.

An error propagated from PostgreSQL to `WriteInternalError` is instead governed
by the explicit original-error exception below; do not copy SQL or bind values
into separate log attributes.

Redis records use only `Cmder.Name()` after low-cardinality validation. They
never inspect `Args()` or `String()` and never contain keys, values, TTL
arguments, URLs, credentials, returned values, or Redis server error messages.
go-redis maintenance notifications remain disabled so the dependency's global
logger cannot bypass the injected application logger with endpoint handoff
diagnostics.

Session cookies, raw session tokens, password material, and Redis session values
must never be independently attached to startup diagnostics, structured logs,
spans, or metrics. The original-error exception below does not inspect or enrich
third-party error text, so callers must not construct errors by interpolating
these values.

## Internal Server Error Diagnostics

Use `httpserver.WriteInternalError(writer, request, err)` whenever an HTTP
adapter maps an unknown application/dependency error. It keeps the client and
server contracts deliberately different:

- Client response: normally `500 internal_error` with the existing safe message
  and request ID. It never contains `err.Error()`, a cause, panic value, or stack.
- Expected cancellation: when the cause chain contains `context.Canceled` and
  the HTTP request context is also canceled, record a bodyless `499` and only the
  INFO completion event. Do not emit `http.request.error` or a failure snapshot.
  A dependency cancellation under an active request remains a normal 500.
- Server record: one ERROR-level `http.request.error`, correlated by request and
  trace, with method, matched route, status, `error_type`, `error_message`, and
  bounded `error_causes` entries containing original type/message pairs. It also
  contains `error_stack` with function/file/line frames and
  `error_stack_source`.
- `error_stack_source=wrapped_error` means the stack was captured while a cause
  was wrapped; the HTTP boundary selects the deepest such trace.
  `error_stack_source=http_boundary` is an explicit fallback captured when the
  adapter submits an error that has no retained stack. It identifies the reporting
  adapter only and must not be interpreted as the dependency failure origin.
- Console format keeps these fields visible for local diagnosis. JSON format
  preserves the same stable field names and complete IDs.
- Expected 4xx categories continue to use `WriteError` and never emit
  `http.request.error`.

`Recover` owns panics separately through `http.request.panic_recovered`. That
event contains `panic_type`, original `panic_value`, and `stack`; it must not
also register the panic as an ordinary `http.request.error`.

The raw diagnostic choice is intentional because these logs are private and
operator-facing. It trades log-level secrecy for complete diagnosis. This does
not weaken the client response boundary or authorize logging request material
outside the redacted failure snapshot.

Do not enable slog `AddSource` or call `debug.Stack()` at the logging boundary to
claim an ordinary error origin. Both observe only the already-unwound HTTP/logging
path. Capture program counters at stack-aware error wrapping points and let the
HTTP boundary format the retained trace.

Console output must resemble a native Go stack rather than a quoted slog value:

```text
2026-08-14 16:05:17 ERR [httpserver] request error ... error_stack_source=wrapped_error
fixthe/backend/internal/platform/postgres.(*Pool).acquire
    /workspace/backend/internal/platform/postgres/pool.go:196
```

Wrong console projection:

```text
error_stack="fixthe/backend/...acquire\\n\\t/workspace/.../pool.go:196"
```

The wrong form is human-readable only after manual unescaping and is not
hyperlinkable by GoLand. Console tests must assert physical newlines, independent
`file.go:line` frames, absence of literal `\\n` stack fragments, and atomic
event-plus-stack output under concurrent handlers. JSON tests must continue to
assert the original `error_stack` and `stack` field values.

## Error Request Snapshots

This is the only, bounded exception to "never log request bodies or query
strings". It exists so operators can reproduce a failed request from its
inputs. It is not permission to log payloads on the success path, and it must
not be widened to headers, cookies, or response bodies.

Trigger:

- Emit a separate `http.request.failed` record only when the final HTTP status
  is `>= 400` and is not the expected client-closed `499` outcome.
- Leave `http.request.completed` unchanged: same fields, same `INFO` level,
  same success and failure coverage.
- Log level is `WARN` for `4xx` and `ERROR` for `5xx`.

Scope:

- Snapshot sources are the JSON request body and the query string only.
- Never include headers, cookies (especially the session cookie), response
  bodies, raw URLs, or unredacted query strings.

Redaction:

- Parse the body as JSON and walk objects/arrays. Apply a case-insensitive
  contains match against a denylist of field-name patterns: `password`,
  `token`, `secret`, `authorization`, `apikey`, `api_key`, `api-key`,
  `credential`, `value`.
- `value` is required because `POST /api/v1/projects/{projectKey}/secrets`
  carries plaintext secret material in that field. `api-key` is the hyphenated
  form of `apikey`/`api_key`; a contains match on `apikey` does not catch it.
- Replace a matching field's entire value with `"[redacted]"`.
- Apply the same denylist to query parameter names.
- If the body is not valid JSON, do not log any raw body bytes. Record
  `body_parse_error: true` only.
- Present snapshot payloads use `request_body` and `request_query`.

Truncation:

- After redaction, serialize body and query independently and cap each at
  4KB on a UTF-8 boundary.
- When a field is truncated, set `body_truncated` or `query_truncated` to
  `true`. Never write the discarded tail.

## Outbound LLM Requests

Inbound `http.request.completed` records the console or API call that reached
FixThe. It does not describe the provider call made on that request's behalf.
Project LLM probes and the remediation OpenAI adapter therefore emit one
`llm.request.completed` record per outbound HTTP attempt.

```go
observability.LogLLMRequest(ctx, logger, observability.LLMRequest{
    Operation: "chat.completions",
    Host:      "api.openai.com",
    Path:      "/v1/chat/completions",
    Model:     "gpt-5.6",
    Status:    401,
    Duration:  elapsed,
    Err:       err,
})
```

Required fields:

- `component=openai`
- `llm.operation.name`: `models.list` or `chat.completions`
- `http.host` and `http.path` from `observability.HTTPIdentity`. The helper
  keeps host and escaped path and drops userinfo and query. Unparseable URLs
  become `invalid` rather than logging the raw string.
- `duration_ms` and `outcome`

Optional fields:

- `llm.model` when the call selected a model. `models.list` omits it.
- `http.status` when a response arrived.
- `http.request` whenever the adapter sent a JSON body (`models.list` has
  none). Same redaction and 4KB cap as the response; set
  `http.request_truncated=true` only when truncated.
- `http.response` whenever the adapter read a provider body, on success and
  on failure. JSON is field-redacted, then `sk-…` / `Bearer …` values are
  replaced, then the text is capped at 4KB on a UTF-8 boundary.
  `http.response_truncated=true` is set only when that cap applies.
  Usage counters such as `prompt_tokens` stay visible; they are not treated
  as secrets.
- `error_class` on failure only. Stable values are `canceled`, `timeout`,
  `dns`, `tls`, `network`, `http_4xx`, `http_5xx`, `decode`, and `internal`.

Levels follow the other adapters: successful calls are `DEBUG`; `canceled` is
`INFO`; `timeout` and `http_4xx` are `WARN`; remaining failures are `ERROR`.
A nil logger is a no-op so unit tests can keep constructing adapters without
observability.

Never log API keys or Authorization headers. Outbound request and response
bodies are logged only after redaction so operators can tell whether the
provider rejected the payload we sent or returned an unexpected body.
Timeouts, DNS, and TLS failures have no response body and therefore no
`http.response`. Application code may still map a failed probe to
`ErrLLMUnreachable` for the client; the outbound record is what operators use
to distinguish a bad URL, a rejected key, a missing model, or a provider
outage.

## Outbound Git Requests

Inbound `http.request.completed` records the console or API call that reached
FixThe. It does not describe the `git ls-remote` attempt made on that
request's behalf. Config-wizard repository probes therefore emit one
`git.request.completed` record per executed command.

```go
observability.LogGitRequest(ctx, logger, observability.GitRequest{
    Operation: "ls-remote",
    Host:      host,
    Path:      path,
    Duration:  elapsed,
    Request:   []byte("git ls-remote --symref --heads https://git.example.com/app.git"),
    Response:  output,
    Err:       err,
})
```

Required fields:

- `component=git`
- `git.operation.name`: `ls-remote`
- `http.host` and `http.path` from `observability.HTTPIdentity` of the
  **public** remote URL. The helper keeps host and escaped path and drops
  userinfo and query. Unparseable URLs become `invalid` rather than logging
  the raw string. Never derive identity from the authenticated exec target.
- `duration_ms` and `outcome`

Optional fields:

- `http.request` is the public command identity, for example
  `git ls-remote --symref --heads https://git.example.com/app.git`. It never
  contains the authenticated HTTPS URL, token, PEM, `GIT_SSH_COMMAND`, or
  temp key path.
- `http.response` is captured stdout on success. On failure it is stdout
  plus stderr when a body was captured. The shared snapshot pipeline
  field-redacts JSON, replaces `sk-…` / `Bearer …` values, replaces HTTPS
  userinfo (`https://user:token@host` → `https://[redacted]@host`), replaces
  PEM private-key blocks, then caps the text at 4KB on a UTF-8 boundary.
  `http.response_truncated=true` is set only when that cap applies.
- `error_class` on failure only. Stable values are `canceled`, `timeout`,
  and `command`. Do not invent an HTTP status from a git exit code.

Omit `http.status` and `llm.model`; Git has neither.

Levels follow the other adapters: successful calls are `DEBUG`; `canceled` is
`INFO`; `timeout` is `WARN`; remaining failures are `ERROR`. A nil logger is
a no-op so unit tests can keep constructing adapters without observability.

Never log API tokens, HTTPS userinfo, PEM private-key material,
`GIT_SSH_COMMAND`, or temp key paths. Application code still maps a failed
probe to `ErrGitUnreachable` for the client; the outbound record is what
operators use to distinguish a bad URL, a rejected credential, DNS failure,
or timeout. Remediation `adapter/git.Reader` clone/fetch/ls-tree/cat-file
commands remain uninstrumented and can reuse `LogGitRequest` later.
