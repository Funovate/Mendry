# Incident Ingestion And CLS Connector

## Goal

Ingest signed external observations and collect read-only Tencent CLS data
through the shared connector contracts.

## Parent References

`08-10-production-incident-mvp/prd.md`, `design.md`, and `implement.md`.

## Dependencies

Requires `08-10-incident-service-foundation` and
`08-10-incident-domain-lifecycle`. It provides the first production connector
implementation that the extensible connector task must reuse.

## Requirements

- Accept signed inbound webhooks with timestamp limits and replay protection;
  preserve the source envelope and map configured JSON fields to observations.
- Implement a read-only Tencent CLS MCP connector for search, nearby log
  context, alarm history, and metrics. The deployed connector configuration is
  independent from developer tooling configuration.
- Support checkpointed polling and fixture-based historical replay without
  customer credentials. Polling and replay use the shared RabbitMQ job runtime;
  checkpoints make redelivery idempotent and prevent skipped source ranges.
- Enforce declared connector capabilities and ensure connector secrets are
  never returned or logged.

## Acceptance Criteria

- [ ] Webhook and CLS fixture input normalize to the same versioned observation
      contract with a raw-source reference.
- [ ] Invalid signatures, stale timestamps, and replayed webhook deliveries are
      rejected and audited without creating an observation.
- [ ] A fake MCP server proves search, context, alarm, and metric requests are
      read-only, capability-scoped, and checkpointed.
- [ ] Historical replay creates expected observations and incidents without
      enabling outbound notifications, including after worker interruption and
      RabbitMQ redelivery.
- [ ] Connector credentials are absent from HTTP responses, logs, audits, and
      replay fixtures.
