# HTTP request debug logging

## Checklist

1. Config
   - Add `HTTPRequestDebugKey = "FIXTHE_HTTP_REQUEST_DEBUG"`.
   - Add `RequestDebug bool` on `config.HTTP`.
   - Parse with `enumValue` in `loadHTTP`, default `"false"`.
   - Document once in `backend/.env.example` next to the other HTTP keys.
   - Extend `TestLoadAPIDefaults` to assert default false.
   - Add `TestLoadHTTPRequestDebug` mirroring
     `TestLoadPostgreSQLQueryDebug`.
   - Add an invalid-value case to the HTTP reject-without-echo tests.

2. Observability
   - Add `FieldHTTPRequestHeaders`, `FieldHTTPResponseHeaders`,
     `FieldHTTPRequestQuery`.
   - Console: hide those plus `FieldHTTPRequest` / `FieldHTTPResponse`
     from tint attributes when projecting inbound debug blocks; write
     labeled following lines under the existing output mutex. Do not
     change outbound LLM/Git console aliases for the same body fields
     when they appear on `llm.request.completed` /
     `git.request.completed`.
   - Add a JSON record test that a completed debug record can carry the
     new fields. Add a console test that inbound debug blocks are
     physical lines, not quoted `request="..."`.
   - Update `observability.Log` comment: inbound debug completed
     records are a fourth explicit payload exception.

3. AccessLog
   - Thread `requestDebug bool` through `AccessLog` and
     `BoundaryOptions`.
   - Wire `apiConfig.HTTP.RequestDebug` in
     `backend/internal/bootstrap/api.go`.
   - Default path (`requestDebug=false`): keep today's
     `statusRecorder` + failure snapshot. Existing tests must keep
     passing with `false`.
   - Debug path: wrap the writer with `responseCapture`; after the
     handler, append unredacted headers / raw query / raw bodies /
     truncated flags to `http.request.completed`; skip
     `logFailureSnapshot`.
   - Preserve `Unwrap`, first-write status semantics, and
     `recordInternalError`.

4. Tests
   - Off + success with password body and Cookie header: completed has
     no payload / header fields.
   - Off + 4xx: `http.request.failed` still redacts and 4KB-caps.
   - On + success: completed INFO contains plaintext password, Cookie,
     Authorization, raw query, response body and Set-Cookie. No
     `http.request.failed`.
   - On + 4xx/5xx: no `http.request.failed`; completed still has the
     dump; `WriteInternalError` still emits `http.request.error`.
   - On + body larger than `maxBodyBytes`: truncated flag true, prefix
     only, secret in the prefix still plaintext.
   - On + empty GET: header fields present, body/query fields omitted.
   - Enabled request through `telemetry.HTTPHandler` does not put
     payloads on the HTTP span.
   - `FIXTHE_LOG_LEVEL` is not the switch: a debug-level logger with
     `requestDebug=false` still omits payloads.

5. Specs
   - `.trellis/spec/backend/logging-guidelines.md`: fourth exception
     under What NOT to Log; new "Inbound HTTP Request Debug" section
     covering fields, levels, skip-failed, MaxBodyBytes ceiling, and
     console projection.
   - `.trellis/spec/backend/directory-structure.md`: HTTP boundary
     group lists `FIXTHE_HTTP_REQUEST_DEBUG`.

## Validation

```bash
cd backend && go test ./internal/platform/config ./internal/platform/httpserver ./internal/platform/observability
cd backend && go test ./internal/bootstrap ./internal/modules/auth/adapter/http ./internal/modules/projects/adapter/http
```

Full gate before claiming done:

```bash
make -C backend check
```

## Risks

- `AccessLog` signature change touches every direct test call in
  `server_test.go`. Keep the extra bool last so the edits stay
  mechanical.
- Console projection of `http.request` / `http.response` is shared
  with outbound LLM/Git. Inbound debug wants following-line blocks;
  outbound currently uses quoted `request=` / `response=` aliases.
  Branch on `event` (or on the new header fields being present), do
  not blindly strip those keys for every record.
- `responseCapture` must not break `WriteJSON`, which buffers then
  writes once. First `Write` still implies 200 if `WriteHeader` was
  not called.
- Do not log `request.URL.String()`. Query comes from `RawQuery`
  only; route stays the mux pattern.

## Rollback

Set `FIXTHE_HTTP_REQUEST_DEBUG=false` (or unset) and restart the API.
No data migration.
