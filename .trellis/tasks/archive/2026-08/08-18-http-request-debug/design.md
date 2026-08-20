# HTTP request debug logging

## Boundaries

- `config` owns the new key, default, and validation. `loadHTTP` /
  `LoadAPI` is the only loader. migrate / seed / bootstrap-admin do not
  read the switch.
- `httpserver.AccessLog` is the only place that may attach inbound
  request or response payloads to a log record. Feature HTTP adapters
  stay unaware of the switch.
- `observability` owns stable field names and console display aliases.
  Do not invent local keys in `httpserver`.
- OpenTelemetry spans and metrics stay on the existing safe contract.
  The debug switch never crosses that boundary.

## Config

Add `FIXTHE_HTTP_REQUEST_DEBUG` beside the other HTTP keys.

- Default: `false` when unset.
- Allowed values: `true`, `false` (case-insensitive) via existing
  `enumValue`. Do not add a generic bool parser.
- Invalid values: `fieldError` with the key and a constraint, never the
  raw value.
- Surface: `config.HTTP.RequestDebug bool`.

`backend/.env.example` documents the key once, default `false`, and
warns that enabled logs contain complete request/response bodies,
headers, cookies, and secret-bearing fields.

`BoundaryOptions` grows `RequestDebug bool`. Zero value keeps every
existing `Boundary(...)` test on the default-off path.
`bootstrap.RunAPI` passes `apiConfig.HTTP.RequestDebug`.

`AccessLog` grows a `requestDebug` argument (or a small options struct
if the signature is already painful). Production path is
`AccessLog(logger, maxBodyBytes, requestDebug, next)`. Existing unit
tests that call `AccessLog` directly pass `false` unless they are the
new debug cases.

## Data Flow

```text
AccessLog
  if request.Body present:
      wrap with bodyCapture(limit = maxBodyBytes)   # already exists
  if requestDebug:
      wrap writer with responseCapture(limit = maxBodyBytes)
  next.ServeHTTP
  emit http.request.completed (INFO)
      identity fields always
      if requestDebug: append unredacted dump
  if !requestDebug && status >= 400 && status != 499:
      emit http.request.failed (existing redacted snapshot)
  if internalError:
      emit http.request.error (unchanged)
```

`bodyCapture` already stops at `maxBodyBytes`. AccessLog wraps
`LimitBody`, so a well-formed request cannot exceed that many bytes
anyway. Debug mode still uses the same capture; it just logs the raw
bytes instead of running `buildBodySnapshot`.

`responseCapture` is new and only constructed when `requestDebug` is
true. Default-off requests must not buffer response bytes.

```text
responseCapture
  embeds statusRecorder (keep status / internalError / Unwrap)
  Write/WriteHeader record status as today
  Write copies min(n, remaining) into an internal buffer
  Unwrap returns the underlying ResponseWriter
```

Do not replace `statusRecorder` on the default path. A disabled switch
must keep today's type and allocation profile.

## Log Contract

Attach the dump to the existing `http.request.completed` record. Do not
invent a second event. Do not change the level.

| Concern | JSON field | Console |
|---|---|---|
| Request headers, including Cookie and Authorization | `http.request_headers` | `request_headers` |
| Raw query string (`url.RawQuery`, not redacted map) | `http.request_query` | `request_query` |
| Raw request body | `http.request` | `request` |
| Request body hit `MaxBodyBytes` | `http.request_truncated` | `request_truncated` |
| Response headers, including Set-Cookie | `http.response_headers` | `response_headers` |
| Raw response body | `http.response` | `response` |
| Response body hit `MaxBodyBytes` | `http.response_truncated` | `response_truncated` |

Reuse the existing outbound constants for body fields
(`FieldHTTPRequest`, `FieldHTTPResponse`,
`FieldHTTPRequestTruncated`, `FieldHTTPResponseTruncated`). Add two
new constants for headers and one for the raw inbound query:

- `FieldHTTPRequestHeaders = "http.request_headers"`
- `FieldHTTPResponseHeaders = "http.response_headers"`
- `FieldHTTPRequestQuery = "http.request_query"`

Do **not** reuse failure-snapshot `request_body` / `request_query` /
`body_truncated` on the completed record. Those names stay bound to
the redacted `http.request.failed` contract.

Header encoding: one string, each header as `Name: value`, multiple
values repeated on their own line, names in canonical MIME form
(`http.Header` iteration). Sort header names so the field is
deterministic in tests. Do not drop hop-by-hop headers; this is a
debug dump.

Empty bodies omit `http.request` / `http.response` rather than logging
an empty string. Empty query omits `http.request_query`. Header maps
are always present when debug is on (even if empty) so operators can
see that capture ran.

Truncated flags appear only when the matching body was actually cut.

Console: bodies and header blocks are large and often multi-line. Treat
`http.request`, `http.response`, `http.request_headers`,
`http.response_headers`, and `http.request_query` like
`db.query.text`: strip them from tint's quoted attributes and write
each as a labeled following block under the shared output mutex.

```text
2026-08-18 10:43:18 INF [httpserver] request completed trace=cbad87f9 req=b8dc3be5 method=GET route="GET /api/v1/projects/{projectKey}/configuration" status=200 took=121ms
request_headers:
Cookie: fixthe_session=...
Authorization: Bearer ...
request_query:
env=prod
request:
{"name":"..."}
response_headers:
Content-Type: application/json
response:
{"data":{...}}
```

JSON keeps the same values as structured string fields.

## Compatibility

- Default off is a no-behavior-change for every existing AccessLog and
  Boundary test.
- `http.request.failed` remains the operator query for redacted
  failures when debug is off. When debug is on, search
  `http.request.completed` instead; document that in the spec.
- Client responses, error envelopes, and request-id headers do not
  change.
- Outbound `http.request` / `http.response` field names stay shared.
  Inbound debug uses them on a different event
  (`http.request.completed` vs `llm.request.completed` /
  `git.request.completed`). That is acceptable: `event` + `component`
  disambiguate.

## Tradeoffs

- INFO + full dump is noisier and more dangerous than the SQL switch.
  Accepted: the operator asked for one switch at default `info`.
- Skipping `http.request.failed` while debug is on removes the
  redacted duplicate. Operators who filter only on
  `event=http.request.failed` will miss failures until they turn the
  switch off. Accepted: two copies of the same request, one plaintext
  and one `[redacted]`, is worse.
- Reusing `MaxBodyBytes` as the response capture ceiling avoids a
  third env key. A 10 MiB list response can still produce a 10 MiB
  log line. Accepted versus unbounded buffering.

## Rollback

Unset or set `FIXTHE_HTTP_REQUEST_DEBUG=false` and restart the API.
No schema, no client contract, no data migration.
