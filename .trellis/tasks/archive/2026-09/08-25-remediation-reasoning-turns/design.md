# Technical Design: Remediation Reasoning Model Turns

## Scope And Constraints

This task changes the provider-neutral remediation model result/error contract,
the OpenAI-compatible response decoder and retry orchestration, the
`AgentEngine` completion bound, provider-native conversation history selection,
coordinator accounting, and model-turn logging. It does not change database schemas, REST/frontend
contracts, selected project models, global run limits, or semantic conversation
compaction.

The existing authority boundaries remain:

- `RemediationCoordinator` owns run state and persisted budgets.
- `AgentEngine` owns one logical phase turn and envelope validation.
- `LLMProviderPort` adapters own provider serialization and decoding.
- `AgentConversation` owns bounded provider-neutral history.

## Data Flow

```text
AgentEngine builds one logical turn (max_tokens=8192)
  -> LLMProviderPort.Complete creates one shared logical-turn context
     -> OpenAI adapter decodes usage + finish reason before content validation
     -> finish_reason=length with no content/tool call
     -> adapter retries the unchanged request once (max_tokens=16384)
        inside the same context
     -> aggregate both provider-call counters and usage
  -> AgentEngine validates only the final content/tool calls
  -> coordinator persists one logical state transition carrying two model calls
```

Before serialization, `AgentConversation.History()` groups native messages into
complete turns beginning with a user message. It copies and bounds message
content, then retains the newest complete groups that satisfy both the existing
item count and the 256 KiB aggregate encoded-byte limit.

## D1. Provider-Neutral Exhaustion Contract

Add a domain sentinel `ErrModelOutputExhausted`. The OpenAI adapter classifies a
decoded response with this sentinel only when the first choice has:

- `finish_reason == "length"`;
- blank `message.content`;
- zero valid native tool calls.

Other blank responses retain the existing generic protocol error. The adapter
must populate response metadata immediately after JSON/choice decoding and
before content/tool-call validation:

- provider/model and provider-call count;
- prompt, completion, and total usage;
- cache hit/miss usage;
- finish reason;
- request/tool/schema metrics.

This keeps failed-turn accounting accurate without retaining or exposing
provider `reasoning_content`.

## D2. One Bounded Logical-Turn Retry

`AgentEngine` starts with `8192` completion tokens. Within the same
`LLMProviderPort.Complete` call and its single derived timeout context, the
OpenAI adapter retries exactly once at `16384` when decoding returns
`domain.ErrModelOutputExhausted`.

The retry rebuilds only the serialized payload's `max_tokens`; it reuses the
same resolved session, `SystemPrompt`, `UserMessage`, `Messages`, `Tools`, and
`Continuation`. The existing logical model timeout and earlier run work
deadline cap configuration lookup, both output attempts, every transport retry,
response reads, and all backoff together. No conversation state is acknowledged
between attempts.

Introduce an additive provider-call counter on `ModelResult`. Adapters return
one for an issued request; legacy fakes/results that leave it zero retain the
existing default of one. A small aggregation helper sums:

- provider calls;
- input/output/total tokens and cost;
- cache hit/miss tokens, preserving the reported flag.

The final attempt owns content, tool calls, finish reason, and current request
shape metrics. Provider/model identity falls back to the first attempt only if
the final result omits it. If the retry also exhausts output, the aggregated
result and typed exhaustion error are returned. Authentication, transport,
timeout, malformed JSON, ordinary empty responses, and envelope failures are
never retried by this policy.

## D3. Budget And Observability

`modelEffect` uses the additive provider-call count, defaulting to one for
legacy results. It continues to normalize legacy total-only token usage.

`remediation.model_turn.completed` adds model-call count and finish reason.
Failed output-exhaustion turns therefore show their real tokens and
`finish_reason=length`; successful retries show `model_calls=2` and the final
finish reason. Per-request `llm.request.completed` remains the authoritative
record for each concrete HTTP request and response.

No new payload fields contain prompts, response bodies, reasoning text, or raw
evidence.

## D4. Native History Aggregate Bound

`AgentConversation.History()` must enforce bounds over encoded provider-neutral
messages, not only content strings:

1. Copy messages and apply the existing per-message text bound.
2. Partition them into groups beginning at each `user` message.
3. Discard malformed leading non-user messages.
4. Walk groups newest-to-oldest and retain complete groups while both the
   aggregate encoded-byte limit and item count allow them.
5. Return retained groups in chronological order.

A group containing assistant native tool calls is retained only with all of
its following tool results. The newest single complete group is retained only
when it fits the aggregate limit; producer-side per-observation bounds make
normal groups fit. Oversized malformed groups are omitted rather than emitting
an invalid partial tool protocol.

This is structural truncation only. Semantic checkpoints, hashes, relevance
selection, and summarization remain in the existing tool-driven-context task.

## D5. Compatibility, Validation, And Rollback

- Existing strict JSON envelope and native tool-call paths remain unchanged.
- Existing transport/status retry behavior remains separate from the single
  output-exhaustion retry; all concrete attempts share one logical-turn
  deadline.
- No migration or rollout sequencing is required.
- Unit tests cover adapter metadata preservation, typed exhaustion, one retry,
  no retry for unrelated errors, aggregated accounting, and history group/byte
  bounds.
- Rollback is code-only: restore the former output bound and remove the additive
  retry/error/accounting fields. No persisted data requires reversal.
