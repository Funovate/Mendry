# Technical Design: Remediation Prompt Selection And Cache Observability

## Scope And Constraints

This task changes the remediation application, dynamic tool catalog, OpenAI-
compatible adapter, and structured logging projection. It does not add a
database/API/frontend surface and does not add a catalog-wide schema byte cap.

The existing authority boundaries remain unchanged:

- `RemediationCoordinator` owns run state and budget decisions.
- `ToolGateway` is the only model-requested execution entry point.
- Project/source policy and phase grants are authoritative.
- The OpenAI adapter owns provider HTTP serialization and response decoding.
- Logs contain numeric request/catalog metrics, not unbounded prompt bodies.

GitNexus reports LOW upstream risk for `BuildCatalog`,
`TurnObservedWithConversationAndTools`, and `ModelTurnCompleted`. OpenAI
`Client.Complete` is MEDIUM risk with seven direct adapter tests. No analyzed
symbol is HIGH or CRITICAL risk.

## Data Flow

```text
MCP discovery
  -> validate every policy-approved route and retain it privately
  -> advertise built-ins + source.search_tools + already activated routes
  -> model calls source.search_tools(query, limit)
  -> ToolGateway searches phase-allowed private routes and activates matches
  -> tool observation returns compact matches
  -> next model request includes complete schemas only for activated matches

AgentConversation
  -> full bounded fallback context for legacy providers
  -> append-only native history + pending incremental context for OpenAI
  -> OpenAI serializes one stable prefix and one incremental user message
  -> exact payload/tool metrics are computed at serialization
  -> provider usage normalizes cache hit/miss fields
  -> remediation and outbound observers emit numeric metrics
```

## D1. Deferred MCP Tool Search

Add `ToolSourceSearchTools = "source.search_tools"`. For a configured,
supported MCP source with a valid policy and successful discovery, this
lightweight control tool is visible without exposing every dynamic schema.

`ToolCatalog` retains all validated, approved dynamic routes privately and
adds an activated-name set. `DefinitionsForPhase` returns:

1. phase-relevant built-in definitions;
2. `source.search_tools` and `source.refresh_tools` for a usable MCP source;
3. activated dynamic definitions whose snapshotted policy allows the requested
   phase.

Dynamic routes are not executable until activated because
`ExecuteToolWithCatalog` continues to require `catalog.hasDefinition`.

`source.search_tools` accepts:

```json
{
  "query": "error logs",
  "limit": 5
}
```

- `query` is required, trimmed, and bounded using the existing argument/schema
  validation path.
- `limit` defaults to 5 and is capped at 10.
- Search considers original MCP name, provider-visible namespaced name, and
  bounded description. Matching and ordering are deterministic.
- Only routes allowed in the current phase participate.
- Returned matches are activated before the tool result is appended, so their
  full schemas appear in the following turn.
- The result contains compact `{name, description}` matches plus count and
  catalog version. Descriptions are independently bounded.
- No remote MCP call occurs during search.

Activation persists for the run. Phase filtering is reapplied on every
`DefinitionsForPhase` call. Refresh atomically replaces routes and clears the
activation set so stale identities cannot survive a discovery version change.

Built-in phase selection is made explicit instead of relying on the catalog's
initial diagnosing phase:

| Phase | Built-ins |
|---|---|
| diagnosing / collecting_more_context | repository reads plus the configured SSH or cloud evidence tools |
| planning | repository reads; MCP search/activated tools only when policy allows planning |
| preparing_context / terminal states | no model-facing catalog |

## D2. Incremental Native Conversation Without Legacy Regression

`ModelTurn.UserMessage` remains the complete bounded fallback request for
providers that ignore `Messages`, preserving the existing additive contract.
Add a provider-native continuation field that contains only the current phase
instruction and context not already represented in native history.

`AgentConversation` tracks whether bootstrap and each textual observation have
been delivered through native history:

- First turn: continuation contains phase instruction + bootstrap.
- Native assistant/tool-call/tool-result sequence: messages are appended once;
  the next continuation does not repeat them.
- Strict JSON `requestTool` compatibility: the assistant envelope is in
  history; its non-native tool result is delivered once as incremental text.
- Protocol correction: the correction observation is delivered once after the
  invalid assistant response.
- Planning: diagnosis history remains in `Messages`; the new continuation is
  the planning instruction, not a replay of the diagnosis context.
- Provider failure does not acknowledge pending context; a retry resends it.

The OpenAI adapter serializes the continuation when present and otherwise
falls back to `UserMessage`. `RecordModelTurn` records the exact provider-native
user content, keeping the next request append-only. Existing aggregate bounds
remain. When history compaction removes old native messages, a deterministic
bounded checkpoint may be inserted; that intentional compaction is the only
normal event allowed to rebuild the prefix.

## D3. Exact Request And Cache Metrics

The OpenAI adapter is the sole owner of exact serialized metrics:

- `request_bytes = len(payload)` after the final `json.Marshal`;
- `tool_count = len(providerTools)`;
- `tool_schema_bytes = sum(len(json.Marshal(definition.Parameters)))` for the
  provider-visible definitions in that request.

The schema measure excludes names, descriptions, and JSON wrapper overhead so
it remains a stable diagnostic for schema growth. `request_bytes` captures all
wrapper and message overhead.

Add provider-neutral result metadata for request bytes, tool count/schema
bytes, cached input tokens, cache-miss input tokens, and a reported flag.
Cache normalization accepts:

- OpenAI: `usage.prompt_tokens_details.cached_tokens`; miss tokens are
  `max(prompt_tokens - cached_tokens, 0)`.
- Compatible providers: top-level `usage.prompt_cache_hit_tokens` and
  `usage.prompt_cache_miss_tokens`. When these are present, they take
  precedence over the derived OpenAI shape.
- No cache fields: reported is false and cache counters are omitted from logs.

No cache counter is persisted in PostgreSQL in this task. The established
remediation trace contract is log-only, and per-turn cache behavior is more
useful than a lossy run aggregate.

Both log surfaces carry the metrics:

- `llm.request.completed`: exact request/tool metrics for every HTTP attempt,
  including failures and retries; cache fields on a decoded response.
- `remediation.model_turn.completed`: the successful logical turn metrics,
  correlated to run, phase, and sequence.

Stable observability constants own `request_bytes`, `tool_count`,
`tool_schema_bytes`, `model_cache_hit_tokens`, and
`model_cache_miss_tokens`. Existing redacted 4 KiB request/response snapshots
remain unchanged.

## D4. Operation Admission And Exhaustion Reason

`runBudget` returns a stable exhaustion reason instead of a boolean. Reasons
are `elapsed`, `model_calls`, `model_cost`, `tool_calls`, `evidence_bytes`, and
`repository_bytes`, evaluated in deterministic order.

Elapsed time is removed from post-operation `consume` checks. Instead, the
coordinator checks elapsed immediately before each model or tool operation:

- If time remains, start the operation with the caller context. The OpenAI
  adapter's independent HTTP timeout remains authoritative for that request
  (currently 30 seconds by default).
- If the run limit is already exhausted, do not start the operation; transition
  from the current phase to `budget_exhausted reason=elapsed`.
- If a model call starts within budget and returns successfully within its
  provider timeout, process and persist that response normally even when the
  run limit was crossed while waiting.
- If that response requires another tool or model turn, the next admission
  check stops the run before the operation begins.

This avoids discarding a valid but slow terminal diagnosis and keeps provider
timeouts distinguishable from run elapsed exhaustion. This task does not add a
new provider-timeout configuration surface.

`StateTransitionObservation` gains the optional exhaustion reason. A transition
to `budget_exhausted` logs `outcome=stopped` plus
`budget_exhausted_reason=<reason>` instead of the current unconditional
`outcome=success`. Normal committed transitions remain `success`.

The default five-minute limit and other hard limits are unchanged.

## D5. Compatibility And Security

- No schema migration, REST change, new secret, or provider SDK type.
- The legacy `ExecuteTool` path remains unchanged; deferred search applies to
  per-run dynamic catalogs.
- Strict JSON envelopes and provider-native tool calls still normalize through
  the same gateway.
- Search results reveal only policy-approved names and bounded descriptions.
- Numeric metrics are safe at INFO/DEBUG. Raw prompts, complete request bodies,
  evidence, tool payloads, and credentials do not gain new logging permission.
- Existing per-tool schema and result bounds remain intact.

## D6. Validation And Rollback

Tests cover deferred discovery/activation, phase filtering, refresh reset,
incremental history, strict-envelope and native tool-result delivery, both
cache usage response shapes, exact byte metrics, retry logging, operation
admission, slow successful terminal responses, exhaustion reasons, and log
secrecy.

Rollback is file-local: reverting the new control tool/catalog activation,
continuation field, usage metadata, and logging fields restores the old request
shape. There is no database migration or durable data rollback.
