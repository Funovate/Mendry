# Implementation Plan: Tencent CLS Detail Response Parsing

## Ordered Work

1. [x] Run GitNexus upstream impact for every existing symbol that will change:
   `Client.Resolve`, `Client.fetch`, `extractRecordID`,
   `parseDetailResponse`, and any parser helper whose contract is modified.
   Record risk before editing.
2. [x] Add production-shaped adapter fixtures for the redirect fragment, exact POST
   body, `text/plain` JSON response, `Response.Record` envelope, and nested
   `AnalysisOriginal` log data.
3. [x] Update `Client.Resolve` to use the final redirected request URL when
   extracting `RecordId` and when building the fixed detail API URL.
4. [x] Update the detail-only media contract and response parser to accept and
   unwrap the actual provider response while retaining strict JSON decoding.
5. [x] Map `AnalysisOriginal` into the existing evidence projection and verify that
   provider control material remains excluded.
6. [x] Add negative tests for malformed plain text, wrong envelopes, and missing
   route IDs; retain existing bounds, redirect, timeout, logging, and resolver
   tests.
7. [x] Run formatting and focused backend tests, then the backend quality gates.
8. [x] Run GitNexus `detect_changes` before any commit and inspect unexpected
   symbols/processes. Do not commit unrelated worktree changes.

## Validation Commands

```bash
cd backend && gofmt -w internal/modules/hooks/adapter/tencentcls/*.go
cd backend && go test ./internal/modules/hooks/adapter/tencentcls ./internal/modules/hooks/application
cd backend && go test ./...
cd backend && go vet ./...
```

Run the repository's `make check` target if its prerequisites are available.

## Risk And Rollback Points

- Editing `Client.Resolve` or `Client.fetch` affects webhook ingestion and
  remediation detail resolution; stop and warn if GitNexus reports HIGH or
  CRITICAL risk.
- Keep the patch limited to the Tencent CLS adapter and its tests unless a
  failing contract test proves another boundary must change.
- Rollback is a source-only revert of the adapter/parser, ingress boundary, and
  tool wiring patch; no database rollback is needed.

## Approved Boundary And Retry Follow-Up

- [x] Removed ingress-only `ProviderDetailResolver`, `Options.Detail`,
      `Service.detail`, `providerDetailRecord`, and
      `unavailableProviderDetail`; bootstrap no longer injects a detail client
      into webhook ingress.
- [x] Kept the complete callback Observation and one `normalized_alert`
      evidence record at ingress; provider detail remains behind the
      incident-bound no-argument `evidence.tencent_cls_detail` tool.
- [x] Added validated, copied retry timing injection to the Tencent client.
      Only `Response.Error.Code=-1001` retries; delays honor context and the
      default schedule remains below two minutes.
- [x] Added success, exhaustion, cancellation, ingress-boundary, and redaction
      regressions without sleeping production retry delays.
- [x] Re-run `gofmt`, focused tests, `go test ./...`, `go test -race ./...`,
      `go vet ./...`, `go build ./cmd/...`, and `git diff --check` before handoff.
- [x] Follow-up review fixes reject failed-ingest remediation emission, empty or
      null `AnalysisInfo`, and untrusted detail projections at resolver and gateway
      boundaries; new regressions cover contextual and contradictory results.
- [x] Tencent response logs recursively redact valid JSON, omit malformed detail
      bodies behind bounded diagnostics, sanitize detail-page HTML control
      assignments and URLs, and use Chinese explanatory comments for new exported
      contracts.
- [x] A non-retryable detail-tool failure now records a safe observation and
      reopens bounded Docker/Git fallback tools; only one retryable detail retry is
      retained, preventing repeated provider failures from exhausting the run
      budget before runtime/code investigation.