# Model-driven tool corrective retry design

## Architecture

The change stays inside the remediation application layer:

- `classifyToolError` remains the trust-boundary conversion from arbitrary
  adapter errors to stable safe codes and messages.
- `AgentConversation` owns per-conversation corrective-failure counters because
  it already owns bounded tool observations and provider-neutral history.
- `AgentEngine.buildPrompt` defines how the model reacts to recovery metadata.
- `RemediationCoordinator.runTool` remains a single-execution boundary. A retry
  happens only when the next model turn requests another tool call.

No adapter, domain port, database schema, HTTP contract, or frontend type
changes.

## Error Recovery Contract

Extend the model-visible safe tool error JSON with:

```json
{
  "code": "invalid_arguments",
  "retryable": false,
  "message": "tool arguments are invalid",
  "recoveryAction": "correct_request",
  "failureAttempt": 1,
  "retriesRemaining": 1
}
```

`recoveryAction` is one of:

| Action | Meaning |
|---|---|
| `correct_request` | Inspect sanitized parameters and schema, then issue corrected parameters. |
| `retry_transient` | Retry once with the same parameters or a safe adjustment when useful. |
| `use_fallback` | Do not replay this failure; use another tool or report missing evidence. |

Classification order:

1. `invalid_arguments`, `path_out_of_scope`, and safe not-found codes are
   correctable when this failure key has retry allowance.
2. Safe errors already classified `retryable=true` are transient when allowance
   remains.
3. All other errors use fallback immediately.
4. Repeating the same `tool + code` failure exhausts its single allowance and
   changes the action to fallback even if the transport indicator remains true.

The existing `retryable` field is retained for compatibility and preserves its
meaning: whether the underlying failure class is transient. It does not grant
unbounded retries and is not overloaded to mean parameter correction.

## Conversation State

Add a lazily initialized map to `AgentConversation`, keyed only by bounded tool
name and safe error code. The map stores the number of failed observations for
that key. It never stores parameter values, raw errors, credentials, or output.

On failure:

1. Classify the error through `classifyToolError`.
2. Increment the key's counter.
3. Derive recovery action and remaining count.
4. Marshal the enriched safe observation.

Success observations remain unchanged. A successful corrected call needs no
counter reset because there is no subsequent failure to recover; preserving the
count also prevents later oscillation on the same failure class within the run.

## Prompt Behavior

The diagnosis and collecting-context prompt instructs the model to:

- use `correct_request` to revise parameters rather than replay unchanged input;
- use `retry_transient` at most while `retriesRemaining > 0`;
- never replay when `use_fallback` or zero remaining is reported;
- after fallback, use other bounded evidence tools or explicitly preserve the
  missing evidence;
- not return an actionable diagnosis that relies on a failed, unpersisted tool
  result.

This remains model-driven: the coordinator never synthesizes a request.

## Budget, Observability, And Compatibility

- Each corrected/retried request passes through `runTool`, records another tool
  invocation, and consumes existing budget counters.
- Existing provider-native assistant/tool message pairing is unchanged because
  only tool result JSON gains safe fields.
- Existing clients that ignore additional observation fields remain compatible.
- No retry delay is introduced at this layer; model turns and the next tool call
  naturally separate attempts, while adapter-local backoff remains authoritative.
- Rollback removes the conversation counters, enriched fields, prompt clause,
  and tests. No persisted data needs reversal.

## Risks

- A prompt-only implementation could still be ignored by the model. Structured
  counters and explicit actions make behavior inspectable, but the coordinator
  intentionally does not force a replay because corrected parameters require
  semantic judgment.
- Keying only by tool and code may exhaust the allowance after unrelated bad
  parameters produce the same error. This is conservative and prevents loops;
  parameters are deliberately excluded from state to avoid a new sensitive-data
  retention boundary.
- Tool budget exhaustion remains possible when many distinct failures occur;
  the existing global budget is the final bound.
