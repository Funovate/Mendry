# Make remediation context collection tool-driven

## Goal

Start the remediation harness from durable incident and project metadata, then
let the agent gather repository and operational evidence through bounded,
trusted tools. A Git, SSH, log MCP, or log API read failure must be a tool
observation that the agent can reason about, not a prerequisite failure that
prevents the diagnosis loop from starting.

## Background

- `ContextAssembler.AssembleInitialContextObserved` eagerly calls both
  `RepositoryReadPort.ListTree` and `EvidenceLogPort.Search`. Either adapter
  error terminates `preparing_context` before the first model turn
  (`backend/internal/modules/remediation/application/context_assembler.go:59-81`).
- `RemediationCoordinator.drive` converts that preparation error directly into
  the terminal `failed` state
  (`backend/internal/modules/remediation/application/coordinator.go:329-345`).
- The bounded agent loop starts only after the eager reads succeed, so the
  observed failure correctly reports zero model and tool calls
  (`backend/internal/modules/remediation/application/coordinator.go:347-389`).
- The Tool Gateway already advertises repository and evidence read tools and
  treats adapter errors as recorded, non-fatal tool invocations
  (`backend/internal/modules/remediation/application/tool_gateway.go:13-23`,
  `backend/internal/modules/remediation/application/coordinator.go:566-597`).
- Tool results still cannot drive reasoning: `ToolResult.Payload` is explicitly
  excluded from model context, and every diagnosis turn receives the unchanged
  `initialContext` string
  (`backend/internal/modules/remediation/application/tool_gateway.go:65-74`,
  `backend/internal/modules/remediation/application/coordinator.go:347-352`).
- Tool definitions do not reach the provider: `AgentEngine` fills
  `ModelTurn.Tools`, but the OpenAI request type and encoder include only model,
  response format, and messages
  (`backend/internal/modules/remediation/application/agent_engine.go:57-63`,
  `backend/internal/modules/remediation/adapter/openai/client.go:93-99`,
  `backend/internal/modules/remediation/adapter/openai/client.go:127-136`).
- Production runtime currently supports only an SSH source. Cloud and MCP source
  configurations can be persisted, while the remediation loader rejects every
  non-SSH source (`backend/internal/bootstrap/remediation_loaders.go:49-58`).
- The stdio MCP task deliberately stops at typed configuration and does not
  implement process launch, handshake, tool discovery, or `tools/call`
  (`.trellis/tasks/08-14-stdio-mcp-harness-config/prd.md`).
- The parent harness design requires transient connector failures to use a
  shared bounded attempt budget; the current walking skeleton deferred worker
  retry and dead-letter durability.

## Requirements

### R1. Tool-driven startup

- `preparing_context` must resolve and validate durable control-plane inputs
  such as incident identity, exact deployed commit, project identity, enabled
  source metadata, and available tool capabilities without requiring a remote
  Git or log read to succeed.
- A run with valid control-plane inputs must enter `diagnosing` and perform its
  first model turn even when Git or a log connector is unavailable.
- Unrecoverable control-plane failures may still fail the run before diagnosis;
  connector read failures may not.

### R2. Agent-visible tool observations

- Tool definitions must reach the model through the actual provider request;
  populating an unused `ModelTurn.Tools` field does not satisfy this contract.
- Every accepted tool call must produce a bounded observation for the next
  model turn, including successful data, empty results, policy rejection, and
  adapter failure. Git, MCP, and log-API payloads stay secret-pattern redacted.
  SSH `ssh.inspect` stdout/stderr are an explicit exception: the model receives
  the bounded raw command output, closer to `classfang/ssh-mcp-server` than to
  the existing evidence-line redaction path.
- Tool observations must preserve ordering and identify the requested logical
  tool without exposing credentials, authenticated remotes, raw connector
  clients, or unrestricted payloads. SSH inspect may include the normalized
  command and raw output; it must not include private-key bytes, temp key
  paths, or the unparsed rejected command string beyond a stable rejection
  code.
- The agent must be able to use one observation to choose another Git/log read,
  retry a retryable failure, change its query, use another available source, or
  return an evidence-insufficient diagnosis.
- Planning turns must receive the diagnosis/evidence context needed to produce
  evidence-backed candidates; they must not silently lose prior tool results.

### R3. Trusted repository and log capabilities

- Repository access remains pinned to the exact deployed commit and is exposed
  only through the existing read-only Git capabilities.
- SSH evidence is one built-in `ssh.inspect` tool. The model writes an inspect
  command string; the gateway parses it, allowlists inspect binaries, allows
  at most a three-segment `|` pipeline, reconstructs a quoted argv, and the
  trusted SSH adapter executes that reconstructed command. The harness must not
  wrap log collection as `tail` of `logPath` or advertise `evidence.search` /
  `evidence.context` for SSH sources.
- Configured `projectFolder` is the remote working directory. Configured
  `logPath` is a bootstrap hint for where logs often live, not a file the
  harness or first-turn prompt may `tail`/`cat` automatically. The model must
  `ls` and discover actual file names before writing a read command.
- Log API / cloud evidence remains bounded logical search/context behind the
  existing evidence port. Executable MCP sources instead expose approved
  server-defined tools directly under the dynamic-tool rules below.
- For an executable MCP source, the trusted runtime must perform MCP
  initialization and `tools/list`, then expose the discovered tool names,
  descriptions, and JSON Schemas directly to the harness. The model must be
  able to select domain-specific MCP capabilities instead of seeing only a
  normalized log-search abstraction.
- Every discovered MCP tool remains mediated by the Tool Gateway. Discovery
  does not grant execution by itself: project/source capabilities, run phase,
  tool policy, argument validation, time/size budgets, and output redaction must
  authorize each `tools/call` before the trusted MCP client executes it.
- Dynamically exposed MCP tool identifiers must be namespaced by source/server
  identity so they cannot collide with built-in repository/evidence tools or
  tools discovered from another server.
- Only MCP tools explicitly approved by the snapshotted project remediation
  tool policy may be exposed. A missing policy defaults to no exposed MCP
  tools. Server-provided annotations, including read-only hints, are descriptive
  input and never grant authority by themselves.
- The approved MCP tool keeps the server-provided description and input JSON
  Schema. Namespacing is the only model-visible identity transformation; the
  runtime keeps the mapping to the original server tool name internally.
- MCP capability discovery may run once as a bounded, non-blocking catalog
  probe during `preparing_context`. A discovery failure must be represented in
  the first model turn as unavailable capability state, and a built-in bounded
  refresh action must let the harness request another discovery attempt.
- Connector selection, credential resolution, transport execution, redaction,
  and payload bounding remain behind trusted ports/gateways. The model receives
  capabilities and safe observations, never credentials or raw clients.
- Source capability metadata must control which tools are advertised. Disabled
  or unsupported sources must be represented accurately instead of failing a
  later tool call due to misleading advertisement.
- SSH must not pass the model string to a remote shell (`bash -c`, unparsed
  `ssh host <raw>`). Rejected syntax includes `;`, `&&`, `||`, `$()`,
  backticks, redirections, environment assignments, and `sudo`. Path jail is
  the SSH user; relative `..` is rejected; absolute paths are not extra-
  chrooted. Log API sources must not expose arbitrary URLs, methods, or
  headers. MCP is the sole source kind in this scope whose approved
  server-defined tool schemas are dynamically advertised.

### R4. Failure semantics and bounded autonomy

- Connector errors must have stable safe classifications and an explicit
  retryability signal. Invalid inspect syntax is a policy rejection before SSH.
  Git/MCP/log-API observations omit secrets, authenticated remotes, headers,
  and provider bodies. SSH inspect stdout/stderr are bounded raw model-visible
  output; operator logs still omit private-key bytes, temp key paths, and
  plaintext credentials. `env`/`printenv` may therefore leak secrets into the
  project LLM context — accepted for this slice.
- Agent-requested retries and alternative reads count against the existing
  elapsed-time, tool-call, evidence-byte, and repository-byte budgets; transport
  retries must also be bounded so nested retries cannot multiply indefinitely.
- Connector retry exhaustion or unavailable evidence is returned to the model
  and can lead to a persisted `insufficient_evidence` /
  `blocked_manual_review` outcome with missing-source information, rather than
  a harness crash. Crossing a hard run budget retains the existing
  `budget_exhausted` terminal state.
- Corrupt run state, invalid identity/configuration invariants, and persistence
  failures remain terminal infrastructure failures.

### R5. Compatibility and auditability

- Preserve frozen credential-free repository, evidence, and LLM boundaries
  unless the design demonstrates that a backward-compatible extension cannot
  express source capabilities or observations.
- Preserve the coordinator as the only run-state owner and the Tool Gateway as
  the only model-requested execution entry point.
- Persist bounded tool metadata and stable errors without duplicating raw logs
  or repository contents. Debug observations remain redacted and bounded.
- Existing SSH projects must migrate without configuration changes. Existing
  cloud/MCP configurations must either become executable when supported or be
  advertised as unavailable with an actionable stable reason.
- Existing strict JSON `requestTool` envelopes remain a compatibility path, but
  provider-native tool calls may be added additively. Both paths must resolve
  through the same catalog and gateway policy.

## Acceptance Criteria

- [ ] A valid run reaches its first diagnosis model turn with zero eager remote
      Git/SSH/log data reads during `preparing_context`; an optional bounded MCP
      catalog probe cannot block the transition.
- [ ] The first provider request contains the exact built-in and approved
      discovered tool definitions advertised for that run and phase.
- [ ] A model-requested repository read returns bounded content to the following
      model turn and can influence its next envelope.
- [ ] An SSH source advertises `ssh.inspect` and does not advertise
      `evidence.search` or `evidence.context`; the first model turn includes
      host, user, `projectFolder`, and `logPath` as hints and does not execute
      a remote command during `preparing_context`.
- [ ] A model-requested SSH inspect or log-API read failure is recorded as a
      safe tool observation; the run remains in the bounded diagnosis loop.
- [ ] `ls /var/log | grep app` is accepted as a parsed pipeline; `ls; rm -rf /`,
      `cat $(pwd)`, and `sudo journalctl` are rejected before SSH.
- [ ] Inspect output over 64KiB is truncated with `truncated=true` and still
      returns the captured prefix; command timeout remains the existing 15s SSH
      bound.
- [ ] A scripted model can recover from one failed tool by retrying or selecting
      another available tool, then produce an evidence-cited diagnosis.
- [ ] Repeated tool/transport failure stops without an unbounded loop: connector
      retry exhaustion remains a model-visible observation, while crossing a
      hard run budget terminates inspectably as `budget_exhausted`.
- [ ] Tool advertisements match the configured and enabled source capabilities;
      unsupported runtime transports are never presented as executable.
- [ ] An MCP fixture completes initialization and discovery, exposes its
      namespaced tool metadata/schema to a model turn, executes an authorized
      `tools/call` through the gateway, and returns a bounded redacted
      observation to the following model turn.
- [ ] An MCP tool absent from the project policy is omitted from the model tool
      catalog even when the server returns it from discovery; annotations alone
      cannot make it executable.
- [ ] Unknown, colliding, out-of-phase, disallowed, malformed, over-budget, or
      unavailable MCP tools are rejected before `tools/call` without terminating
      the diagnosis loop.
- [ ] SSH inspect, supported MCP, and supported log API adapters pass the same
      bounds, safe-error, and credential-isolation contract tests. Git/MCP/log
      API keep secret-pattern redaction; SSH inspect output is bounded raw.
- [ ] Existing exact-commit Git isolation, tool policy rejection, budget,
      remediation state, and observability tests remain passing.

## Out Of Scope

- Repository mutation, patch application, validation command execution,
  commit/push/PR creation, deployment, or automatic merge.
- Giving the model an unparsed remote shell (`bash -c` of the model string),
  upload/download, interactive SSH sessions, sudo, docker-exec wrappers,
  arbitrary HTTP requests, MCP process control, or unrestricted connector
  authority.
- Infinite retry-until-success behavior or retries outside the shared run
  budget.
