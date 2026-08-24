# Technical Design: Remediation Time Model

## Scope And Constraints

This task changes API configuration, the remediation OpenAI adapter, run
budget timing, coordinator operation contexts, bootstrap wiring, tests, and
deployment documentation. It does not change persistence, REST contracts,
frontend behavior, prompt construction, tool payload bounds, or the webhook
normalization timeout contract.

The timing model has two independent ceilings:

- Run work budget: 20 minutes for the complete automatic remediation run.
- Logical model turn: five minutes shared by every HTTP attempt and retry
  backoff belonging to one `LLMProviderPort.Complete` call.

The effective model deadline is always the earlier deadline. Terminal state
persistence, observations, and cleanup are allowed to finish after the work
budget expires so the durable run does not remain in an active state.

## D1. Configuration Contract

Add `FIXTHE_REMEDIATION_MODEL_TIMEOUT` to the API configuration layer.

```text
default: 5m
minimum: 30s
maximum: 20m
```

Add `Remediation.ModelTurnTimeout` to `config.API`. `LoadAPI` validates it with
the existing `durationValue` helper and names only the environment key in
errors. Bootstrap passes the validated value to the remediation OpenAI
adapter. `.env.example` documents the setting next to the existing webhook AI
timeout.

The webhook normalizer continues to apply
`WebhookAI.NormalizationTimeout` as its parent context. Because an earlier
parent deadline wins, its existing default remains independent even though it
shares the provider adapter.

## D2. Shared Logical-Turn Deadline

Extend the OpenAI adapter options with a logical-turn timeout and store the
normalized value on `Client`. `Complete` derives one timeout context before
configuration/secret resolution and request execution, then uses that same
context for every HTTP attempt and retry wait.

The default `http.Client` no longer has a 30-second total timeout. When a
caller injects an HTTP client, clone it and clear only `Client.Timeout` so the
adapter cannot regain a per-attempt total deadline. Preserve its transport,
redirect, cookie, and other behavior. Transport-specific dial/TLS/header
timeouts remain valid lower-level safety boundaries.

```text
caller context
  -> logical-turn context (5m default, parent may be earlier)
     -> attempt 1
     -> retry backoff
     -> attempt 2
     -> retry backoff
     -> attempt 3
```

There is no fresh timeout context per attempt. Fast transient failures may
still retry up to three total attempts. Once the shared context expires, no
further attempt or backoff begins.

Expose a stable adapter timeout cause that still unwraps to
`context.DeadlineExceeded`. Errors and outbound observations must distinguish
this logical provider timeout from caller cancellation. Existing safe error
wrapping remains unchanged for credentials and response bodies.

## D3. Run Work Deadline

Change `DefaultBudgetLimits().MaxElapsed` from five to 20 minutes. Custom
limits used by tests and future project policy remain supported.

Add a `runBudget` helper that returns the remaining duration and derives an
operation context ending at `startedAt + MaxElapsed`. The coordinator uses it
for each model turn and each model-requested tool execution. A five-minute
adapter context created below a run operation context is automatically
clamped to the earlier run deadline.

The coordinator retains its uncanceled lifecycle context for durable state
transitions. It inspects the operation context cause before canceling it:

- Run deadline expired: account any completed effect, then transition to
  `budget_exhausted` with reason `elapsed`.
- Model-turn deadline expired while run time remains: record the model call and
  fail the run as a provider timeout.
- Caller canceled: preserve existing cancellation/failure behavior.
- Operation completed: continue existing state-machine routing.

Tool connector timeouts remain adapter-owned when shorter than the remaining
run duration. The run operation context supplies the upper bound when a tool's
own timeout is longer.

## D4. Accounting And Observability

One logical `Complete` call continues to count as one model call regardless of
its HTTP attempt count. Per-attempt `llm.request.completed` records remain
unchanged and include request/tool/cache metrics when available.

A failed turn has no provider usage tokens unless the provider returned them,
but its model-call effect is still persisted. Run deadline expiry is reported
as `budget_exhausted reason=elapsed`; logical model timeout remains a failed
provider turn. This preserves the existing distinction between budget control
and outbound dependency failure.

No complete prompts, tool outputs, API keys, or response bodies gain a new log
surface.

## D5. Compatibility

- Existing append-only conversation and cache-friendly prefix ordering remain
  unchanged.
- Existing maximum of three total HTTP attempts remains unchanged.
- Existing retryable status and transport classifications remain unchanged.
- Existing custom `BudgetLimits` constructors remain available for tests.
- No database migration, stored configuration row, REST response, or frontend
  change is required.

## D6. Validation And Rollback

Tests will prove configuration defaults/bounds, slow success beyond the old
30-second behavior using short injected test durations, one shared retry
deadline, parent deadline precedence, three-attempt compatibility, 20-minute
run defaults, run-deadline terminal classification, tool cancellation, and
webhook timeout independence.

Rollback is file-local and contains no data migration. Reverting the config
field/wiring, adapter shared context, and coordinator operation context restores
the previous timing behavior.
