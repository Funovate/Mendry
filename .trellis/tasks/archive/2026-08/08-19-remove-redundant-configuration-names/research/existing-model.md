# Existing configuration-name model research

## Scope confirmed

The current implementation does not give all four project configuration components a `name` field:

- Git repository: no domain, database, or HTTP `name`; the summary renders `scmProvider` under a generic `Name` heading.
- LLM provider: no domain, database, or HTTP `name`; it is described and persisted as the project's unique OpenAI-compatible provider.
- Collection source: has a required `name` across database, domain, SQL, HTTP, frontend DTO, editor, review, fixtures, and tests.
- Trigger: has a required `name` across the same layers.

The true removal scope is therefore source and trigger aliases plus the misleading generic presentation.

## Cardinality and identity evidence

Migration `backend/internal/commands/migrate/migrations/000004_project_scope.up.sql` defines:

- `project_repositories_project_unique UNIQUE (project_id)`
- `project_sources_project_unique UNIQUE (project_id)`
- `project_triggers_project_unique UNIQUE (project_id)`

Migration `000009_project_llm_provider.up.sql` and the current upsert query also use project-level uniqueness for the LLM provider. The configuration endpoint saves one environment, repository, source, trigger, and LLM provider as one transaction. Component rows retain UUIDv7 IDs for references and token encryption context; names are not used to address configuration records.

## Cross-layer data flow

```text
Configuration editor
  -> PUT /api/v1/projects/{projectKey}/configuration
  -> HTTP request DTO
  -> domain.Configuration
  -> ValidateConfiguration
  -> Service.PutConfiguration
  -> project repository UpsertConfiguration
  -> project_sources / project_triggers
  -> generated SQL row
  -> PostgreSQL mapConfiguration
  -> domain.Configuration
  -> HTTP response DTO
  -> Zod schema
  -> summary / review UI
```

Current synthetic defaults are `production-logs` and `incident-rule`. They do not express additional domain identity.

## Incident dependency

`backend/internal/modules/incidents/adapter/postgres/queries.sql` currently resolves `source.environment_id, source.name`. The repository copies that value through a generated `SourceName` parameter into the immutable `incidents.source` display snapshot when creating an incident.

Removing `project_sources.name` must therefore change this lookup to return source kind and rename the adapter/query parameter to `Source`. Existing `incidents.source` values are historical snapshots and should not be rewritten. New incidents can use source kind (`ssh`, `cloud`, or `mcp`) because the project has one source and the kind is the requested user-visible discriminator.

## Generated-code boundary

SQL files are authoritative. Changes to project and incident SQL must be followed by `make generate`; generated `queries.sql.go` and models must not be edited manually. `make generate-check` verifies reproducibility.

## Migration boundary

Applied migrations `000001` through `000010` are immutable. The schema change requires contiguous forward migration `000011`, documenting lock risk, transaction behavior, compatibility, and rollback safety. It should drop source/trigger name constraints and columns. A coordinated backend deployment is required because the old binary selects and writes the removed columns.

## GitNexus impact analysis

- `ValidateConfiguration`: HIGH risk, 7 impacted symbols, 5 direct callers, 3 affected processes, 3 modules. Affected flows are configuration save, configuration load, and PostgreSQL upsert.
- PostgreSQL `mapConfiguration`: LOW risk, 2 direct callers (`GetConfiguration`, `UpsertConfiguration`).
- HTTP `mapConfiguration`: LOW risk, 2 direct callers (`getConfiguration`, `putConfiguration`).
- `Service.PutConfiguration`: LOW upstream risk.
- Frontend `ConfigurationWizard`: LOW risk, reaching `ConfigurationEditorPage` and the application route.

The HIGH-risk validator change requires synchronized domain, SQL, HTTP, and frontend contract updates with round-trip tests.

## Test and documentation surfaces

Expected updates include:

- domain configuration validation tests
- project application and HTTP handler fixtures/tests
- project PostgreSQL mapper/upsert tests
- incident PostgreSQL repository query and parameter tests
- migration source tests and PostgreSQL integration expected column counts
- seed/demo SQL and backend README/spec references
- frontend Zod schema, editor state/payload, source/trigger steps, review/summary
- frontend unit and Playwright route-mocked fixtures/assertions
