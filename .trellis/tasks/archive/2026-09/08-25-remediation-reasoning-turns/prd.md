# Harden remediation reasoning model turns

## Goal

Prevent OpenAI-compatible reasoning models from failing a remediation run when
their internal reasoning consumes the configured completion budget before they
emit final envelope content or a native tool call.

## Background

- Production run `593c1541-2411-40e5-907b-e9237969a9c8` used
  `deepseek-v4-flash` and failed in `diagnosing` after four model calls and ten
  tool calls.
- The final provider response was HTTP 200 with `finish_reason=length`, empty
  `message.content`, no `tool_calls`, and populated `reasoning_content`.
- `AgentEngine` currently requests `max_tokens=4096`. The preceding successful
  turn used 3,926 completion tokens, leaving little safety margin for the next
  reasoning turn.
- Provider request size grew from 6.7 KiB to 137 KiB as native tool results and
  assistant turns accumulated. A `docker.logs` result contributed about 77 KiB
  before model-visible bounding.
- `AgentConversation` bounds each message/observation and message count, but
  `History()` does not enforce its declared aggregate byte bound.
- The OpenAI adapter applies cache usage before response validation but assigns
  ordinary usage and `FinishReason` only after content/tool-call validation.
  The failed fourth call was therefore counted as one model call with zero
  tokens and an empty finish reason; the run total of 77,264 tokens omitted the
  failed provider usage.
- Full deterministic conversation compaction remains owned by the existing
  `08-21-remediation-tool-driven-context` task. This task must not duplicate
  that broader design.

## Requirements

### R1. Reasoning-model completion headroom

- Remediation diagnosis and planning turns must provide enough completion
  headroom for OpenAI-compatible reasoning models to emit a final envelope or
  native tool call after reasoning.
- Use `8192` completion tokens for the initial call. If and only if the provider
  reports output-budget exhaustion, retry the same logical turn once with
  `16384` completion tokens.
- The retry must reuse the same conversation and continuation, remain under the
  existing logical-turn and run deadlines, and never recurse or retry more than
  once.
- The bound must remain explicit, deterministic, and covered by a request test.

### R2. Output-exhaustion classification and accounting

- Parse and retain provider usage and `finish_reason` before validating whether
  content or tool calls are present.
- An empty response terminated by `finish_reason=length` must be classified as
  stable, distinguishable output-budget exhaustion rather than the generic
  no-content error; if the single retry also exhausts output, that typed error
  is returned to the application.
- Failed turns must preserve input/output/total token usage, provider/model
  identity, request metrics, cache metrics, and finish reason for budget
  accounting and operator logs.
- A successful retry must aggregate token/cost usage and record two provider
  model calls while exposing only the final successful content/tool calls to
  conversation history.
- Existing invalid empty responses with another finish reason must continue to
  fail closed as provider protocol errors.

### R3. Provider-native conversation byte bound

- `AgentConversation.History()` must enforce an aggregate model-visible byte
  bound in addition to per-message and item-count bounds.
- Truncation must retain complete user/assistant/tool-call groups: no orphaned
  tool result and no assistant native tool call without all of its tool results.
- Retain the latest complete turns deterministically. Do not implement semantic
  summarization or replace the broader compaction work owned by the existing
  tool-driven-context task.

### R4. Compatibility and safety

- Preserve strict JSON envelope compatibility and native tool-call mapping.
- Do not retry authentication, HTTP 4xx, malformed JSON, or ordinary invalid
  provider responses.
- Do not expose provider response bodies, reasoning text, credentials, or raw
  evidence in terminal remediation failure records.
- Work with the existing uncommitted `agentSystemPrompt` changes and do not
  overwrite unrelated user edits.

## Acceptance Criteria

- [ ] Agent turns send the agreed increased completion budget and tests assert
      the serialized `max_tokens` value.
- [ ] A 200 response with `finish_reason=length`, empty content, no tool calls,
      and non-zero usage is classified as output exhaustion; after the bounded
      retry, all response metrics remain preserved in `ModelResult`, including
      on terminal exhaustion.
- [ ] The coordinator charges preserved failed-turn tokens to run budget and
      model-turn logs expose the finish reason needed to diagnose truncation.
- [ ] Generic empty responses remain fail-closed and are not mislabeled as
      output exhaustion.
- [ ] Provider-native history stays within its aggregate byte bound without
      orphaning native tool messages; latest complete turns remain available.
- [ ] Focused remediation application/adapter tests and the project backend
      quality gate pass.

## Out Of Scope

- Semantic summarization, evidence ranking, or full deterministic conversation
  compaction from `08-21-remediation-tool-driven-context`.
- Switching the remediation harness to the Responses API.
- Provider-specific parsing or persistence of `reasoning_content`.
- Changing project-selected models, pricing policy, or global run budgets.
