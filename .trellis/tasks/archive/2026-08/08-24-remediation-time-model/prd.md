# Adjust Remediation Time Model

## Goal

Allow long, cache-friendly remediation model turns to complete without being
terminated by the current fixed 30-second HTTP timeout, while keeping the
entire automatic remediation run bounded to at most 20 minutes.

## Background

- Run `ee81b390-7c3c-4a94-a303-5d6061fe0808` failed after a 237,186-byte
  model request received no response headers within 30 seconds on each of
  three attempts. The logical turn took 90.778 seconds and the run took
  206.129 seconds.
- Earlier requests in the same run succeeded and provider metrics proved the
  append-only native conversation prefix was cacheable. The last failed
  request returned no usage metadata, so its actual cache hit cannot be
  observed.
- `backend/internal/modules/remediation/adapter/openai/client.go` currently
  creates `http.Client{Timeout: 30 * time.Second}` and retries transient
  transport errors up to three total attempts.
- `backend/internal/modules/remediation/application/budget.go` currently uses
  a five-minute default elapsed budget. Elapsed time is admission control for
  the next operation rather than cancellation of an in-flight operation.
- Production bootstrap does not inject a custom OpenAI HTTP client, so the
  adapter's fixed default timeout is authoritative.

## Requirements

### R1. Run Deadline

- The automatic remediation run hard elapsed limit must be 20 minutes.
- The run work deadline must cover model calls, retry backoff, tool calls, and
  context collection in the run.
- No model or tool operation may start after the run deadline is exhausted.
- An in-flight operation must not continue beyond the run's remaining time.
- Terminal state persistence, observations, and connector cleanup may finish
  after work-budget expiry so the durable run cannot remain active.

### R2. Logical Model-Turn Deadline

- The default logical model-turn timeout must be five minutes.
- A logical model turn must have one bounded deadline shared by all HTTP
  attempts and retry backoff.
- Retries must not multiply the configured logical-turn timeout.
- The effective turn deadline must be the earlier of the model-turn limit and
  the run's remaining deadline.
- Provider timeout failures must remain distinguishable from run elapsed
  exhaustion in errors and structured observations.
- The model-turn duration must be configurable through the established API
  configuration layer, bounded to `30s..20m`, and documented in
  `.env.example`.

### R3. HTTP Attempt Semantics

- Preserve at most three total attempts for transient network failures and
  retryable HTTP statuses.
- Request creation, response-header wait, response-body read, and retry
  backoff must all honor the shared logical-turn context.
- Authentication, configuration, request validation, and response protocol
  errors must continue to fail without retry.
- Do not expose credentials, model request bodies, or provider response bodies
  through new errors or configuration logs.

### R4. Compatibility And Observability

- Preserve the existing append-only native conversation and provider-cache
  behavior; this task does not redesign conversation compaction.
- Preserve existing request, tool-schema, cache-hit, cache-miss, model usage,
  and per-attempt outbound metrics.
- Existing webhook normalization timeout behavior must remain independent from
  remediation model-turn timing.
- No database, REST API, or frontend contract change is required.

## Acceptance Criteria

- [ ] AC1: Default remediation run elapsed admission is 20 minutes.
- [ ] AC2: A slow model response that takes longer than 30 seconds but finishes
      within the logical-turn and run deadlines succeeds.
- [ ] AC3: All retries for one logical model turn share one deadline; total
      time cannot become `turn timeout x attempt count`.
- [ ] AC4: A model turn is canceled at the earlier of its configured deadline
      and the run's remaining deadline.
- [ ] AC5: Retryable failures still use at most three attempts with bounded
      backoff, while non-retryable failures make one attempt.
- [ ] AC6: Provider timeout and run elapsed exhaustion remain observably
      distinct.
- [ ] AC7: Configuration parsing, bounds, defaults, `.env.example`, focused
      adapter/coordinator tests, and the complete backend test suite pass.
- [ ] AC8: Webhook normalization keeps its existing independent timeout.

## Out Of Scope

- Changing prompt caching or native conversation construction.
- Adding conversation summarization or a new prompt token budget.
- Changing tool result byte limits.
- Changing the maximum number of OpenAI attempts.
- Database, REST API, or frontend changes.
