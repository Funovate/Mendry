# Reduce Remediation Prompt Cost And Expose Cache Metrics

## Goal

Make the remediation harness complete multi-turn diagnosis and planning runs
without a first model request consuming the run's wall-clock budget, while
making prompt size and provider cache behavior directly diagnosable from
structured logs.

## Background

- A production run transitioned from `diagnosing` to `budget_exhausted` after
  one successful model call with 360,520 input tokens, 762 output tokens, no
  tool calls, and no repository or evidence bytes.
- Model tokens are accounting-only. Given the recorded counters and default
  limits, the run exhausted the five-minute elapsed-time budget.
- Initial bootstrap context is bounded, but an MCP catalog may contain up to
  128 phase-approved tools with schemas bounded individually. There is no
  catalog-wide schema-size bound.
- The OpenAI-compatible adapter currently retains only prompt, completion, and
  total token counts. Provider cache hit/miss details are discarded.
- Each model turn currently combines provider-native history with a new user
  message containing the full compacted conversation context, duplicating
  bootstrap and prior observations.
- Existing design documents require bounded conversation history and native
  tool calls, but define no stable-prefix, cache-observability, or cache-hit
  acceptance contract.

## Requirements

### R1. Phase-Aware, On-Demand Tool Exposure

- Do not add a catalog-wide tool-schema byte limit.
- Preserve the existing per-tool schema validation and policy authority.
- Advertise only tools relevant to the current remediation phase.
- Large dynamic MCP catalogs must support on-demand exposure so every approved
  schema is not sent on every model request.
- The model-visible mechanism is a lightweight
  `source.search_tools(query, limit)` tool. It searches only policy-approved,
  phase-allowed MCP tools, returns compact identities/descriptions, and
  activates the matches so their complete schemas are advertised from the
  following model turn.
- Activated tools remain selected for the run, but every phase continues to
  apply its own policy filter. A tool selected during diagnosis is not exposed
  during planning unless the snapshotted policy also allows it there.
- On-demand selection must not let the model bypass project/source policy,
  phase restrictions, schema validation, or the Tool Gateway execution path.
- Tool ordering and selection must be deterministic for equivalent run state.

### R2. Stable, Non-Duplicated Conversation Prefix

- Bootstrap context must enter provider-native conversation history once.
- Later turns append only new assistant, tool, protocol-correction, or phase
  instruction content needed for that turn.
- Do not place the same compacted conversation both in `Messages` and the new
  `UserMessage`.
- Preserve bounded history, deterministic compaction, native tool-call pairing,
  strict JSON-envelope compatibility, and planning access to diagnosis context.

### R3. Provider Cache Usage

- Normalize provider-reported cache hit and cache miss input-token counts when
  present.
- Support OpenAI `prompt_tokens_details.cached_tokens` and compatible-provider
  top-level cache hit/miss fields without making either shape mandatory.
- Expose cache usage on the correlated per-turn remediation and outbound LLM
  logs; this task does not add a database or API cache-metrics surface.
- Missing provider cache fields mean unknown/unreported, not a fabricated hit.

### R4. Request And Tool-Catalog Observability

- Every remediation model-turn completion record must include:
  - `tool_count`: number of provider-visible tool definitions;
  - `tool_schema_bytes`: complete serialized parameter-schema bytes for those
    definitions, measured by one documented deterministic method;
  - `request_bytes`: complete serialized outbound HTTP request payload size;
  - cache hit/miss token counts when reported.
- `request_bytes` is a numeric count of the full serialized payload. This task
  does not log the complete request body.
- Existing request/response snapshots remain redacted and bounded.
- Terminal budget exhaustion logs must identify the exhausted dimension, at
  minimum distinguishing elapsed time, model calls, model cost, tool calls,
  evidence bytes, and repository bytes.
- A successful state transition to `budget_exhausted` must not be presented as
  a successful remediation outcome.

### R5. Elapsed-Time Enforcement

- Separate run wall-clock budget reporting from model-call duration reporting.
- Treat run elapsed as admission control for the next model or tool operation,
  not as a deadline that cancels an already-started model call.
- A model response that succeeds within the provider's independent timeout
  must be processed normally even if the run elapsed limit was crossed while
  waiting. If that response requires another model/tool operation, stop before
  starting it and expose `budget_exhausted reason=elapsed`.
- Keep provider timeout failures distinct from run elapsed exhaustion in logs.
- Do not merely raise the default elapsed limit to hide oversized prompts.
- Preserve existing hard limits and persisted counters unless a schema change
  is explicitly required by the final design.

### R6. End-To-End Harness Proof

- Add a representative multi-turn test that completes the path from diagnosis
  through at least one tool request/result and a subsequent model decision.
- Cover the code-fixable path through planning/review when existing fakes and
  frozen scope allow it.
- Assert prompt growth is incremental and tool exposure changes by phase or
  explicit on-demand selection.

## Acceptance Criteria

- [ ] AC1: A large approved MCP catalog is not serialized in full on every
      model turn; tool exposure is phase-aware and on-demand without a total
      schema-byte cap.
- [ ] AC2: Repeated turns do not duplicate bootstrap or prior observations in
      both history and the new user message.
- [ ] AC3: OpenAI and compatible cache usage shapes map to normalized hit/miss
      counters, while absent fields remain unreported.
- [ ] AC4: Structured model-turn logs contain accurate `tool_count`,
      `tool_schema_bytes`, `request_bytes`, and available cache counters.
- [ ] AC5: Budget exhaustion records contain the exact exhausted dimension and
      do not label the remediation terminal result as success.
- [ ] AC6: Tests prove a multi-turn diagnosis/tool/diagnosis flow completes
      within configured budgets and a code-fixable flow reaches review.
- [ ] AC7: Existing remediation, OpenAI adapter, observability, and security
      tests pass with no raw prompt, tool payload, evidence, or credential added
      to logs.

## Out Of Scope

- A catalog-wide tool-schema byte budget.
- Unrestricted dynamic tool execution or trusting MCP annotations as policy.
- Logging complete model request bodies on the success path.
- Provider-specific cache creation controls that are unsupported by the
  configured OpenAI-compatible endpoint.
- Raising budgets as the primary remediation for oversized prompts.
