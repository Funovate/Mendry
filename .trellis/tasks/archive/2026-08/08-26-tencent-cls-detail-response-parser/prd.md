# Fix Tencent CLS detail response parsing

## Goal

Make the Tencent CLS detail resolver consume the same request and response
contract used by the public CLS detail page, so the incident-bound remediation
tool turns a stored webhook callback into the actual alert record and original
log evidence.

## Approved Boundary Adjustment

Webhook ingress validates and stores the complete callback observation and
persists only `normalized_alert` evidence. It does not resolve Tencent detail,
persist `provider_detail`, persist `connector_observation`, or depend on a
provider detail resolver. The existing no-argument
`evidence.tencent_cls_detail` tool and its incident-bound callback loader are
the only detail acquisition path; `DetailUrl` and `RecordId` never become model
arguments.

`Response.Error.Code=-1001` is treated as bounded eventual-consistency retry
state. The adapter retries only that response with context-aware delays and
returns retryable `OutcomeUnavailable` when the schedule is exhausted or
canceled. Retry timing is an optional constructor-compatible, validated and
copied schedule; production defaults stay below two minutes and tests inject
zero delays.

## Confirmed Provider Contract

- `GET https://alarm.cls.tencentcs.com/<short-code>` returns a `302` to a
  regional `*-monitor.cls.tencentcs.com` URL.
- The redirect fragment contains the real route parameters:
  `#/alert?RecordId=<uuid>&JumpDomainID=<uuid>`.
- The redirected `200` response is only an HTML SPA shell. It contains
  `h5-app.js` and no alert data.
- The page JavaScript calls
  `POST /cls_no_login?action=GetAlertDetail` with
  `{"RecordId":"<uuid>"}`.
- The endpoint returns `text/plain; charset=utf-8`, while the body is valid
  JSON shaped as `Response.Record.ResultsSnapshot`.
- The useful original log is under
  `ResultsSnapshot.AnalysisInfo[].AnalysisOriginal[]`.
- The response also contains callback and H5 shield secret material. Those
  fields must remain outside persisted evidence, logs, and model-visible data.

## Requirements

- Preserve the existing trusted Tencent CLS URL allowlist, redirect bounds,
  timeout bounds, response-size bounds, and safe connector error mapping.
- Webhook ingress must validate and store the complete callback Observation and
  only `normalized_alert` evidence; it must not fetch detail or persist
  `provider_detail` / `connector_observation`.
- Detail acquisition remains behind the incident-bound no-argument
  `evidence.tencent_cls_detail` tool and callback loader. Do not expose
  `DetailUrl` or `RecordId` as model arguments and do not add generic
  `web_fetch`.
- Follow the short-link redirect and derive the detail request `RecordId` from
  the final URL fragment, including the `JumpDomainID` route shape observed in
  production. Do not use the short-link path as a substitute for the UUID.
- Send the exact browser-equivalent `GetAlertDetail` POST body and accept a
  text/plain response only when its body is valid JSON.
- Treat `Response.Error.Code=-1001` as temporary eventual-consistency state;
  retry only that response with bounded, context-aware delays and return a
  retryable `OutcomeUnavailable` when retries are exhausted or canceled.
- Validate and copy injected retry timing. The default retry window must cover
  the observed race while remaining below two minutes; tests must not sleep
  production delays.
- Unwrap the `Response.Record` envelope before projecting record metadata and
  `ResultsSnapshot`.
- Preserve the provider's `AnalysisInfo`, including `AnalysisOriginal`, in the
  existing bounded operational-evidence projection so original log fields such
  as message, file, host, source, timestamp, level, path, and time survive.
- Keep provider control material such as callback URLs, `H5AlarmShield` secret
  fields, and unrelated response fields out of the evidence projection and
  existing operator/model boundaries.
- Keep compatibility with already supported fixture shapes where doing so
  does not weaken the production contract.

## Acceptance Criteria

- [x] A short-link adapter/tool fixture produces the initial GET, regional
      redirected GET, and POST to `cls_no_login?action=GetAlertDetail`.
- [x] Webhook ingress stores the complete callback observation and exactly one
      `normalized_alert` evidence record without fetching detail.
- [x] The POST body contains the UUID from the final URL fragment, not the
      original short-link path.
- [x] A `text/plain; charset=utf-8` response containing the provider JSON is
      accepted and parsed as one JSON value.
- [x] `Response.Record.RecordId`, alert identity, nested snapshot metadata, and
      `AnalysisInfo[].AnalysisOriginal[]` are projected into `OperationalEvidence`.
- [x] A `-1001` response retries with injected test timing, succeeds when the
      next response is ready, and aggregates response bytes across attempts.
- [x] Exhausted or canceled `-1001` retries return retryable
      `provider_detail_unavailable` / `OutcomeUnavailable` without another
      detail acquisition path.
- [x] The projected payload and provenance contain no callback URL,
      `H5AlarmShield.SecretID`, `H5AlarmShield.SecretText`, or other control
      material from the full response.
- [x] Malformed text/plain, non-JSON, wrong-envelope, oversized, redirect,
      timeout, and HTTP failure responses retain bounded safe error outcomes.
- [x] Existing Tencent CLS adapter, resolver, logging, and backend test suites
      pass without changing unrelated worktree changes.

## Out Of Scope

- Calling a Codex-host-only network tool from the Go service.
- Browser automation, JavaScript execution, arbitrary URL fetching, or provider
  write operations.
- Expanding the persisted evidence contract to store the complete raw
  `Response.Record` object.
