# Implementation Plan

## Ordered Checklist

1. [x] Add migration `000011_remove_configuration_names.up.sql` with required operational header, explicit constraint drops, and source/trigger column drops. Update migration ordering/content tests and integration expected column counts.
2. [x] Remove Source/Trigger `Name` fields and name validation from the project domain. Update domain fixtures and validation tests.
3. [x] Update project SQL source files to remove source/trigger names from configuration select/upsert paths. Regenerate sqlc output and adapt handwritten PostgreSQL row mapping/parameters.
4. [x] Update incident source-scope SQL to return `source.kind AS source`; rename handwritten and generated-use parameters from `SourceName` to `Source`. Regenerate sqlc output and update incident repository tests.
5. [x] Remove source/trigger names from HTTP request/response DTOs and mapping. Update handler fixtures and exact JSON contract assertions.
6. [x] Remove source/trigger names from frontend Zod DTOs, configuration editor state/payload, SourceStep/TriggerStep props and inputs, review UI, and summary table. Render component-specific kind/provider/model values.
7. [x] Update seed/demo data, backend integration fixtures, README/spec contracts, and frontend route-mocked fixtures/assertions. Preserve environment/project/credential/Git reference names.
8. [x] Run formatting, generation, backend/frontend checks, focused PostgreSQL integration coverage when the dedicated test database is configured, and GitNexus change detection.

## Validation Commands

From `backend/`:

```bash
make generate
make generate-check
gofmt -w .
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/...
go test -tags=integration ./tests/integration
```

The real integration command requires the fail-closed `FIXTHE_TEST_POSTGRES_URL` and matching `FIXTHE_TEST_POSTGRES_ISOLATION`. If unavailable, compile integration tests and report that the database-backed run was not executed.

From `frontend/`:

```bash
npm run lint
npm run typecheck
npm run test
npm run build
npm run test:e2e
```

From repository root:

```bash
git diff --check
node .gitnexus/run.cjs detect-changes --repo fixthe
```

## Review Gates

- Confirm no `source.name`, `trigger.name`, `Source.Name`, `Trigger.Name`, `source_name`, or `trigger_name` remains outside immutable migrations or historical documentation that explicitly describes the old contract.
- Confirm `project_sources_project_unique` and `project_triggers_project_unique` remain intact.
- Confirm existing Incident records are not rewritten by the migration.
- Confirm new incident creation stores source kind in `incidents.source`.
- Confirm signed-webhook token generation, admin reveal, rotation, and unknown-token behavior remain unchanged.
- Confirm generated sqlc files match SQL source and were not manually patched.
- Confirm the configuration PUT request captured by Playwright omits both removed fields.

## Risky Files and Rollback Points

- `backend/internal/modules/projects/domain/project.go`: HIGH GitNexus impact through validation/load/save flows. Complete domain and persistence changes together before testing.
- `backend/internal/modules/projects/adapter/postgres/queries.sql`: generated contract fan-out. Regenerate immediately after editing.
- `backend/internal/modules/incidents/adapter/postgres/queries.sql`: changes newly created incident display snapshot semantics; protect with repository/integration assertions.
- `backend/internal/commands/migrate/migrations/000011_*.sql`: irreversible alias removal; inspect schema on a disposable database before production use.
- `frontend/src/api.ts`: strict runtime contract; all frontend fixtures must be migrated in the same change.

Do not edit old migrations or generated sqlc files manually. If implementation validation fails before migration deployment, revert only this task's new code and migration changes. After migration deployment, use a new forward corrective migration rather than editing migration history.

## User-Requested Save Contract Amendment

The wizard's visible Git repository, collection source, trigger, and LLM provider sections save through separate component mutations. The LLM model probe is local to the LLM save control; it must not disable or gate Trigger/source/repository saves. The editor reads partial state from `/configuration/draft` and the component payloads contain no sibling configuration fields.
