# Implementation Plan: Remediation Reasoning Model Turns

## Step 1: Establish Provider-Neutral Result Contracts

- [x] Add a stable output-budget exhaustion sentinel to the remediation domain.
- [x] Add an additive provider-call count to `ModelResult`, with zero treated as
      legacy one-call behavior by consumers.
- [x] Add focused domain/application tests for default and aggregated call
      accounting.

## Step 2: Preserve OpenAI Response Metadata Before Validation

- [x] Move ordinary usage and finish-reason assignment ahead of content/tool
      validation in `Client.Complete`.
- [x] Return the typed exhaustion error for `finish_reason=length` plus blank
      content/tool calls; preserve the generic error for other empty responses.
- [x] Test preserved tokens, cache metrics, provider/model, request metrics, and
      finish reason on failed decoded responses.

## Step 3: Add One Reasoning Output Retry

- [x] Raise the initial `AgentEngine` completion budget from 4096 to 8192.
- [x] Within one OpenAI `Client.Complete`, retry exactly once at 16384 only for
      the typed output-exhaustion error, reusing the resolved session and shared
      logical-turn context.
- [x] Aggregate both calls' usage/cost/cache counters and return only the final
      content/tool calls for validation and history.
- [x] Test successful retry, second exhaustion, unrelated provider failure,
      unchanged request fields apart from `MaxTokens`, and shared deadline
      behavior across both output attempts.

## Step 4: Enforce Native History Aggregate Bytes

- [x] Group bounded native messages into complete user-led turns.
- [x] Retain the newest complete groups within both byte and item limits.
- [x] Test deterministic oldest-turn eviction, aggregate byte compliance,
      latest-turn retention, and no orphaned native tool messages.

## Step 5: Correct Accounting And Operator Metadata

- [x] Make `modelEffect` persist the real provider-call count while preserving
      legacy fake behavior.
- [x] Add `model_calls` and `finish_reason` to remediation model-turn completion
      logs without adding payload exposure.
- [x] Extend observer/coordinator tests for successful retry and exhausted retry
      accounting.

## Step 6: Verification

- [x] Run `gofmt` on changed Go files.
- [x] Run focused tests for remediation domain/application/OpenAI/logging.
- [x] Run the backend quality gate required by `.trellis/spec/backend/quality-guidelines.md`.
- [x] Run `git diff --check`.
- [x] Run GitNexus `detect_changes` and confirm only expected remediation model,
      conversation, accounting, and observability flows are affected.

## Risk And Rollback

- `Client.Complete`, `TurnObservedWithConversationAndTools`, `History`, and
  `modelEffect` are the primary risk symbols; run GitNexus impact analysis for
  each before editing.
- Preserve the user's existing uncommitted prompt changes in
  `application/agent_engine.go`.
- The change has no migration. A code revert restores previous behavior.
