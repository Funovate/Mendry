# HTTP request debug logging

## Goal

Operators can turn on `FIXTHE_HTTP_REQUEST_DEBUG` and see the complete
inbound request and response on the existing INFO
`http.request.completed` record — including bodies, query, headers, and
cookies, without redaction or the 4KB failure-snapshot cap — so a
console or API call can be reproduced from logs the same way
`FIXTHE_POSTGRES_QUERY_DEBUG` exposes interpolated SQL.

## Background

Inbound access logs are data-minimized by design.

- `httpserver.AccessLog` (`backend/internal/platform/httpserver/server.go:132-169`)
  always emits one `http.request.completed` INFO record with method,
  matched route pattern, status, duration, and request id. It never logs
  the raw URL.
- On `status >= 400` except client-closed `499`, it emits a second
  `http.request.failed` record with a redacted, 4KB-truncated JSON body
  and query snapshot (`server.go:166-167`, `server.go:239-277`,
  `snapshot.go:71-133`). Headers, cookies, and response bodies are
  forbidden.
- `.trellis/spec/backend/logging-guidelines.md` "What NOT to Log" and
  "Error Request Snapshots" encode that contract. Payload exceptions
  today are the failure snapshot, unsanitized `http.request.error` /
  panic diagnostics, and `FIXTHE_POSTGRES_QUERY_DEBUG`.

Outbound LLM / Git already log redacted request and response on every
attempt. That is a different boundary. This task is only the inbound
API / console surface.

The 2026-08-14 failure-snapshot work explicitly left success-path and
response bodies out of scope. This task is a new, opt-in exception.

SQL precedent (`FIXTHE_POSTGRES_QUERY_DEBUG`): independent of
`FIXTHE_LOG_LEVEL`; attaches extra fields to the existing completed
record; does not change levels; never copies debug material onto spans
or metrics; no production deny list.

HTTP completed records are already INFO. Attaching the dump there means
`FIXTHE_HTTP_REQUEST_DEBUG=true` alone is enough at default `info`.

## Requirements

- **R1.** Add `FIXTHE_HTTP_REQUEST_DEBUG`, default `false`. Accepted
  values are `true` and `false` (case-insensitive). Invalid values fail
  `LoadAPI` / `loadHTTP` with a field error that does not echo the raw
  value. `FIXTHE_LOG_LEVEL=debug` alone does not attach request or
  response payloads.
- **R2.** When the switch is off, inbound logs stay unchanged: completed
  records remain identity-only; failure snapshots remain the only,
  redacted, 4KB-truncated body/query exception; headers, cookies, and
  response bodies stay absent.
- **R3.** When the switch is on, every `http.request.completed` record
  includes the inbound request and outbound response: raw body, raw
  query string, request headers (including `Cookie` and
  `Authorization`), and response headers (including `Set-Cookie`). Do
  not redact field names or values. Do not apply the 4KB
  failure-snapshot cap. Secret-bearing fields are logged as received.
  Completed stays INFO, so default `FIXTHE_LOG_LEVEL=info` shows the
  dump.
- **R4.** Capture is still bounded by `FIXTHE_HTTP_MAX_BODY_BYTES`
  (default 1 MiB, max 10 MiB) for both request and response. If a body
  exceeds that ceiling, log the captured prefix and set the matching
  truncated flag. That ceiling is not the 4KB snapshot cap.
- **R5.** Debug fields are written only to the application logger
  (console or JSON). OpenTelemetry spans and metrics never receive
  bodies, headers, cookies, or query strings.
- **R6.** The switch does not change HTTP access-log levels. Completed
  stays INFO; `http.request.error` and panic records stay as they are.
- **R7.** When the switch is on, do not emit `http.request.failed`.
  Completed already has the unredacted request and response. When the
  switch is off, failure-snapshot behavior is unchanged.
- **R8.** Specs and `.env.example` document the switch as an explicit,
  unsafe debug exception. Default comments continue to say ordinary
  inbound records do not contain payloads, headers, or cookies.
- **R9.** Only the API HTTP boundary honors the switch. Outbound LLM /
  Git records, PostgreSQL, and Redis stay on their current contracts.

## Technical Notes

- Config keys live in `backend/internal/platform/config/config.go`.
  `loadHTTP` (`config.go:489-534`) has no boolean today. PostgreSQL uses
  `enumValue(..., "false", {true,false})` for `QueryDebug`.
- `config.HTTP` (`config.go:73-81`) is API-only. migrate / seed /
  bootstrap-admin do not load HTTP.
- `backend/.env.example` must document every `FIXTHE_*` key exactly once
  (`TestEnvironmentExampleDocumentsEveryConfigurationKey`).
- Composition root wires `httpserver.Boundary` in
  `backend/internal/bootstrap/api.go:267-272` with `MaxBodyBytes` and
  `CORSAllowedOrigin` only.
- `Boundary` (`middleware.go:227-255`) passes `MaxBodyBytes` into
  `AccessLog` as the request-body capture limit. Capture is a transparent
  `bodyCapture` (`snapshot.go:34-61`) that already exists for failure
  snapshots. `statusRecorder` (`server.go:279-318`) does not buffer the
  response body today. `Unwrap` must stay so `Flush` / `Hijack` keep
  working. Current API has no SSE / EventStream consumer.
- Request bodies are already hard-capped by `LimitBody` at
  `FIXTHE_HTTP_MAX_BODY_BYTES`. AccessLog wraps LimitBody, so
  `bodyCapture` already sees at most that many bytes.
- Stable field names must be `observability` constants and have a JSON
  record test. Console already aliases `http.request` / `http.response`
  for outbound records (`logging.go:183-190`). Failure snapshots use
  `request_body` / `request_query` and must keep those names.
- `AccessLog` is called from `Boundary` and from
  `backend/internal/platform/httpserver/server_test.go`. Existing
  `BoundaryOptions` callers rely on the zero value of any new bool.

## Disposition

Archived 2026-08-20. `FIXTHE_HTTP_REQUEST_DEBUG`, AccessLog dump, config/AccessLog tests, and logging specs are landed. Independent observability slice; not an incident-MVP dependency.

## Acceptance Criteria

- [ ] AC1. Unset or `false` `FIXTHE_HTTP_REQUEST_DEBUG` keeps current
      AccessLog tests: no headers, cookies, response bodies, or
      unredacted success-path payloads; 4xx/5xx still emit the redacted
      `http.request.failed` snapshot. (R2, R7)
- [ ] AC2. `FIXTHE_HTTP_REQUEST_DEBUG=true` causes INFO
      `http.request.completed` to contain request body, raw query,
      request headers (including Cookie / Authorization), response
      headers (including Set-Cookie), and response body without
      redaction and without the 4KB snapshot cap. Default `info` is
      enough. (R1, R3, R6)
- [ ] AC3. A body larger than `FIXTHE_HTTP_MAX_BODY_BYTES` is truncated
      to that ceiling and marked truncated. A password / session cookie
      inside the captured prefix remains plaintext. (R3, R4)
- [ ] AC4. When the switch is on, a 4xx/5xx request does not emit
      `http.request.failed`. `http.request.error` for unknown 500s is
      unchanged. (R6, R7)
- [ ] AC5. The same enabled request still produces a span/metric without
      those payloads. (R5)
- [ ] AC6. `FIXTHE_LOG_LEVEL=debug` alone does not attach payloads. (R1)
- [ ] AC7. Invalid switch values fail HTTP/API config load without
      echoing the raw value. (R1)
- [ ] AC8. `backend/.env.example` documents the new key exactly once,
      default `false`, with a warning that enabled logs contain secrets,
      cookies, and headers. (R8)
- [ ] AC9. Logging and directory-structure specs describe the
      default-off contract and this explicit debug exception. (R8, R9)

## Out Of Scope

- Changing outbound LLM / Git snapshot policy.
- Changing `FIXTHE_POSTGRES_QUERY_DEBUG` or Redis command logging.
- Environment-based deny list (`production` may still enable the switch).
- Putting payloads on spans, metrics, or client error responses.
- New operator UI / runtime toggle; env-var only, process restart
  required.
- Streaming / SSE response semantics beyond preserving `Unwrap`.
- A separate debug max-bytes setting.
- Changing completed / failed / error log levels.
