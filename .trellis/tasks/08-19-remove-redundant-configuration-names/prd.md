# Remove redundant configuration names

## Goal

Remove user-supplied names that do not identify or distinguish project configuration components. A project configuration is a single transactional snapshot containing one Git repository, one collection source, one trigger, and one optional LLM provider; users should configure those components through their domain fields rather than inventing aliases.

## Background

- `project_repositories`, `project_sources`, and `project_triggers` are each constrained by `UNIQUE(project_id)` in migration `000004_project_scope.up.sql`.
- The project domain models and HTTP contracts already have no `name` field for Git repositories or LLM providers.
- `Source.Name` and `Trigger.Name` are persisted and exposed through the HTTP API even though their records are selected and upserted by project scope, not by name.
- The configuration editor initializes and submits synthetic values such as `production-logs` and `incident-rule`.
- The configuration summary uses one generic `Name` column: repository rows show the SCM provider while source and trigger rows show their aliases. This presentation makes the four components appear to share a naming concept that their domain models do not actually share.

## Requirements

- Remove `name` from the collection source domain model, persistence model, SQL queries, HTTP request/response contracts, and frontend configuration types and payloads.
- Remove `name` from the trigger domain model, persistence model, SQL queries, HTTP request/response contracts, and frontend configuration types and payloads.
- Add a forward-only database migration that removes the obsolete source and trigger name columns and their associated constraints/indexes without rewriting prior migrations.
- Preserve project-scoped component identity through existing component IDs and project/environment relationships; do not replace names with another user-entered alias.
- Present each configuration component using meaningful domain fields, such as repository provider/remote, source kind/provider/resource, trigger kind, and LLM provider/model.
- Keep project names, environment names, credential names, Git branch names, and other names with independent display or selection semantics unchanged.
- Preserve existing configuration, webhook token, ingestion, incident source-scope, audit, and optimistic-version behavior except for removal of the redundant fields.
- Update tests, fixtures, generated SQL code, and documentation that assert or describe source/trigger names.

## Acceptance Criteria

- [x] Users can create and update a project configuration without entering a collection source name or trigger name.
- [x] Configuration API requests and responses contain no `source.name` or `trigger.name` fields.
- [x] The source and trigger domain and persistence models contain no `Name` field.
- [x] A forward migration drops `project_sources.name`, `project_triggers.name`, and all constraints/indexes that depend on those columns.
- [x] Existing rows retain all non-name configuration data after migration.
- [x] Source and trigger lookup/binding continues to use project, environment, component ID, kind, and other existing domain fields as appropriate.
- [x] The configuration summary/review UI no longer labels heterogeneous component values as a shared `Name` concept.
- [x] Backend unit/integration tests, SQL generation checks, frontend tests, lint, and type checks pass.

## Out of Scope

- Supporting multiple repositories, sources, triggers, or LLM providers per project.
- Changing project, environment, credential, or Git reference naming.
- Replacing internal UUID primary keys with composite natural keys.
- Redesigning source, trigger, or LLM provider configuration beyond removing redundant aliases and correcting their presentation.

## Open Questions

- None currently blocking requirement definition; technical impact and migration ordering remain to be documented in `design.md` and `implement.md`.
