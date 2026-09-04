# Technical Design

## Boundaries

`hooks/application` owns inbound normalization and defines a narrow `FingerprintAnalyzer` port. It does not import HTTP, PostgreSQL, provider SDKs, or remediation internals. The composition root injects an adapter that translates this port to the existing OpenAI-compatible client. The adapter sends a `remediationdomain.ModelTurn` with no tools and a small token budget.

The flow becomes:

```text
POST /hooks/{token}
  -> validate UTF-8/non-empty body
  -> LookupWebhookToken
  -> return 202 {"accepted": true}
  -> detached background processing
  -> FingerprintAnalyzer.Normalize(projectID, sourceID, raw)
       -> bounded/redacted JSON model request
       -> strict model response, or local fallback
  -> CreateInbound(raw message, final fingerprint)
  -> IngestInbound(title, final fingerprint)
```

The model remains ordered before `GetIncidentByFingerprint`, but that entire normalization and persistence sequence runs asynchronously after request validation and token lookup. The background context preserves request trace values while ignoring request cancellation; the analyzer still applies its configured child timeout. Model failure is intentionally non-fatal and falls back locally. Persistence failures are reported through a hooks-owned failure reporter without logging the token or raw payload.

## Normalized Contract

`NormalizedInbound` contains:

- `Title`: bounded incident title.
- `Fingerprint`: `ai:v1:<sha256>` or `fallback:v1:<sha256>`.
- `Message`: the original bounded UTF-8 body.

The model response is decoded into a private structure:

```json
{
  "title": "...",
  "grouping_fields": {
    "category": "...",
    "alarm": "...",
    "error_code": "..."
  }
}
```

Only scalar string fields with bounded names/values are accepted. The server injects `projectID` and `sourceID`, sorts/canonicalizes the field map using JSON encoding, and hashes the canonical bytes. The model never supplies the final hash.

## Fallback

Valid JSON is decoded to generic JSON, recursively removes volatile object keys (identifier/request/token/timestamp/detail URL families), and is re-encoded canonically. The fallback title prefers stable semantic keys such as `title`, `alarm`, `message`, `error`, `summary`, and `name`; otherwise it uses `Webhook alert`. The canonical payload plus project/source scope is hashed. Plain text keeps the existing first non-empty line title and grouping value for compatibility.

The raw message remains the observation evidence and is never sent to incident title/fingerprint storage as a substitute for the canonical signature.

## Safety And Operations

- The model request is capped to a bounded JSON/text representation and uses `Temperature: 0`, `MaxTokens: 256`, and no tools.
- A denylist redacts secret/token/password/authorization/API-key-like fields before model input and fallback hashing.
- The analyzer does not log raw payloads; the existing outbound LLM telemetry owns redaction.
- The public response acknowledges acceptance only and cannot include an incident ID or creation flag before background processing completes.
- This in-process async boundary is best effort: process termination can interrupt accepted work. Durable delivery and retries require a future queue/outbox design.
- No new environment key or secret kind is introduced.
- If no project LLM provider exists, the adapter returns an error and the application uses fallback.

## Compatibility

The backend project and incident specifications must change their inbound contract from opaque first-line normalization to semantic normalization with deterministic fallback. Existing plain-text behavior and incident lifecycle behavior remain unchanged. The current LLM configuration remains the single project provider; model selection/cost policy stays with project configuration.

## Debug SQL

`formatQueryArg` gains explicit `*string`, `*time.Time`, and other nullable pointer handling so query-debug output is diagnostically accurate without changing bound values or query behavior.
