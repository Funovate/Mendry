# Technical Design: Tool-Driven Remediation Context

## Design Basis

This design corrects the walking-skeleton implementation without weakening the
authority model established by the earlier Claude Code harness work:

- `RemediationCoordinator` is the only run-state owner.
- `AgentEngine` receives typed tool definitions and returns structured model
  intent; it never receives adapter clients or credentials.
- `ToolGateway` is the sole execution entry point for model-requested actions.
- Tool output is untrusted, bounded, and redacted before the next model turn.
- Git reads remain pinned to the run's exact deployed commit.

The current implementation violates the intended data flow in three places:

1. `ContextAssembler` performs mandatory Git and SSH data reads before the
   model/tool loop exists.
2. `ToolResult.Payload` is kept out of model context, so a successful or failed
   tool call cannot affect the next turn.
3. `ModelTurn.Tools` is populated by `AgentEngine` but ignored by the OpenAI
   adapter, so the provider never receives the advertised schemas.

## Decisions

### D1. Start From Metadata, Read Through Tools

`preparing_context` validates only durable control-plane inputs and builds a
compact bootstrap context:

- run, incident, project, environment, and source identities;
- priority, trigger reason, and exact deployed commit;
- source kind, enabled state, declared capabilities, and configuration version;
- repository and source capability status;
- remaining run budget and the initial tool-catalog version;
- for an SSH source: host, user, `projectFolder`, and `logPath` as inspect
  hints, never credentials or a precomputed tail of `logPath`.

It does not clone/fetch Git, execute SSH, or query a log API. MCP discovery is
the sole optional remote action in this phase because dynamic schemas must be
known before they can be advertised. It is attempted at most once, has its own
short timeout, and is non-blocking. Failure becomes capability status rather
than a phase error.

### D2. Preserve Frozen EvidenceLogPort

GitNexus reports `EvidenceLogPort` modification as HIGH risk with at least 19
direct dependencies and incomplete dynamic-dispatch visibility. The existing
interface remains unchanged and is no longer the SSH evidence path.

- Normalized log API / cloud implementations continue behind
  `EvidenceLogPort.Search/GetContext`.
- SSH inspect uses a new credential-free inspect port. Do not add command
  execution methods to `EvidenceLogPort`.
- Dynamic MCP uses a new `DynamicToolRuntimePort` rather than adding generic MCP
  methods to the evidence interface.
- `ToolGateway` aggregates built-in repository, SSH inspect, evidence, and
  dynamic MCP routes into one phase-specific catalog.

Illustrative additive contract:

```go
type DynamicToolRuntimePort interface {
    Discover(context.Context, DynamicToolScope) (DynamicToolCatalog, error)
    Call(context.Context, DynamicToolScope, DynamicToolCall) (DynamicToolResult, error)
    CloseRun(context.Context, string) error
}

type ToolPolicyResolver interface {
    ResolveToolPolicy(context.Context, string, string) (ToolPolicySnapshot, error)
}
```

The port uses credential-free project/source/run identifiers. The adapter owns
remote headers, stdio environment secrets, process/session handles, and MCP SDK
types.

### D3. Direct MCP Exposure Means Direct Schema, Mediated Execution

For an MCP source, the runtime performs initialization and paginated tool
discovery. For each discovered tool it retains:

- original server tool name;
- server description;
- complete input JSON Schema within configured size/depth limits;
- non-authoritative annotations;
- source/server identity and a definition hash.

Only the intersection of discovered tools and the run's snapshotted project
tool policy is advertised. Missing policy means no dynamic MCP tools. MCP
annotations never authorize a tool because they are supplied by the server.

The model-visible identifier is a provider-safe namespace derived from source
identity plus a sanitized tool name and collision hash. The original name,
description, and schema semantics are otherwise preserved. The registry keeps
the opaque mapping back to the source and original MCP name.

This is equivalent to the old static registry's security property: a tool is
visible only after deterministic code registers it for the current phase. It
does not give the model an MCP client or arbitrary `tools/call` authority.

### D4. Tool Policy Is Separate From Connector Configuration

The stdio MCP configuration task intentionally deferred `allowedTools` to the
runtime layer. This task therefore adds a versioned remediation tool-policy
record keyed by project and source rather than embedding authorization in MCP
connection JSON.

Each policy entry identifies the original MCP tool name, allowed run phases,
and the permitted effect class. This slice supports `read` only. Project admins
manage the policy; operators may use but not widen it. The run snapshots policy
and source versions so a mid-run configuration change cannot silently grant a
new capability.

An administrative discovery/preview path may display all discovered tool
metadata, but the agent sees only approved definitions. Audit records contain
tool identity, policy version/hash, and bounded metadata, never connector
credentials.

### D5. Built-In Tools Remain Narrow

Built-in repository tools remain `repository.list_tree`, `read_file`, `search`,
and `history`. They execute only at `RepoRef.Commit`.

SSH sources advertise only `ssh.inspect`. The model supplies a command string.
The gateway tokenizes it without a shell, allows this inspect binary set:

`ls`, `cat`, `head`, `tail`, `grep`, `egrep`, `fgrep`, `find`, `stat`, `wc`,
`file`, `readlink`, `realpath`, `pwd`, `date`, `uname`, `hostname`, `df`, `du`,
`ps`, `journalctl`, `dmesg`, `id`, `env`, `printenv`.

At most three `|` segments are allowed. `find` rejects `-exec`/`-delete`/`-ok`.
`journalctl` rejects follow/vacuum flags. Rejected syntax never reaches SSH and
is a policy `rejected` observation. The adapter executes the reconstructed
quoted argv with `cd -- projectFolder` prepended, a 15s timeout, and a 64KiB
combined stdout/stderr cap. Overflow truncates and marks `truncated=true`.

Inspect success payload is `{command, exitCode, stdout, stderr, truncated}`.
No evidence-line IDs. No secret-pattern redaction on stdout/stderr. Tool
description and diagnosing prompt tell the model to `ls` the hinted `logPath`
directory and discover file names before reading; the harness never auto-tails.

Configured log API sources still expose bounded logical evidence tools and do
not accept model-provided URLs, methods, or headers.

MCP is different because the server defines typed tools. Approved discovered
MCP definitions are exposed directly instead of being collapsed into SSH
inspect or `evidence.search/context`.

### D6. Complete The Agent Tool Loop

The coordinator owns a bounded per-run `AgentConversation` containing:

- bootstrap metadata context;
- model assistant turns;
- ordered tool requests;
- ordered, model-visible tool observations;
- catalog version/hash and context version;
- compacted diagnosis context reused by planning.

Every tool observation has a stable shape:

```json
{
  "sequence": 4,
  "tool": "mcp_logs_a1b2_query_errors_c3d4",
  "status": "success|error|rejected",
  "content": {},
  "truncated": false,
  "bytes": 1204,
  "error": {
    "code": "connector_timeout",
    "retryable": true,
    "message": "connector request timed out"
  }
}
```

`content` is present only on success and is already bounded. Git/MCP/log-API
content is secret-pattern redacted. SSH inspect `content` is the bounded raw
command result. Errors carry safe codes and guidance. Tool-result content
cannot change the system contract or grant capabilities.

The next model turn receives the observation. Planning receives a compacted
view of the same conversation instead of the current empty context.

Per-result model-visible content is capped independently from adapter output;
the conversation has an aggregate cap and deterministic compaction. Compaction
retains tool/error identity, evidence IDs, content hashes, latest relevant
excerpts, and all final structured decisions.

### D7. Provider-Native Calls Are Additive

The existing strict JSON envelope remains supported for compatibility. The LLM
domain values gain additive provider-neutral message/tool-call fields:

- model turns can carry ordered messages plus typed tools;
- model results can carry zero or more typed tool calls;
- providers without native tool calling may continue returning a validated
  `requestTool` envelope.

The OpenAI adapter must actually serialize advertised tools and parse returned
tool calls. Native calls and JSON-envelope calls are normalized to the same
gateway request. Multiple native read calls are executed sequentially in model
order so budget accounting, audit order, and catalog changes remain
deterministic.

### D8. Capability Discovery And Refresh

Tool catalog assembly is:

```text
load source metadata + policy snapshot
  -> add phase-allowed built-in repository tools
  -> add SSH/log API tools when source config declares and supports them
  -> for MCP: bounded best-effort initialize + tools/list
       -> filter by policy
       -> namespace and validate schemas
       -> publish catalog version/hash
```

If MCP discovery fails, the catalog still contains a built-in
`source.refresh_tools` control action and a safe capability-status observation.
The agent can retry discovery, use repository tools, or conclude insufficient
evidence. A successful refresh creates a new catalog/context version for the
next model turn; it never mutates the set of tools inside an in-flight turn.

### D9. MCP Runtime Lifecycle

Remote HTTP/SSE/streamable-HTTP and stdio are trusted adapter transports. The
adapter handles initialization, pagination, calls, cancellation, and close.

- Remote credentials are resolved from same-project encrypted secrets and
  injected only into the transport.
- Stdio combines configured non-secret environment with decrypted `secretEnv`
  only at launch, uses argv rather than a shell, and clears plaintext after the
  session closes.
- One run owns its session; terminal completion/cancellation closes it. A TTL
  cleanup handles abandoned sessions.
- Command launch, cwd, environment count/size, process lifetime, output, and
  child-process behavior are bounded. Stdio implementation depends on the
  separately planned stdio configuration contract and must not accept ad hoc
  model-provided command data.
- MCP text and structured JSON results are supported first. Binary/audio/image
  blocks, embedded resources, and resource links are omitted with an explicit
  bounded unsupported-content marker unless a later policy enables them.

### D10. Error And Retry Semantics

Adapters return typed safe errors classified at minimum as policy, invalid
arguments, unavailable capability, authentication, authorization, not found,
rate limit, timeout, transport, remote execution, invalid response, and
internal error.

Only timeout, rate limit, transport interruption, and explicitly uncertain
remote errors are retryable. Authentication, authorization, invalid config,
and policy rejection are not. SSH inspect stderr is part of the bounded model-
visible result. Operator logs still omit private-key bytes and temp key paths;
exit status and classification remain on the tool observation.

The agent chooses whether to retry a tool or switch capability. Each call and
refresh consumes the shared tool/elapsed/data budget. Transport implementations
do not hide unbounded nested retries. Connector retry exhaustion becomes a tool
observation; hard run-budget exhaustion may still terminate as
`budget_exhausted`.

### D11. Log API Boundary

The existing `cloud` source shape is insufficient for a generic HTTP JSON log
API because it lacks endpoint, authentication shape, pagination, and response
mapping. The generic adapter therefore requires a versioned explicit connector
contract rather than guessing vendor URLs or accepting model-provided requests.

That contract owns configured base endpoint, credential reference, fixed
request template, query-variable mapping, pagination, response JSON mapping,
and bounds. It implements `EvidenceLogPort`; the harness still advertises only
bounded evidence tools. Provider-specific cloud adapters may implement the same
port without changing the agent loop.

## Runtime Flow

```text
queued
  -> preparing_context
       load metadata/source/policy
       assemble static catalog
       best-effort MCP discovery (non-blocking)
  -> diagnosing
       model receives conversation + allowed tool catalog
       -> tool call(s)
            gateway validates phase/policy/schema/budget
            trusted provider executes
            bounded observation appended
            loop
       -> diagnosis
            persist context/catalog versions + evidence refs
            route fixability
  -> planning
       model receives compacted diagnosis conversation
       -> planCandidates
```

Tool adapter failure never advances directly to `failed`. A malformed but
JSON-or-native protocol envelope is a bounded, retryable observation and stays
in the current phase until the model returns a valid envelope or a hard run
budget is crossed. Control-plane identity corruption, persistence failure,
invalid state transition, and an unrecoverable provider/infrastructure failure
remain harness failures.

## Persistence And Audit

The durable model stores or extends:

- versioned remediation tool policy and entries;
- run source/policy/catalog version snapshots and hashes;
- tool invocation original/public IDs, phase, outcome, safe error code,
  retryability, timing, byte counts, result hash, truncation, and evidence IDs
  where the tool still produces them; SSH inspect stores the normalized command
  hash and byte counts, not a full transcript;
- decision context/catalog versions and cited observation/evidence IDs.

Raw repository contents, raw logs, unrestricted MCP results, schemas containing
secret defaults, provider request bodies, and credentials are not persisted in
audit JSON. Approved bounded evidence storage remains the source of retained
excerpts.

## Compatibility And Rollout

1. Add additive domain fields, policy storage, inspect port, and runtime ports
   while leaving `EvidenceLogPort` unchanged.
2. Land the agent conversation/tool-result loop against fakes and existing
   built-in tools.
3. Remove mandatory remote reads from context preparation.
4. Add remote MCP discovery/call behind disabled-by-default policy.
5. Enable stdio only after its configuration/secret contract and restricted
   launcher are available.
6. Add the explicit generic HTTP JSON log connector contract/adapter.
7. Enable per project after discovery preview and allowlist configuration.

Existing SSH projects need no config migration. Existing MCP configs remain
readable but expose no dynamic tool until policy is configured. Rollback disables
dynamic runtime and restores the earlier static catalog; additive policy/audit
rows remain inert. Do not roll back by deleting credentials or evidence.

## Rejected Alternatives

- **Retry eager SSH until success:** permanent auth/path errors loop forever and
  the model never gets a chance to choose another source.
- **Normalize every MCP server to two log tools:** hides domain-specific trace,
  metric, topology, or alarm capabilities from the harness.
- **Expose every discovered MCP tool:** contradicts the old static registry's
  allow-before-advertise boundary and may expose mutation/control-plane tools.
- **Trust MCP read-only annotations:** annotations are server-supplied hints, not
  project authorization.
- **Add generic methods to EvidenceLogPort:** HIGH blast radius and mixes a
  stable evidence abstraction with arbitrary dynamic schemas.
- **Pass the model command string to SSH/shell unchanged:** regex allowlists
  on the raw string, as in `classfang/ssh-mcp-server`, are bypassable with
  pipes, substitution, and `;`. Parse then reconstruct argv instead.
- **Auto-tail configured `logPath`:** the path is a hint; the current file name
  is not guaranteed. The model must list and then read.
- **Expose unparsed SSH/HTTP:** gives the model command/network authority and
  bypasses connector policy.
- **Persist raw tool transcripts:** duplicates sensitive evidence and violates
  retention and credential-isolation contracts.
