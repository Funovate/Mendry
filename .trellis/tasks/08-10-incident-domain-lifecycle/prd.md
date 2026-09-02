# Incident Domain And Lifecycle

## Goal

Persist every error observation and turn related failures into recoverable,
auditable incidents without using aggregation as an ingestion filter.

## Parent References

`08-10-production-incident-mvp/prd.md`, `design.md`, and `implement.md`.

## Dependencies

Requires `08-10-incident-service-foundation` for database, RabbitMQ durable
jobs, authentication, encrypted configuration, and application conventions.

## Requirements

- Add migrations for projects, environments, sources, observations, error
  groups, incidents, policies, audit events, retention settings, and the
  necessary supporting records.
- Define versioned observation and evidence contracts with a stable raw-source
  reference and optional service/request ID fields.
- Normalize errors and calculate deterministic fingerprints that remove known
  volatile values while preserving materially different messages and source
  locations.
- Persist all successfully ingested observations in a project event stream;
  a new fingerprint creates an `Info` incident and repeats append to it.
- Implement recovery after a configurable quiet window, operator-only closure,
  recurrence reopening, policy resolution, and retention cleanup. Scheduled
  recovery and retention use the shared versioned job runtime and remain
  idempotent under redelivery.

## Acceptance Criteria

- [ ] Unit tests prove that volatile IDs and timestamps group correctly while
      different locations or messages do not collapse incorrectly.
- [ ] Every accepted observation remains visible in its project event stream,
      including one matching an existing fingerprint.
- [ ] A new group creates exactly one `Info` incident; repetition updates it;
      quiet recovery, operator closure, and recurrence transitions preserve
      prior history.
- [ ] Recovery and retention jobs honor project overrides, tolerate duplicate
      RabbitMQ delivery, and retain referential integrity.
- [ ] Authorization and audit records protect all domain mutations.
