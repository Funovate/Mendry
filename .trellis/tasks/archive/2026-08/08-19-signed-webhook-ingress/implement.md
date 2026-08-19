# Signed inbound webhook URL and ingress

## Checklist

1. Config
   - Add `FIXTHE_PUBLIC_URL` to `config`, `LoadAPI`, `.env.example`, and `env_example_test`.
   - Reject missing/invalid values without echoing the raw string.
   - Wire the parsed public URL into the projects and hooks constructors.

2. Migration `000010`
   - Add the three trigger token columns, the all-or-nothing check, and the unique hash index.
   - Comments in Chinese, matching existing schema comments.
   - Do not edit `000001`–`000009`.

3. Projects domain / application
   - Stop requiring `SigningSecretID` for `signed_webhook`.
   - Generate token on `PutConfiguration` when kind is `signed_webhook` and hash is empty.
   - Clear token columns when kind becomes `custom_rule`.
   - `GetConfiguration` decrypts the token only for project admins and returns `inboundUrl`.
   - `RotateWebhookToken` creates or replaces the token and writes the rotate audit event.
   - `LookupWebhookToken(hash)` returns project id, source id, enabled trigger, or not found.
   - Hash with SHA-256; encrypt/decrypt with the existing AES-GCM cipher and a distinct context.

4. Observations / incidents inbound
   - `CreateInbound` without a principal; resolve environment from the same-project source.
   - `IngestInbound` implements create / bump-open / ignore-closed-or-recovered.
   - Null audit actor. P2 create still calls `RemediationTrigger.Emit`.
   - Add `GetByFingerprint` and occurrence update SQL via sqlc.

5. Hooks module
   - New `internal/modules/hooks` with HTTP adapter + application ingest.
   - Register `POST /hooks/{token}` with no Session middleware.
   - Normalize first non-empty line to title/fingerprint; persist raw body as the observation message.
   - Map all lookup/source/trigger failures to `404 webhook_not_found`.

6. Frontend
   - Trigger step: remove HMAC / event-types / dedup fields for webhook.
   - Read-only inbound URL + copy; Generate / Regenerate via the new POST.
   - Configuration overview shows the URL only when the API returned it.
   - `custom_rule` unchanged aside from not showing the URL.
   - Drop the signed-webhook HMAC save gate.

7. Tests
   - Domain: webhook without signing secret is valid; token columns all-or-nothing.
   - Application: generate on first save, rotate invalidates old hash, admin reveal vs operator omit, inbound create emits remediation, open bump does not, closed does not reopen.
   - HTTP: unauthenticated 202; bad token 404; empty body 400; pattern-only access logs.
   - Frontend unit/e2e: copy URL, no HMAC field, regenerate, viewer has no URL.
   - Integration compile at minimum; live Postgres only when the isolated test DB is configured.

8. Specs after implementation
   - `project-guidelines.md`: public ingress exists; token reveal; HMAC no longer required.
   - `incident-guidelines.md`: inbound ingest and occurrence bump.
   - `directory-structure.md`: `FIXTHE_PUBLIC_URL` and `POST /hooks/{token}`.
   - `logging-guidelines.md`: hooks route pattern, never raw token path.

## Validation

```bash
cd backend && go test ./internal/platform/config ./internal/modules/projects/... ./internal/modules/incidents/... ./internal/modules/observations/... ./internal/modules/hooks/...
cd frontend && npm test -- --run tests/configuration.test.ts tests/api.test.ts
```

Full gate before claiming done:

```bash
make -C backend check
cd frontend && npm run lint && npm test
```

## Rollback points

- After migration: new columns are nullable; old configuration continues to load.
- After route registration: disable by not registering hooks if a hotfix is needed.
- After frontend: viewers still work if `inboundUrl` is absent.

## Risky files

- `backend/internal/modules/incidents/application/service.go` — do not break the authenticated create/status + remediation emit contract.
- `backend/internal/platform/httpserver/server.go` — do not start logging raw URLs.
- `backend/internal/modules/projects/domain/project.go` — keep `custom_rule` validation unchanged.
