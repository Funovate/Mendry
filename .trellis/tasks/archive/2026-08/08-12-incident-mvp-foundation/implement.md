# Implementation Plan: Project-Scoped Incident MVP Foundation

## Execution

- [x] Remove worker, jobruntime, RabbitMQ and remote OTLP exporter code and retain
      PostgreSQL, Redis sessions, the API, migrations, bootstrap-admin, health, and
      the shared HTTP safety boundary.
- [x] Implement local users/password authentication, Redis-backed sessions, secure
      cookies, login/logout/current-user routes, and system-role authorization.
- [x] Add immutable forward migration `000004` for projects, environments,
      memberships, encrypted secrets, repositories, sources, triggers, observations,
      audit events, and incident ownership/backfill; regenerate sqlc code and extend
      migration/integration assertions.
- [x] Implement project domain/application/PostgreSQL layers, system-admin project
      creation, membership-aware discovery, stable project resolution, role
      capabilities, and member administration with application-layer authorization.
- [x] Add AES-256-GCM secret storage with write-only HTTP responses and safe config;
      test nonce generation, associated-data binding, non-disclosure, and wrong-key
      failures without logging secret material.
- [x] Implement transactional project configuration persistence for environment,
      Git repository, SSH/Cloud/MCP source, and signed-webhook/custom-rule trigger;
      validate same-project secret references and expose read/write APIs.
- [x] Implement project-scoped Observation persistence and bounded Event Stream APIs;
      add cross-project isolation and role tests.
- [x] Refactor the existing incident repository/service/handlers and bootstrap wiring
      to require project context, replace global fingerprint uniqueness, remove global
      routes, and append safe audit events for writes.
- [x] Implement project audit-event listing and verify member/config/incident mutations
      produce allowlisted, secret-free records.
- [x] Update seed/local run documentation for encryption key, project creation,
      memberships, configuration, observations, and incidents.
- [x] Run the full backend quality gate and targeted real-service smoke test. Only
      after this backend boundary passes may frontend API integration resume.
- [x] Add the frontend project selector and server-derived capabilities; persist setup
      through project configuration APIs; move Event Stream and incident loading,
      selection, and lifecycle actions from fixtures to nested project APIs.
- [x] Update frontend tests and run frontend build/E2E checks.

## Validation

```bash
cd backend && make generate
cd backend && make check && make build && make generate-check
cd backend && go test -tags=integration ./tests/integration/...
```

After the project-scoped backend is complete:

```bash
cd frontend && npm run build
cd frontend && npm run test:e2e
```

The real-service smoke test must log in, create a project, add/read a member, create a
write-only secret, persist and reload configuration, append/read an observation,
create/update an incident through nested routes, inspect audit events, prove a
non-member receives not found, and log out.

## Risk And Review Gates

- Never edit migrations `000001` through `000003`; their checksums may already be in
  development databases.
- Before making incident ownership columns non-null, backfill every existing row into
  a deterministic legacy project/environment/source and grant existing system admins
  access.
- Every PostgreSQL query for a project-owned resource must include `project_id`; test
  that an ID from project A cannot be used through project B.
- Resolve authorization in the application layer before repository mutation. HTTP
  authentication middleware is defense in depth, not the business authorization
  implementation.
- Generated sqlc files are regenerated, never hand-edited. Schema comments remain the
  source of generated model documentation.
- Never include secret value, ciphertext, nonce, password hash, session token, raw
  connector headers, or full config JSON in errors, logs, audit metadata, or API
  responses.
- Keep frontend fixture behavior intact until backend project boundaries pass; do not
  bind the UI to transitional global endpoints.
- Re-run `trellis-before-dev` when switching to frontend work, and run `trellis-check`
  before claiming the backend or full task is complete.
