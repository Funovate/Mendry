# Implementation Plan: Incident Service Foundation

## Child Delivery Order

- [ ] Complete `08-11-incident-backend-scaffold` and verify its engineering
      contracts before security or deployment packages depend on them.
- [ ] Plan and complete a security child for local sessions, roles,
      authorization, audit boundaries, and encrypted secret storage.
- [ ] Plan and complete the runtime-packaging child against the stable external
      PostgreSQL, Redis, RabbitMQ, telemetry, health, and secret contracts.
- [ ] Plan and complete protected console-shell integration.
- [ ] Run the parent integration review against every PRD acceptance criterion.

## Parent Validation

- Child acceptance criteria and quality gates pass independently.
- Security behavior does not leak secrets through API, logs, traces, metrics,
  errors, audit records, or configuration diagnostics.
- Deployment packaging constructs the same application commands and external
  contracts designed by the scaffold rather than adding alternate code paths.
- RabbitMQ outage degrades worker readiness and accumulates committed intent in
  the PostgreSQL outbox without making the API directly broker-dependent.
- The service remains usable when no LLM provider is configured or reachable.

## Rollback

Rollback follows each child plan. Application rollback must preserve PostgreSQL
data, and schema rollback is reviewed separately from binary rollback.
