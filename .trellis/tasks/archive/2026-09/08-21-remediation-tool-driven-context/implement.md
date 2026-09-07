# Implementation Plan: Tool-Driven Remediation Context

## Ordering And Dependencies

The implementation is staged so the agent loop is correct before any new
connector runtime is enabled. MCP and log API work depend on the shared catalog,
observation, and policy contracts. Stdio runtime additionally depends on the
`08-14-stdio-mcp-harness-config` task landing its validated configuration and
secret projection.

## Step 1: Freeze Current Behavior With Regression Tests

- [ ] Add failing coordinator tests proving current eager repository/evidence
      adapter failures terminate before the first model turn.
- [ ] Add failing scripted-loop tests proving tool success/error content is not
      visible to the following turn and planning receives empty context.
- [ ] Add an OpenAI adapter test proving `ModelTurn.Tools` is currently omitted
      from the outbound request.
- [ ] Record existing state, budget, observer, and credential-isolation behavior
      that must remain compatible.

## Step 2: Add Provider-Neutral Conversation And Tool-Call Values

- [ ] Add additive ordered model-message, assistant tool-call, and tool-result
      values while preserving `SystemPrompt`, `UserMessage`, and strict JSON
      envelope compatibility during migration.
- [ ] Add a bounded `AgentConversation` that owns bootstrap context, ordered
      observations, catalog/context versions, and deterministic compaction.
- [ ] Define model-visible tool observation and safe typed tool error contracts.
- [ ] Test ordering, serialization, truncation, compaction, prompt-injection
      labeling, and absence of credentials/raw diagnostics.

## Step 3: Make Context Preparation Metadata-Only

- [ ] Refactor `ContextAssembler` to build bootstrap metadata and capability
      status without calling Git, SSH, or log API data ports.
- [ ] Add a credential-free source capability loader for kind, enabled state,
      declared capabilities, source version, and policy version.
- [ ] Transition valid runs into `diagnosing` even when a connector is
      unavailable; keep invalid identity/state/persistence failures terminal.
- [ ] Update context observations and budgets so no repository/evidence bytes
      are credited before a tool actually reads data.

## Step 4: Close The Built-In Agent Tool Loop

- [ ] Refactor `ToolGateway` into a per-run, phase-specific catalog of registered
      routes while retaining all current pre-adapter policy checks.
- [ ] Add complete JSON Schemas for built-in repository tools and `ssh.inspect`
      instead of the current empty object schema; SSH sources must not advertise
      `evidence.search` / `evidence.context`.
- [ ] Return bounded model-visible payloads and safe errors separately from
      persistence summaries and DEBUG observations.
- [ ] Append every tool request/result to the conversation and feed it to the
      next model turn; pass the compacted conversation into planning.
- [ ] Execute multiple provider-native read calls sequentially and stop cleanly
      on policy or budget boundaries.
- [ ] Preserve the JSON `requestTool` envelope as a fallback routed through the
      same catalog.

## Step 5: Make The OpenAI Adapter Honor Tools

- [ ] Serialize ordered messages and allowed tool definitions into the actual
      provider request.
- [ ] Parse provider-native tool calls into provider-neutral domain values and
      preserve tool-call IDs for result messages.
- [ ] Continue strict schema validation for final diagnosis/plan/stop envelopes.
- [ ] Bound tool names/descriptions/schemas and provider response sizes; reject
      duplicate IDs, invalid arguments, and mixed invalid responses safely.
- [ ] Extend outbound observability without logging tool-result bodies,
      credentials, or unrestricted schemas.

## Step 6: Add Versioned MCP Tool Policy

- [ ] Add additive migration/query/application contracts for project/source
      MCP tool policies with optimistic versioning and read-only phase grants.
- [ ] Default missing policy to no exposed MCP tools.
- [ ] Add admin-only policy management and discovery-preview API contracts;
      operators/viewers cannot widen authority.
- [ ] Snapshot source/policy versions and catalog hash on each run.
- [ ] Test same-project/source ownership, stale-version conflicts, unknown tool
      rejection, and annotations-not-authority behavior.

## Step 7: Add Dynamic MCP Runtime

- [ ] Select and pin a maintained Go MCP client library after verifying current
      official SDK transport and lifecycle support; do not hand-roll protocol
      framing when the maintained library covers it.
- [ ] Implement trusted remote MCP configuration loading, encrypted credential
      injection, initialize, paginated discovery, call, cancellation, and close.
- [ ] Namespace provider-safe tool IDs and retain an internal collision-safe map
      to source/original names.
- [ ] Validate discovered schema size/depth, intersect with policy, and publish a
      deterministic catalog version/hash.
- [ ] Add one bounded non-blocking discovery probe during preparation and a
      built-in `source.refresh_tools` action for agent-requested retry.
- [ ] Validate arguments against the discovered JSON Schema before `tools/call`.
- [ ] Accept bounded text/structured JSON results; mark unsupported content
      blocks without forwarding binary/unrestricted resources.
- [ ] Implement per-run session cleanup and abandoned-session TTL cleanup.
- [ ] Add fixture-server tests for discovery pagination, direct schema exposure,
      allowlist filtering, name collisions, call success/error, timeout,
      reconnect/refresh, truncation, redaction, and cleanup.

## Step 8: Add Stdio MCP Runtime After Its Configuration Dependency

- [ ] Consume the validated stdio command/args/cwd/env/secretEnv contract from
      `08-14-stdio-mcp-harness-config`.
- [ ] Launch argv directly without a shell, inject decrypted same-project
      `mcp_env` values only at launch, clear them after close, and never expose
      command/cwd/env to the model or logs.
- [ ] Enforce process, time, output, environment, cwd, and child-process bounds;
      document and test the deployment isolation assumption.
- [ ] Run the same discovery, policy, namespace, call, and observation contract
      suite used by remote MCP.

## Step 9: Replace SSH Wrappers With Inspect And Add Explicit Log API Support

- [x] Add a credential-free SSH inspect port. Do not extend `EvidenceLogPort`.
- [x] Parse `ssh.inspect` command strings in the gateway: allowlisted binaries,
      at most three `|` segments, reject `; && || $() \` redirections, env
      assignments, `sudo`, `find -exec/-delete/-ok`, and `journalctl` follow/
      vacuum flags. Execute only the reconstructed quoted argv.
- [x] Default cwd to `projectFolder`, timeout 15s, combined output 64KiB with
      truncation marker. Return `{command, exitCode, stdout, stderr, truncated}`
      without secret-pattern redaction or evidence-line IDs.
- [x] Put host/user/`projectFolder`/`logPath` in bootstrap metadata and tell the
      model to `ls` before reading; never auto-tail `logPath`.
- [x] Replace SSH's undifferentiated remote failure with internal typed safe
      classification. Inspect stderr is model-visible and bounded; operator logs
      still omit private-key bytes and temp key paths.
- [ ] Define a versioned generic HTTP JSON log connector configuration with
      fixed endpoint/template, same-project credential reference, query mapping,
      pagination, response mapping, and strict bounds.
- [ ] Implement the generic log API adapter behind `EvidenceLogPort` and expose
      only bounded logical evidence tools.
- [ ] Share timeout, byte-limit, safe-error, and credential-isolation tests
      across SSH inspect and HTTP JSON evidence adapters; keep secret-pattern
      redaction tests on Git/MCP/log API, not on inspect stdout.

## Step 10: Persistence, Observability, And Console Integration

- [ ] Persist catalog/context versions, public/original tool identity, policy
      hash, result hash, safe error code/retryability, timing, byte counts,
      truncation, and evidence/observation refs without raw payload duplication.
- [ ] Attach context/catalog versions and citations to diagnosis and plan rows.
- [ ] Show unavailable sources, discovery failures, rejected tools, retryability,
      and evidence-insufficient outcomes in the existing remediation review
      chain without exposing sensitive details.
- [ ] Keep large redacted model/tool payloads DEBUG-only and bounded under the
      existing observability contract.

## Step 11: Full Validation

- [ ] Run focused Go tests for remediation application, Git/SSH/OpenAI/MCP/log
      adapters, project policy/configuration, persistence, HTTP, and bootstrap.
- [ ] Run PostgreSQL integration and migration/source tests for additive policy
      and audit changes.
- [ ] Run frontend unit/browser tests for any policy/discovery configuration UI.
- [ ] Run `go test ./...`, backend lint/vet gates, frontend lint/typecheck/tests,
      and repository-wide quality checks required by Trellis specs.
- [ ] Run GitNexus `detect_changes(scope: "compare", base_ref: "main")` before
      commit and inspect every affected flow.

## Risk Gates And Rollback

- Do not edit `EvidenceLogPort`; its current blast radius is HIGH. If later
  evidence proves an interface change unavoidable, rerun impact analysis and
  obtain explicit review before proceeding.
- Before editing each existing function/class/method, run GitNexus upstream
  impact as required by repository policy. Warn before HIGH/CRITICAL changes.
- Keep dynamic MCP disabled by default until policy rows exist. Rollback by
  disabling dynamic catalog/runtime wiring; do not delete policy, audit,
  credentials, or evidence rows.
- Stdio is independently feature-gated so remote MCP and the corrected built-in
  loop can ship without enabling process launch.
- Generic HTTP JSON is independently feature-gated so incomplete mappings never
  cause fallback arbitrary requests.
