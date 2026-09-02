# Normalize webhook alerts with AI fingerprints

## Goal

Accept webhook payloads from different cloud vendors without treating the first line of a JSON document as the incident identity. Use a bounded, project-scoped LLM call to extract stable grouping fields when available, then compute the final fingerprint in the backend. Preserve a deterministic local fallback so webhook ingestion still succeeds when the model is unavailable or returns invalid output.

## Confirmed Facts

- `POST /hooks/{token}` currently treats the body as opaque text and derives title/fingerprint from the first non-empty line.
- A formatted JSON body therefore produces `{` as the fingerprint and can merge unrelated alerts.
- `IngestInbound` must receive the final fingerprint before querying or creating an incident.
- The existing project OpenAI-compatible provider and encrypted `http_bearer` credential can be reused through an adapter boundary.
- Observations retain the original bounded message; the model must not replace the raw evidence.

## Requirements

- Resolve and validate the webhook token before accepting the request or making a model call.
- Return `202` as soon as the bounded body and webhook token are validated; run model normalization, Observation persistence, and incident ingestion asynchronously.
- Support valid JSON objects from arbitrary vendors without a vendor-specific schema.
- Send only a bounded, redacted payload to the model with no tools and a low output budget.
- Require a strict JSON model response containing a bounded title and grouping fields.
- Compute the final fingerprint in the backend from a canonical representation containing project/source scope and validated grouping fields.
- Exclude obvious per-event identifiers and volatile fields from model grouping input and backend fallback grouping.
- Fall back to deterministic local normalization for missing LLM configuration, timeout, provider failure, malformed output, or invalid grouping fields.
- Keep the raw inbound message in the Observation and do not log request/model payloads through new application logs.
- Keep existing incident semantics: a new inbound P2 can trigger remediation; an existing Open fingerprint only records an occurrence.
- Correct the PostgreSQL debug formatter for pointer values used by Observation queries.

## Acceptance Criteria

- [ ] A pretty-printed JSON alert no longer creates or updates an incident with fingerprint `{`.
- [ ] Two equivalent JSON payloads with reordered keys produce the same fallback fingerprint.
- [ ] Dynamic identifiers such as UIN, request ID, timestamp, and detail URL do not change the fallback grouping fingerprint.
- [ ] A valid model response produces a stable server-computed fingerprint and bounded title.
- [ ] A valid webhook returns `202` with an acceptance acknowledgement without waiting for the model or persistence path.
- [ ] A model error, timeout, empty provider, malformed JSON response, or invalid fields still produces a deterministic incident fingerprint in the background.
- [ ] Unknown tokens and invalid bodies are rejected synchronously, while background failures are logged without token or payload disclosure.
- [ ] Model input is bounded/redacted and the model request advertises no tools.
- [ ] Existing plain-text webhook behavior remains compatible with the current first-line title and grouping behavior.
- [ ] Tests cover JSON fallback, model success/failure, scope isolation, invalid model output, and existing incident ingestion behavior.
- [ ] `gofmt`, `go vet ./...`, `go test ./...`, `go test -race ./...`, `go build ./cmd/...`, and `make generate-check` pass from `backend/`.

## Out Of Scope

- Vendor-specific connector schemas or a UI for configuring field mappings.
- A new database table, durable queue, delivery retry, or delivery-status API for classifier calls.
- Letting the model diagnose root cause or trigger remediation directly.
- Changing the existing project LLM configuration shape or adding a second credential kind.
