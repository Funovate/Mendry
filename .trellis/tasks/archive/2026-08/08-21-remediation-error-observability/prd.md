# Improve Remediation Harness Error Observability

## Goal

Make an internal operator able to identify the actual failing boundary of a
remediation run from the normal harness logs, without correlating an opaque
`error=internal` or `error=adapter` to source code manually.

The diagnostic surface is an internal server log. It may include the original
error message and bounded SSH command stderr. Model-visible tool observations
remain a separate contract and must continue to receive safe, typed error
messages rather than adapter diagnostics.

## Confirmed Facts

- `backend/internal/modules/remediation/adapter/sshlog/reader.go:208-245`
  captures stderr but discards it, returning only `read ssh log: remote command
  failed` for a failed SSH command. The completed SSH event at
  `backend/internal/platform/observability/ssh.go:21-51` then classifies that
  unknown error as `internal` through `ClassifyOutbound`.
- `backend/internal/modules/remediation/application/tool_gateway.go:90-107`
  and `tool_gateway_dynamic.go:30-50` set `FailureClass` to the generic
  `adapter`. They compute `ErrorCode` and `Retryable`, but
  `backend/internal/modules/remediation/adapter/logging/observer.go:94-115`
  does not emit either field or the diagnostic cause.
- `backend/internal/modules/remediation/application/agent_engine.go:104-155`
  records only `FailureClass=provider|protocol|decode` for model failures and
  returns the wrapped error to the coordinator. The strict decoder at
  `backend/internal/modules/remediation/application/envelope.go:135-207`
  rejects unknown fields, so the observed `json: unknown field "type"` is a
  protocol rejection rather than an adapter transport failure.
- `backend/internal/modules/remediation/application/coordinator.go:398-409`
  fails the run after a model-turn error, while the boundary reporter at
  `backend/internal/modules/remediation/adapter/logging/reporter.go:32-55`
  emits the complete wrapped error only at the end. The earlier model event
  therefore lacks the failure reason even though the final event has it.
- Existing tool-loop tests prove that adapter diagnostics must not be copied
  into the next model turn (`coordinator_test.go:249-268`), so operator logs and
  model context must remain separate.

## Requirements

### R1. Preserve Root Cause At The Adapter Boundary

- SSH command failures must retain a diagnostic error containing the remote
  command failure reason and bounded stderr for the internal log boundary.
- The SSH completed event must expose an actionable failure class/reason and
  preserve the existing host, port, operation, duration, byte count, and run
  correlation fields.
- SSH diagnostics must include the actual remote command and bounded stderr so
  an internal operator can reproduce the failing boundary. Private key bytes
  are never part of the command or stderr captured from the SSH process.

### R2. Make Tool Failure Events Actionable

- `remediation.tool.completed` must emit the stable safe error code,
  retryability, and an operator-facing diagnostic reason when the tool fails.
- Policy rejection must remain distinguishable from adapter execution failure.
- The operator diagnostic must be emitted only by the server-side observer; the
  conversation and model-visible observation must continue to use the safe
  `code`, `retryable`, and generic safe `message` contract.

### R3. Make Model Protocol Failures Actionable

- `remediation.model.completed` must include the failure reason for provider,
  protocol, and envelope decode failures, including the failing JSON field or
  validation rule when available.
- The event must retain phase, sequence, model, token counts, and run
  correlation, so a model failure can be traced to the exact turn without
  waiting for `remediation.failed`.
- The final `remediation.failed` event remains the complete boundary error and
  must stay consistent with the earlier model event.

### R4. Keep Protocol Behavior Unchanged

- Do not decide whether the observed `type` field is a model envelope error or
  a provider-native compatibility issue in this task.
- Preserve strict envelope decoding and existing provider normalization. This
  task only makes the current failure reason visible at the model-turn log
  boundary.

### R5. Stable, Testable Diagnostics

- Add focused tests for SSH stderr propagation/diagnostic boundaries, tool event
  fields, and model failure event fields using a generic decode error fixture.
- Preserve stable event names and existing success-path log levels.
- Keep diagnostics bounded and avoid logging model prompts, tool payloads, or
  evidence bodies as part of this change. The concrete `type` response is a
  follow-up investigation once the improved logs capture enough evidence.

## Acceptance Criteria

- [x] A failed SSH search produces a log record that states the operation,
      classification, actual remote command, actionable error reason, and
      bounded remote stderr; the record remains correlated to the same run/trace
      and contains no private key bytes.
- [x] A failed `evidence.search` tool event shows its safe error code and
      retryability in addition to the operator diagnostic, and clearly labels
      the failure as adapter execution rather than policy rejection.
- [x] A failed model turn produces a `remediation.model.completed` error record
      that identifies `decode`/`protocol`/`provider` and includes the concrete
      reason such as `unknown field "type"`; the run still transitions to
      `failed` and the final failure report remains available.
- [x] Protocol behavior, including strict unknown-field rejection, is unchanged
      and no compatibility behavior is added based on the incomplete evidence.
- [x] Existing tests for safe model-visible connector errors continue to pass,
      and no raw adapter diagnostic appears in a subsequent model turn.
- [x] Focused remediation and observability tests, `go test ./...`, and the
      required backend quality checks pass.

## Scope Boundaries

- This task changes diagnostics only; it does not redesign the remediation state
  machine, envelope protocol, tool policy, persistence schema, or frontend
  review UI.
- Internal visibility does not justify unbounded stderr, payload, or prompt
  logging. Only the failure reason and bounded command diagnostic needed to
  explain the failure are in scope.

## Follow-Up Evidence For Protocol Investigation

- The complete `llm.request.completed` record or raw assistant response for the
  failed turn, including whether `type` was top-level or nested.
- The same run's model-turn, tool-call, and terminal failure records with full
  trace/run correlation.
- The configured provider response mode and whether the failing call used
  provider-native tools or the JSON `requestTool` fallback.
- A reproducible response fixture or a captured response hash that can be
  converted into a regression test without guessing.

These facts are explicitly out of scope for the current implementation and are
needed for a later protocol investigation task or follow-up iteration.
