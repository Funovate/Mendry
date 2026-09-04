# Technical Design: Incident Service Foundation

## Scope

This parent coordinates independently verifiable foundation children. It is
not an implementation target. The first child establishes a deployment-neutral
Go backend scaffold; later children add security, packaging, and console-shell
integration without weakening that scaffold's boundaries.

## Layering

```text
React/TypeScript console
          |
          v
      Go API --------> PostgreSQL <-------------------- Go worker
          |              |                                ^  |
          |              v                                |  |
          |        transactional outbox -> RabbitMQ ------+  |
          |                                                 |
          +--------> Redis (disposable state only) <--------+
```

The backend child owns process construction, observability, external connection
contracts, process-specific health behavior, and RabbitMQ at-least-once job
delivery backed by a PostgreSQL transactional outbox. RabbitMQ transports work
but PostgreSQL remains the durable business/publication source. The security
child owns authentication, authorization, audit boundaries, and secret
encryption. Packaging remains a separate concern and may select a runtime only
after the application contracts are stable.

## Security And LLM Boundaries

Secret values are encrypted before persistence and are never readable after
creation through the API. Authentication uses secure server-managed sessions;
authorization is enforced at transport and application boundaries and tested
at the service layer.

A typed `LLMProvider` port accepts only redacted evidence summaries and
structured allowed-query descriptors. It cannot receive connector credentials
or data-store clients. Disabled or unavailable providers return a typed
non-fatal result so deterministic service behavior remains available.

## Integration And Rollback

Each child defines its own rollback points. Parent completion requires an
integration review across process lifecycle, persistence, disposable state,
durable messaging, authentication, authorization, encryption, observability,
and the console boundary. Database changes remain additive during the initial
foundation. RabbitMQ consumption can be paused while unpublished work remains
recoverable in the PostgreSQL outbox.
