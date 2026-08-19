# Signed inbound webhook URL and ingress

## Boundaries

```text
Tencent Cloud / other alerter
        |
        v
POST /hooks/{token}          hooks HTTP  (no Session)
        |
        v
hooks.Service.Ingest
        |-- projects.LookupWebhookToken  (hash -> project + trigger + source)
        |-- observations.CreateInbound   (no principal)
        +-- incidents.IngestInbound      (no principal)
                |-- create Open P2  -> existing RemediationTrigger.Emit
                +-- bump Open occurrence
```

- `projects` owns token generate / hash lookup / encrypt-for-reveal / rotate / inbound URL.
- `hooks` owns the unauthenticated HTTP adapter and the ingest orchestration.
- `observations` and `incidents` grow inbound use cases that do not take `auth.User`.
- Remediation / log search stay behind the existing `RemediationTrigger` seam.
- Do not add a public plaintext-secret method on `projects.Service`. Token reveal is a dedicated inbound-URL field on configuration for project admins only.

## Public URL

New API-only key `FIXTHE_PUBLIC_URL`.

- Required by `config.LoadAPI`.
- Absolute `http`/`https` origin, optional path prefix, no query/fragment, no trailing slash.
- Invalid or missing: startup `fieldError` naming only the key.
- Displayed URL is `{publicURL}/hooks/{token}`.
- migrate / seed / bootstrap-admin do not read it.

Development default in `.env.example`: `http://127.0.0.1:8080`.

## Token

- 32 cryptographically random bytes, encoding `base64.RawURLEncoding` (43 characters, `[A-Za-z0-9_-]+`).
- Path: `POST /hooks/{token}`. Reject any other shape before lookup.
- Lookup key: `SHA-256(token)` stored on `project_triggers.ingress_token_hash`, unique.
- Reveal: same AES-256-GCM cipher as project secrets, AAD/context `projectID + triggerID + "webhook_token"`. Ciphertext and nonce live on the trigger row, not in `project_secrets`.
- Do not reuse `webhook_hmac` or `SigningSecretID` for this capability.
- `signed_webhook` no longer requires `SigningSecretID`. Existing HMAC secrets stay unused.
- Rotate replaces hash + ciphertext. Old path returns the same 404 as an unknown token.

## Persistence

Forward migration `000010` (do not edit `000001`–`000009`):

- `project_triggers.ingress_token_hash bytea`
- `project_triggers.ingress_token_ciphertext bytea`
- `project_triggers.ingress_token_nonce bytea`
- unique index on `ingress_token_hash` where not null
- check: all three token columns null together, or all present with hash length 32
- `signed_webhook` rows may keep `signing_secret_id` null

Token columns are not written by the existing configuration JSON. `PutConfiguration` generates a token when kind is `signed_webhook` and the row has none. Kind change away from `signed_webhook` clears the three columns.

## Authenticated contracts

`GET /api/v1/projects/{projectKey}/configuration`

- Project admin: when trigger is `signed_webhook` and a token exists, `trigger.inboundUrl` is the full public URL. Viewer/operator get `inboundUrl: null`.
- Never return the raw token as its own field.

`POST /api/v1/projects/{projectKey}/configuration/webhook-token`

- Project admin only.
- Creates a token if missing, otherwise rotates.
- Returns `{ inboundUrl }`.
- Audit `project.trigger.webhook_token` with metadata `{rotated: bool}` only.

Frontend Trigger step and configuration overview:

- Hide HMAC credential, event types, and deduplication key.
- Show read-only URL + copy when `inboundUrl` is present.
- Show Generate / Regenerate calling the dedicated POST.
- `custom_rule` shows none of this.

Webhook config written by the editor may keep compatibility defaults `eventTypes: ["alarm"]` and `deduplicationKey: "title"` so existing domain validation stays intact. Runtime ignores those fields.

## Unauthenticated ingest

`POST /hooks/{token}` is registered without `RequireAuthentication`.

Request: raw body, `Content-Type` `text/plain`, `application/json`, `application/x-www-form-urlencoded`, or omitted. Max body is the existing `FIXTHE_HTTP_MAX_BODY_BYTES`. Empty or whitespace-only body is `400 invalid_request`.

Normalize:

- `raw` = exact body bytes decoded as UTF-8 (invalid UTF-8 rejected).
- `title` / `fingerprint` = first non-empty line, trimmed, whitespace collapsed, then existing incident text limits (240 / 255).
- Observation `message` = raw text truncated to 65536.
- Observation `level` = `error`.
- Observation `attributes` = `{}`.
- Observation / incident `sourceId` and environment come from the project's configured source. Missing or disabled source is the same 404 as a bad token.

Response:

- New or bumped incident: `202` with `{ incidentId, created }` (`created` true only on insert).
- Do not echo the token or raw secret material.

Errors:

| Condition | Status / code |
|---|---|
| Malformed token shape, empty body, invalid UTF-8 | `400 invalid_request` |
| Unknown hash, disabled trigger, wrong kind, missing source, missing configuration | `404 webhook_not_found` |
| Unexpected store failure | `500 internal_error` |

Unknown vs disabled vs missing source must be indistinguishable.

## Incident / observation inbound use cases

Current `Create` paths require a Session and `RequireIncidentWrite`. Inbound cannot use them.

Add:

```text
observations.Service.CreateInbound(ctx, projectID, sourceID, message, fingerprint, occurredAt)
incidents.Service.IngestInbound(ctx, projectID, sourceID, title, fingerprint, occurredAt)
```

`IngestInbound`:

1. Load incident by `(project_id, fingerprint)`.
2. None → create `Open` / `P2` / counts 1, audit `actor_user_id` null, then `RemediationTrigger.Emit` (existing automatic P2 path).
3. Status `Open` → set `last_seen`, `occurrence_count + 1`. No priority change. No second Emit.
4. Status `Closed` or `Recovered` → leave the incident unchanged. Observation was already written. Do not reopen.

Fingerprint uniqueness stays `incidents_project_fingerprint_unique_idx`. Concurrent first inserts: one wins, the other retries as a bump.

Audit actor is null. Summaries stay generic (`Incident created.`, `Incident occurrence recorded.`). Metadata is allowlisted identifiers only.

Orchestration is sequential in the request: observation then incident. A failed incident after a successful observation leaves an Event Stream row; a retry creates another observation and then bumps. No outbox in this slice.

## Logging

`AccessLog` already records `request.Pattern`, not the raw URL. Register `POST /hooks/{token}` so completed/failed records show that pattern.

Do not log the path value, hash, or plaintext token. Audit and error envelopes must not include them. `FIXTHE_HTTP_REQUEST_DEBUG` may dump the alert body; that is the existing debug exception, not a new token leak.

## Compatibility

- Keep trigger kind name `signed_webhook`.
- Keep `webhook_hmac` as a secret kind; stop requiring it.
- Authenticated Observation POST stays as the development ingestion boundary.
- Manual incident create still defaults to `Info`.
- No HMAC verification, timestamp window, or replay table.

## Rollback

Revert `000010` only via a new down/forward pair if needed; do not rewrite applied migrations. Removing the hooks route restores “no public ingress”. Tokens left on trigger rows are inert without the route.
