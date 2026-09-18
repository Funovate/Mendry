# Project Notifications

The API process delivers project-scoped notifications to Telegram bots, Feishu custom bots, and WeCom group bots. Each project can configure multiple channels, including multiple Telegram bot/chat pairs.

## Deployment

Apply migration `000025_notifications` before deploying the new API binary. Notifications reuse `MENDRY_ENCRYPTION_KEY` for AES-GCM credentials and `MENDRY_PUBLIC_URL` for console links. No additional broker or environment variables are required. Retain the encryption key across restarts; changing it without re-encrypting stored channels and delivery snapshots makes them unreadable.

The notification worker runs alongside remediation recovery and drains before PostgreSQL closes. It uses an atomic PostgreSQL claim, a 60-second fenced lease, a 20-second send budget, and exponential retries (2 seconds through 1024 seconds). After 12 attempts, delivery becomes `failed` and can be retried manually only while its channel still exists and is enabled. An older `pending` or `sending` delivery blocks later messages for the same channel, incident, and lifecycle, so an AI result cannot overtake its active trigger. Permanently `failed` and `cancelled` deliveries do not block later results. Other channels and incidents can continue independently.

## Event Semantics

- New incidents and `Recovered -> Open` emit `trigger` once per incident ID and lifecycle generation.
- The first stopped AI outcome emits `result` once per incident ID and generation. A subsequent successful retry does not send another result.
- Results cover diagnosis awaiting review, completed non-code diagnosis, manual-review blocks, failure, exhausted budget, and hotfix awaiting human review.
- Automatic hotfix's intermediate diagnosis-awaiting-review state is skipped unless the run is constrained to analysis only.
- Manual recovery and closure do not emit notifications. AI completion does not imply incident recovery.
- No enabled channels means no notification event or delivery is stored. Previously stopped attempts are checked to avoid backfilling a later retry when a channel is subsequently added.
- Business state, notification event, and encrypted channel snapshots commit in the same transaction. Each delivery retries independently of the business workflow.
- Deliveries retain immutable encrypted destination snapshots for historical audit, but the worker claims only existing enabled channels and uses their current platform and encrypted credentials. Rotation therefore applies to tasks not yet claimed, without rewriting history.
- Disabling or deleting a channel atomically marks its `pending` and `sending` deliveries `cancelled` and clears their leases. Enqueue, claim, and manual retry coordinate with channel changes through PostgreSQL row locks; cancellation reads newly committed jobs after acquiring the channel lock. Old worker acknowledgements cannot overwrite cancellation. Re-enabling a channel does not backfill cancelled deliveries, and cancelled deliveries cannot be retried manually.
- A request already being sent cannot be recalled and may still complete after disablement or deletion. Credentials already read by a claimed worker cannot be recalled on rotation either; an in-flight attempt may use the previous credentials. Test messages are explicit immediate sends, including for disabled channels, and are not business delivery records.

Delivery is **at least once**. A platform may accept a message before its HTTP response is lost; retrying can then produce a duplicate. Database event deduplication does not remove that external ambiguity.

## Security

Credentials are encrypted with AES-GCM, bound to project ID, channel ID, and a notification-specific credential context. Read DTOs never expose bot tokens, chat IDs, webhook URLs, signing secrets, ciphertext, or nonces. Platform errors are stable classifications rather than credential-bearing network URLs or response bodies. Incoming credential objects are covered by the existing request snapshot redaction.

Only HTTPS to `api.telegram.org`, `open.feishu.cn`, and `qyapi.weixin.qq.com` is supported. Webhook paths are restricted to the platform's bot endpoint. Custom hosts, explicit ports, userinfo, fragments, encoded path variants, duplicate WeCom keys, proxies, and redirects are rejected or disabled. Feishu signing secrets are optional.

## API And UI

Configuration -> Notifications opens `/projects/:projectKey/configuration/notifications`.

All endpoints are under `/api/v1/projects/{projectKey}/notifications`, require the existing authenticated session, and resolve the project through the existing project service:

| Method | Path | Result |
| --- | --- | --- |
| GET | `/channels` | Safe channel list |
| POST | `/channels` | Create channel |
| PUT | `/channels/{channelId}` | Update channel; omit `credentials` to retain them |
| DELETE | `/channels/{channelId}` | Remove channel |
| POST | `/channels/{channelId}/test` | `{ "sent": true }` |
| GET | `/deliveries` | Most recent 100 deliveries |
| POST | `/deliveries/{deliveryId}/retry` | Requeue a failed delivery only for a current enabled channel; `{ "queued": true }`, otherwise HTTP 409 |

Channel input uses `name`, `platform` (`telegram`, `feishu`, `wecom`), `enabled`, and optional `credentials`. Creation requires credentials. Telegram credentials use `botToken` and `chatId`; Feishu/WeCom credentials use `webhookUrl`, with optional `signingSecret` for Feishu. Platform is immutable on update. Responses use the project's standard success/list envelopes. Delivery `state` is one of `pending`, `sending`, `delivered`, `failed`, or `cancelled`.

## Validation

```sh
cd backend
go test ./internal/modules/notifications/... ./internal/bootstrap ./internal/commands/migrate
MENDRY_TEST_POSTGRES_URL=postgres://user:password@localhost:5432/mendry_notifications_test?sslmode=disable \
MENDRY_TEST_POSTGRES_ISOLATION=mendry_notifications_test \
go test -tags integration ./tests/notifications -v
```

The integration test requires a separately provisioned database named exactly `mendry_notifications_test`, validates the matching isolation marker, and rebuilds only that database's public schema. Never point it at an application or shared test database.
