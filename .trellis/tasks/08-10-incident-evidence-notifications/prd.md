# Incident Evidence Reports And Notifications

## Goal

Produce provenance-backed incident evidence and reports, then notify external
systems according to explicitly configured, signed policies.

## Parent References

`08-10-production-incident-mvp/prd.md`, `design.md`, and `implement.md`.

## Dependencies

Requires `08-10-incident-domain-lifecycle` and
`08-10-incident-ingestion-cls`. It must use their contracts and must not add
an alternate ingestion, grouping, lifecycle, or background-job path.

## Requirements

- Deterministically collect evidence from incident correlators and configured
  runbooks, retaining connector, query, access scope, timestamp, hash, and
  expiration provenance. Evidence/report work uses the shared RabbitMQ job
  runtime and remains idempotent under at-least-once delivery.
- Generate a report that separates facts, hypotheses, missing evidence,
  verification steps, and remediation proposals.
- Permit optional remote LLM enrichment only after project opt-in, redaction,
  minimization, and audit. Validate any model-proposed read-only follow-up
  request before connector execution; the model has no direct tool or secret
  access. This child uses only the foundation fake provider; real Provider
  transports belong to `08-10-incident-llm-providers`.
- Implement project/fingerprint/source notification policies and HMAC-signed
  outbound webhooks through the shared RabbitMQ job runtime with bounded
  retries, idempotency, dead-letter isolation, and delivery audit records.

## Acceptance Criteria

- [ ] Deterministic evidence collection and factual reports work with remote
      LLM analysis disabled.
- [ ] Every evidence item includes complete provenance; unsupported or
      unapproved LLM follow-up proposals cannot invoke a connector.
- [ ] Redaction occurs before remote LLM submission, and audit records expose
      intended provider/model metadata and evidence IDs but not secrets or
      sensitive values; a fake provider validates the complete boundary.
- [ ] Muted, lifecycle, and per-occurrence notification policies behave per
      fingerprint/source/project rules without hiding observations.
- [ ] Outbound webhook deliveries are signed, retry safely, use idempotency
      keys, record delivery outcomes, and cannot duplicate a durable delivery
      effect after RabbitMQ redelivery.
