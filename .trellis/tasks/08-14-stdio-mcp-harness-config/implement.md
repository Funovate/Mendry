# Implementation Plan: stdio MCP Harness Configuration

## Dependency And Scope Gate

- [ ] Reconfirm that this task ends at configuration/API persistence and does
      not add a launcher, MCP client, connection test, tool discovery, or tool
      allowlist.
- [ ] Preserve unrelated worktree changes and treat migration `000004` as
      immutable; all schema changes use contiguous migration `000005`.

## Backend And Persistence

- [ ] Add migration `000005` for the `mcp_env` secret kind and the documented
      `project_source_secret_env` same-project reference projection. Update
      embedded migration and integration expectations without editing generated
      files manually.
- [ ] Extend the project domain with `mcp_env`, a transport-discriminated strict
      MCP config decoder, stdio field bounds, environment-name validation,
      remote/stdio field exclusion, and deterministic `secretEnv` extraction.
- [ ] Extend application validation so every stdio `secretEnv` ID resolves to a
      same-project `mcp_env` credential without decrypting it. Retain the current
      public invalid-input error contract.
- [ ] Update project SQL and repository mapping so configuration upsert replaces
      the sorted secret-env projection atomically and configuration reads fail
      closed if JSON and relational references drift. Regenerate sqlc output
      through `make generate`.
- [ ] Update HTTP/API mapping and tests for the new secret kind and stdio config
      round trip; prove responses and audit metadata contain no secret value.
- [ ] Add domain, application, repository, migration, and integration coverage
      for valid stdio, strict rejection, wrong-kind/unknown/cross-project
      references, projection replacement, and unchanged remote configurations.

## Frontend Contract And Experience

- [ ] Extend the API credential schema with `mcp_env` and add shared typed MCP
      config/import projections rather than casting raw config inside multiple
      components.
- [ ] Extend configuration state and payload building for `command`, ordered
      `args`, optional `cwd`, `env`, and `secretEnv`, clearing incompatible
      remote/stdio fields on transport changes.
- [ ] Add a focused production MCP editor with Studio and JSON-import modes,
      exactly-one-server parsing, manual stdio fields, environment row review,
      default-Secret imports, and explicit Non-secret classification. Reuse or
      extend `CredentialField` for `mcp_env` writes and references.
- [ ] Block project configuration Save for unresolved imports, clear transient
      plaintext after credential writes/reset/navigation, and keep Review and
      reload surfaces metadata-only. Do not add connection testing or tool-name
      permissions.
- [ ] Add Vitest cases for parsers/builders/gates and Playwright coverage for
      manual entry, import, secret creation, saved request shape, reload,
      redaction, and remote compatibility at desktop and mobile widths.

## Documentation And Quality Gates

- [ ] Update backend README and backend/frontend project specs with the final
      stdio/secretEnv contract, ownership boundary, and secret handling rules.
- [ ] Run backend formatting, generation, unit/race/vet/build checks, frontend
      lint/typecheck/unit/build/E2E checks, focused PostgreSQL integration tests
      when the isolated test database is available, and `git diff --check`.
- [ ] Review the final cross-layer data flow from JSON import through encrypted
      credential IDs, SQL persistence, API reload, and future-runtime handoff;
      verify no plaintext or prototype dependency crossed the boundary.

## Validation Commands

```bash
cd backend && make generate
cd backend && make generate-check
cd backend && gofmt -w <changed-go-files>
cd backend && go test ./...
cd backend && go test -race ./...
cd backend && go vet ./...
cd backend && go build ./cmd/...
cd frontend && npm run lint
cd frontend && npm run typecheck
cd frontend && npm run test
cd frontend && npm run build
cd frontend && npm run test:e2e
git diff --check
```

Run the repository's isolated PostgreSQL integration command only with an
explicit test database and matching isolation marker.

## Rollback Points

- Before migration: revert application/frontend changes normally.
- After migration but before `mcp_env` data: roll back the binary while leaving
  the additive table/check expansion in place.
- After `mcp_env` data exists: disable stdio editing and forward-fix; do not run
  destructive credential or mapping deletion automatically.
