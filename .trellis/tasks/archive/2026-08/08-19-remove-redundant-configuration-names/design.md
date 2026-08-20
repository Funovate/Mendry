# Technical Design

## Summary

Treat repository, source, trigger, and LLM provider as project-scoped configuration components rather than user-named resources. Git repository and LLM provider already follow that model. Remove the remaining `name` contract from Source and Trigger across storage, backend, and frontend.

## Boundaries

### Database

Add `000011_remove_configuration_names.up.sql` as a forward-only migration:

1. Drop `project_sources_project_name_unique` and `project_sources_name_bounded`.
2. Drop `project_triggers_project_name_unique` and `project_triggers_name_bounded`.
3. Drop `project_sources.name` and `project_triggers.name`.

The existing `UNIQUE(project_id)` constraints remain the cardinality invariant. UUIDv7 component IDs and project/environment foreign keys remain unchanged.

The migration is a contract migration, not an online expand/contract migration. It requires a coordinated deployment because binaries built against the old SQL cannot run after the columns are removed. Existing rows lose only synthetic aliases. Existing `incidents.source` snapshots are retained.

### Backend domain and validation

Remove `Name` from `domain.Source` and `domain.Trigger`. Update `ValidateConfiguration` so source and trigger validity is based on kind, typed config, capabilities, enabled state, and scoped secret references. Do not add replacement aliases or derive hidden names.

### Persistence and generated SQL

Update project configuration select/upsert SQL to stop selecting, inserting, updating, returning, and mapping source/trigger names. Regenerate project sqlc code.

Update incident source-scope SQL from `source.name` to `source.kind AS source`. Rename generated/adapter concepts from `SourceName` to `Source` so the removed naming assumption does not survive in code. Existing Incident domain/API fields remain `source`; only the value assigned for newly created incidents changes from an arbitrary alias to the configured source kind. Regenerate incident sqlc code.

Webhook token lookup continues to bind trigger and source through project/environment IDs and is otherwise unchanged.

### HTTP contract

Remove `name` from source and trigger request/response DTOs and mapping. The strict JSON decoder will reject stale clients that continue sending those fields; frontend and backend must be deployed together. No compatibility-only ignored fields will be retained.

### Frontend contract and UI

Remove `name` from source/trigger Zod schemas and inferred DTOs. Remove editor state, payload fields, Source/Trigger name inputs, and review rows.

Replace the generic configuration summary `Name` heading with a component-specific neutral heading such as `Type / provider`, and render meaningful values:

- Environment: environment name
- Git repository: SCM provider
- Collection source: source kind, with provider/resource details already available where useful
- Trigger: trigger kind
- LLM provider: provider/model when present

The review step should list kind and domain configuration, not an alias.

## Data Flow After Change

```text
UI kind/config selection
  -> source/trigger DTO without name
  -> domain validation by kind/config
  -> project-scoped upsert by project_id
  -> readback without name
  -> UI renders kind/provider/model

Incident create
  -> resolve enabled source by project_id + source_id
  -> return environment_id + source.kind
  -> copy kind into incidents.source historical snapshot
```

## Compatibility and Rollback

- Migration rollback is not automated. Restoring removed aliases cannot be done faithfully because they have no domain source of truth.
- Binary rollback after migration requires rolling forward with a corrective migration or restoring the database from backup; this must be stated in the migration header.
- Historical incidents keep their existing `source` text, so lists may contain old aliases and new type values. This is acceptable historical snapshot behavior and avoids rewriting incident history.
- API removal is intentionally breaking for stale configuration clients and fixtures.

## Risk Controls

- Apply all cross-layer contract changes in one implementation task.
- Run sqlc generation before compilation so stale field references fail visibly.
- Add migration assertions that the new version exists and contains the intended drops.
- Exercise configuration round-trip and incident creation in PostgreSQL integration coverage.
- Keep webhook token generation, reveal, rotation, and lookup tests in the gate because Trigger shape changes touch those flows.
- Run GitNexus `detect_changes` before commit and verify only configuration, incident-source snapshot, migration, and frontend configuration flows are affected.

## Follow-up Amendment: Independent Component Saves

The configuration editor now treats environment, repository, source, trigger, and LLM provider rows as independently persisted project components. `GET /configuration/draft` returns nullable component state for the editor, and `PUT /configuration/{component}` validates and upserts only the addressed component. The complete `GET /configuration` read model remains available for runtime consumers once the required environment, repository, source, and trigger rows exist.

Source and trigger writes establish the project-default environment when no environment row exists, because their database foreign keys require an environment. This prerequisite does not inspect or require the LLM provider. The LLM model-list and chat probes gate only the LLM component's own save control.
