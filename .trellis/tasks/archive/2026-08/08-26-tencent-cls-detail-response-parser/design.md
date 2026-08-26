# Technical Design: Tencent CLS Detail Response Parsing

## Boundaries

The change remains inside the trusted Tencent CLS adapter at
`backend/internal/modules/hooks/adapter/tencentcls/client.go` and the
incident-bound remediation tool adapter. Webhook ingress validates the incoming
callback, stores the complete callback Observation, and persists only
`normalized_alert` evidence; it does not call the provider detail client. The
application callback loader provides that stored callback only to the trusted
remediation resolver. No MCP client or browser runtime is introduced.

## Data Flow

```text
validated callback ingress
  -> complete inbound Observation + normalized_alert evidence

incident-bound evidence.tencent_cls_detail tool
  -> callback loader reads the stored callback server-side
  -> bounded GET with redirect allowlist
  -> final regional URL + fragment
  -> parse fragment RecordId
  -> fixed regional POST /cls_no_login?action=GetAlertDetail
  -> accept application/json or text/plain only for valid JSON detail bodies
  -> retry only Response.Error.Code=-1001 with bounded context-aware delays
  -> decode Response.Record
  -> parse Record.ResultsSnapshot
  -> project bounded operational evidence
  -> persist provider_detail evidence for the current remediation run
```

The final request URL is retained only for deriving the fixed provider API
origin/path. The fragment is parsed as a route URL so both `RecordId` and the
observed route syntax are handled without ad hoc substring slicing. Detail
resolution is never performed on the webhook ingress goroutine.

## Response Contract

The detail response parser first decodes one JSON object. If the object has a
`Response.Record` object, that record becomes the canonical root. The existing
direct-record and nested fixture forms remain accepted for compatibility. The
canonical snapshot is `Record.ResultsSnapshot`; its `AnalysisInfo` entries map
`AnalysisOriginal` to the existing `AnalysisInfoItem.RawResult` field, while
the provider's `AnalysisResultFormat`, `RawResults`, `ColNames`, `Columns`, and
`QueryParams` remain bounded raw JSON sections.

The `fetch` helper keeps strict media validation for the HTML page. For the
detail operation only, it accepts `application/json` and `text/plain`; the
subsequent JSON decoder remains authoritative, so arbitrary plain text is
still invalid.

## Security And Failure Handling

- The existing HTTPS host allowlist and redirect policy remain unchanged.
- The initial page response's final URL is validated before deriving the API
  URL, and the extracted record ID is validated with the existing bounds.
- `Response.Error.Code=-1001` is the only retryable response-level envelope
  condition. Its copied retry schedule is bounded below two minutes, waits honor
  context cancellation, and every attempt retains its own redacted log event.
- Only the canonical operational fields and result sections enter
  `OperationalEvidence`. Callback URLs, shield secrets, and unrelated
  envelope fields are never copied into the projection or ingress evidence.
- Response bytes are counted across both GET responses and every POST attempt;
  all existing response limits and typed connector outcomes remain in force.
- Invalid content type, malformed JSON, missing `Response.Record`, missing
  snapshot/analysis, and invalid IDs map to the existing invalid outcome.

## Compatibility And Rollback

The public `Client` and resolver port remain unchanged, while webhook ingress no
longer has a provider detail resolver dependency. The no-argument remediation
tool remains the only detail acquisition entry point, and its callback loader
keeps `DetailUrl` server-side. Rollback is limited to the adapter, ingress
boundary, and tool wiring; no schema or configuration migration is required.
Existing direct-record and nested fixture tests continue to cover compatibility,
while production-shaped and eventual-consistency fixtures cover the new flow.
