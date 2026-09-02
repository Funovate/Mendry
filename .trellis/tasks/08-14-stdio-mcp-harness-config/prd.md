# Support stdio MCP configuration for harness

## Goal

Allow a project administrator to persist a local `stdio` MCP server definition
that the agentic remediation harness can consume through its trusted connector
and tool runtime. `stdio` is a required harness transport, not an optional
compatibility feature.

## Background

- The production project configuration currently models MCP only as a remote
  HTTP endpoint. `backend/internal/modules/projects/domain/project.go:279-286`
  has no command-based shape, and `project.go:315-320` requires a valid HTTP
  URL while allowing only `http`, `sse`, and `streamable_http`.
- The production configuration form exposes the same remote-only choices in
  `frontend/src/features/configuration/wizard/SourceStep.tsx:89-100`.
- The earlier high-fidelity prototype already models local `stdio` with
  `command`, arguments, optional working directory, and environment variables
  in `frontend/src/App.tsx:151-269`, including import of the common
  `mcpServers` JSON shape.
- The persisted project source configuration is an input to a later connector
  runtime; the projects API does not itself collect data. The database contract
  records this boundary in
  `backend/internal/commands/migrate/migrations/000004_project_scope.up.sql:371-386`.
- The harness exposes bounded logical tools such as `logs.search` and
  `logs.context`; the model must not receive connector credentials or arbitrary
  process authority. See
  `.trellis/tasks/08-14-agentic-remediation-harness/prd.md:90-104` and
  `design.md:139-159`.

## Requirements

- The versioned MCP source contract must distinguish remote URL transports from
  local `stdio` process transport without weakening validation of existing
  saved remote configurations.
- Project configuration must expose and persist the fields required to start
  one `stdio` MCP server in a future runtime.
- A `stdio` definition must persist the standard MCP client fields directly:
  required `command`, optional ordered `args`, optional `cwd`, and optional
  `env`. It must not replace those fields with an internal
  `commandDefinitionId`.
- Non-secret process environment values must be persisted in the standard
  string-to-string `env` object. Secret environment values must not appear in
  `env`; the project extension `secretEnv` maps each environment-variable name
  to an encrypted project credential ID. A future runtime will resolve and
  merge both maps only at process launch.
- The frontend must remain compatible with the common single-server
  `mcpServers` JSON shape and the earlier prototype's Studio fields. Imported
  input is normalized into the persisted versioned source contract.
- Every `env` entry imported from standard `mcpServers` JSON must enter an
  explicit review state and default to Secret. An administrator may deliberately
  reclassify an entry as Non-secret. A Secret draft must be stored through the
  encrypted credential API and converted to a `secretEnv` reference before the
  project configuration can be saved; variable names must not be used to guess
  sensitivity.
- This task stops at the typed configuration boundary: it must make the saved
  definition available to a future trusted harness runtime, but must not start
  a process, perform an MCP handshake, discover tools, or issue `tools/call`.
- Secret values must remain in encrypted project credential storage and must
  not be returned through configuration APIs, logs, or audit payloads.
- Every `secretEnv` credential reference must resolve inside the same project;
  environment names must be unique across `env` and `secretEnv` so one value
  cannot ambiguously override the other.
- Invalid or incomplete `stdio` definitions must be rejected through the
  existing project-configuration invalid-input contract.
- Existing HTTP, Streamable HTTP, and SSE MCP configurations must remain
  readable and writable during migration.
- The persisted `stdio` shape must not add an MCP tool-name allowlist. Existing
  source capabilities, evidence profile, and query scope remain the policy
  inputs available at this configuration layer; tool discovery and runtime
  authorization belong to the later runtime task.

## Acceptance Criteria

- [ ] An administrator can select `stdio`, enter its required process fields,
      save the transactional project configuration, reload the page, and see
      the same non-secret values.
- [ ] An administrator can import a common `mcpServers` JSON document containing
      exactly one `stdio` server and review the normalized standard fields before
      saving.
- [ ] Imported environment entries default to Secret, require an explicit
      per-entry review, and block configuration save until every Secret entry
      has an encrypted same-project credential reference.
- [ ] A saved `stdio` definition round-trips non-secret `env` values and
      `secretEnv` credential references while configuration responses, audit
      records, logs, and post-save frontend state never contain the referenced
      secret plaintext.
- [ ] Validation rejects unknown, cross-project, wrong-kind, or malformed
      `secretEnv` references and overlapping variable names between `env` and
      `secretEnv`.
- [ ] Backend contract tests accept a valid `stdio` definition and reject
      missing, conflicting, unknown, oversized, or unsafe fields.
- [ ] Remote MCP contract tests continue to pass without changing existing
      persisted values.
- [ ] Frontend unit and browser tests cover transport-dependent fields and prove
      that remote endpoint fields and `stdio` process fields cannot be submitted
      together.
- [ ] The resulting typed configuration exposes the process definition and
      credential IDs needed by a future trusted harness runtime without
      containing secret plaintext or adding runtime behavior to this task.

## Out Of Scope

- Implementing the complete durable agent loop, model protocol, incident state
  machine, patch workflow, or publication workflow owned by the parent harness
  task.
- Implementing a `stdio` process launcher, MCP client/session lifecycle,
  `initialize`, `tools/list`, `tools/call`, connection testing, restart policy,
  or runtime isolation. Those require a separately owned runtime task.
- Persisting or editing an `allowedTools` list for MCP server tool names.
- Treating a developer's Codex MCP configuration as deployed project
  configuration.
