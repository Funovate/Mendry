# Incident Service Foundation

## Goal

Establish the complete self-hosted service foundation needed by later incident
features without granting the service production write permissions.

## Parent References

`08-10-production-incident-mvp/prd.md`, `design.md`, and `implement.md`.

## Delivery Map

This task owns the combined foundation contract. Independently verifiable work
is delivered through child tasks rather than by starting this parent directly.

| Child task | Deliverable | Status |
|---|---|---|
| `08-11-incident-backend-scaffold` | Go API/worker baseline, PostgreSQL/Redis injection, RabbitMQ durable jobs, internal observability | Planning |
| Future security child | Local authentication, RBAC, encrypted secret storage, audit boundaries | Not yet planned |
| Future packaging child | Approved runtime packaging, external service topology, persistence and health operations | Not yet planned |
| Future console-shell child | Built React console and protected login shell integration | Not yet planned |

## Requirements

- Establish the Go API/worker module and React/TypeScript console with
  documented build, runtime, and deployment boundaries.
- Provide an approved deployment topology for API, worker, PostgreSQL, Redis,
  RabbitMQ, persistent storage, health checks, secret mounts, and administrator
  bootstrap. Deployment choices are intentionally outside the backend scaffold
  child.
- Implement local accounts with `admin`, `operator`, and `viewer` roles plus
  authenticated session/API access and auditable authorization checks.
- Load the deployment encryption key from a protected secret source and encrypt
  connector/notifier values before database storage. APIs expose only secret
  metadata; values are write, rotate, or delete only.
- Keep LLM access behind a typed provider port that accepts only redacted
  evidence summaries and remains optional to normal service operation.
- Do not connect to production systems or expose a production write action.

## Acceptance Criteria

- [ ] All child tasks pass their independent acceptance criteria and the
      integrated service becomes healthy from a clean approved deployment.
- [ ] Authentication and role checks reject unauthenticated and unauthorized
      requests; the three roles have documented and tested permissions.
- [ ] A connector secret round-trips through encrypted local storage while its
      plaintext never appears in API output, logs, or audit payloads.
- [ ] Encryption and bootstrap secrets come from the approved protected secret
      mechanism rather than committed or casually logged configuration.
- [ ] PostgreSQL data persists across an application restart and documented
      backup/rollback procedures are verified.
- [ ] RabbitMQ-backed jobs survive broker/worker restart with at-least-once
      handling, bounded retry/dead-letter behavior, and idempotent durable
      effects; a broker outage does not by itself make the API unavailable.
- [ ] A fake LLM provider exercises the typed boundary without network access;
      absence or outage does not stop API, worker, or console operation.

## Out Of Scope

- Incident lifecycle, production connectors, evidence collection,
  notifications, real LLM providers, and remediation behavior owned by sibling
  tasks under the Production Incident MVP.
