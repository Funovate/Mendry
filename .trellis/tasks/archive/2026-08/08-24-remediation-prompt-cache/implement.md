# Implementation Plan: Remediation Prompt Selection And Cache Observability

## Step 1. Add Deferred Dynamic Tool Search

- [x] Add `source.search_tools` definition, bounded parameters, and description.
- [x] Change `ToolCatalog` to retain approved routes privately and advertise
      only activated dynamic definitions.
- [x] Implement deterministic phase-filtered search-and-activate behavior in
      `ExecuteToolWithCatalog` without calling the MCP runtime.
- [x] Recompute built-in definitions for the requested phase and clear
      activation after refresh.
- [x] Update dynamic catalog tests for default deferral, search activation,
      phase filtering, no policy bypass, deterministic limit/order, and refresh.

Primary files:

- `backend/internal/modules/remediation/application/tool_catalog.go`
- `backend/internal/modules/remediation/application/tool_gateway.go`
- `backend/internal/modules/remediation/application/tool_gateway_dynamic.go`
- `backend/internal/modules/remediation/application/tool_catalog_dynamic_test.go`
- `backend/internal/modules/remediation/application/tool_gateway_test.go`

Rollback point: dynamic catalog tests pass before conversation changes begin.

## Step 2. Make Provider-Native Conversation Incremental

- [x] Add the provider-native continuation field to `domain.ModelTurn` while
      preserving full `UserMessage` fallback semantics.
- [x] Track pending/native-delivered conversation content and acknowledge it
      only after a successful provider result.
- [x] Have `AgentEngine` build full fallback plus incremental continuation and
      record the exact continuation in history.
- [x] Have the OpenAI message encoder prefer continuation content.
- [x] Add tests proving bootstrap, assistant output, native tool results,
      strict-envelope tool observations, protocol corrections, and planning
      context are delivered exactly once in the actual OpenAI message sequence.

Primary files:

- `backend/internal/modules/remediation/domain/types.go`
- `backend/internal/modules/remediation/application/conversation.go`
- `backend/internal/modules/remediation/application/agent_engine.go`
- `backend/internal/modules/remediation/application/conversation_test.go`
- `backend/internal/modules/remediation/application/coordinator_test.go`
- `backend/internal/modules/remediation/adapter/openai/client.go`
- `backend/internal/modules/remediation/adapter/openai/client_test.go`

Rollback point: all existing multi-turn coordinator tests and new incremental
request assertions pass.

## Step 3. Normalize Cache Usage And Exact Request Metrics

- [x] Extend provider-neutral model result metadata with request/tool/cache
      metrics and an explicit cache-reported flag.
- [x] Count serialized schema bytes during provider tool construction and exact
      request bytes after payload serialization.
- [x] Decode OpenAI nested cached tokens and compatible top-level hit/miss
      tokens with documented precedence and safe miss derivation.
- [x] Attach exact request/tool metrics to every outbound HTTP attempt and cache
      metrics when a response was decoded.
- [x] Add stable observability field constants and project metrics onto the
      INFO remediation model-turn record.
- [x] Test exact values, absent cache fields, zero-token reported fields,
      retries, redaction, and console/JSON projection.

Primary files:

- `backend/internal/modules/remediation/domain/types.go`
- `backend/internal/modules/remediation/adapter/openai/client.go`
- `backend/internal/modules/remediation/adapter/openai/client_test.go`
- `backend/internal/platform/observability/logging.go`
- `backend/internal/platform/observability/outbound.go`
- `backend/internal/platform/observability/outbound_test.go`
- `backend/internal/modules/remediation/adapter/logging/observer.go`
- `backend/internal/modules/remediation/adapter/logging/observer_test.go`

Rollback point: adapter and observability tests pass with no request payload
logging expansion.

## Step 4. Enforce Operation Admission And Log Exhaustion Reason

- [x] Change budget evaluation to return a deterministic exhausted dimension.
- [x] Remove elapsed from post-operation resource consumption and check it
      immediately before every model/tool operation.
- [x] Accept and process a successful in-flight model result even when elapsed
      crosses the run limit while waiting; stop only before a required next
      operation.
- [x] Keep provider timeout failures distinct from run elapsed admission in
      observations and tests; do not add a provider-timeout configuration in
      this task.
- [x] Carry the optional reason through `StateTransitionObservation`.
- [x] Log `outcome=stopped` and `budget_exhausted_reason` on exhaustion while
      retaining `success` for ordinary committed transitions.
- [x] Add focused budget, coordinator, and observer tests for every reason,
      slow successful terminal responses, and blocked next-operation paths.

Primary files:

- `backend/internal/modules/remediation/application/budget.go`
- `backend/internal/modules/remediation/application/budget_test.go`
- `backend/internal/modules/remediation/application/coordinator.go`
- `backend/internal/modules/remediation/application/coordinator_test.go`
- `backend/internal/modules/remediation/application/observer.go`
- `backend/internal/modules/remediation/adapter/logging/observer.go`
- `backend/internal/modules/remediation/adapter/logging/observer_test.go`

Rollback point: existing persisted counters and terminal states remain
unchanged; only reason/operation-admission behavior is additive.

## Step 5. Full Harness Proof And Quality Gate

- [x] Extend the existing multi-turn coordinator trace to cover deferred tool
      search, activation, a dynamic call/result, re-diagnosis, planning, and
      `diagnosis_ready_for_review`.
- [x] Assert provider-visible tool count/schema bytes change only after search
      activation and conversation growth is incremental.
- [x] Run focused remediation and observability tests.
- [x] Run the complete backend test suite and formatting/static checks defined
      by the repository.
- [x] Run GitNexus `detect_changes(scope="compare", base_ref="main")` before
      commit and review every affected flow.
- [x] Update backend remediation/logging specs with the landed contracts via
      `trellis-update-spec`.

Validation commands:

```bash
cd backend && gofmt -w <changed-go-files>
cd backend && go test ./internal/modules/remediation/... ./internal/platform/observability/...
cd backend && go test ./...
```

Final review must confirm:

- no catalog-wide schema byte limit was introduced;
- exact metrics are counts, not unbounded request bodies;
- policy/phase enforcement still gates discovery and execution;
- cache fields distinguish absent from reported zero;
- the working tree's unrelated existing changes were preserved.
