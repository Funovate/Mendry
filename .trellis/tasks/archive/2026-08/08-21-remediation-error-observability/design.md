# Technical Design: Remediation Error Observability

## Scope

Improve the server-side diagnostic projection for three already-existing
failure boundaries:

1. SSH evidence command execution.
2. Built-in and dynamic remediation tool execution.
3. Provider/protocol/envelope failures during a model turn.

The change is additive to the observation and log contracts. It does not decide
whether the observed `type` field is valid, does not change strict envelope
decoding, and does not alter remediation state transitions.

## Current Information Flow

```text
SSH exec
  -> stderr discarded, generic error returned
  -> ssh.evidence.completed: error_class=internal

tool gateway
  -> safe error code/retryability computed
  -> ToolObservation keeps them in memory
  -> remediation.tool.completed: only error_class=adapter

model turn / envelope decode
  -> error returned to coordinator
  -> ModelTurnObservation keeps only failure_class
  -> remediation.model_turn.completed has no reason
  -> remediation.failed eventually contains the reason
```

The missing data is diagnostic metadata, not a missing domain decision. The
server log should receive the diagnostic at the boundary where it exists, while
the model conversation continues to receive only `safeToolError`.

## Proposed Data Flow

```text
SSH exec
  -> bounded diagnostic error (remote command + stderr + exit reason)
  -> ssh.evidence.completed: command + error_class + error_message

tool gateway
  -> ErrorCode + Retryable + ErrorMessage in ToolObservation
  -> remediation.tool.completed: error_class + error_code + retryable + error_message
  -> AgentConversation: unchanged safeToolError only

model turn / envelope decode
  -> ErrorMessage in ModelTurnObservation
  -> remediation.model_turn.completed: failure class + error_message
  -> remediation.failed: unchanged complete boundary report
```

## Contracts

### Application observations

Add `ErrorMessage string` to `ModelTurnObservation` and `ToolObservation`.
This is an internal, in-process observation field used by the logging adapter;
it is not persisted and is not serialized into model context.

Populate it from the actual error at each failure branch in
`AgentEngine.TurnObservedWithConversationAndTools` and both observed tool
gateway methods. Leave it empty on success.

### Stable log fields

Add observability constants:

- `error_code` for the existing safe tool error code.
- `retryable` for the existing safe retry decision.
- `error_message_truncated` when the operator diagnostic exceeds the log cap.

Reuse the existing `error_message` field for the bounded operator diagnostic.
The tool event emits `error_code` and `retryable` only on failure. The model and
SSH events emit `error_message` only on failure. Existing `error_class` remains
the low-cardinality query field.

All operator diagnostics are bounded to a fixed UTF-8-safe limit at the logging
projection. SSH stderr is also bounded before it is put into the returned error,
while retaining the actual remote command and actionable reason such as
`Permission denied (publickey)`.

### SSH adapter error

When `exec` returns a non-context command failure:

- retain the command exit reason;
- include the actual remote command and trimmed stderr when present;
- cap the resulting diagnostic before wrapping it.

Private key bytes are never passed to the command or included in the error
text. Context cancellation and timeout errors preserve
their current `context.Canceled` / `context.DeadlineExceeded` wrapping so
classification and retry behavior do not change.

### Logging adapter

`Observer.ToolCompleted` adds the safe code, retryability, and bounded
diagnostic. `Observer.ModelTurnCompleted` adds the bounded diagnostic. The SSH
adapter's `LogSSHEvidenceRequest` adds the bounded error message directly from
the completed request record.

No payload event is promoted from DEBUG to INFO/ERROR. This keeps model prompts,
tool arguments/results, and evidence bodies out of the normal failure line while
making the failure cause queryable.

## Compatibility

- Existing event names, success fields, success levels, and run correlation are
  unchanged.
- New fields are additive for JSON consumers. Console output renders them as
  `error_code=...`, `retryable=...`, and `error_message=...`.
- `AgentConversation.AppendToolResult` keeps the existing safe error object and
  never uses `ToolObservation.ErrorMessage`. Operator diagnostics are intended
  for internal logs and are not redacted at that projection boundary.
- The envelope schema and `DecodeEnvelope` remain untouched.
- Existing SSH callers still receive an error; only the diagnostic text is
  richer. Tests that assert remote details are hidden must be updated to assert
  the new bounded diagnostic contract instead.

## Verification Strategy

- SSH adapter test: a fake SSH process writes a concrete stderr reason and exits
  non-zero; assert the returned error and `ssh.evidence.completed` include the
  reason, actual command, and bounded output.
- Logging observer tests: construct failure observations and assert tool code,
  retryability, model reason, and truncation fields are present; success records
  do not gain failure fields.
- Application tests: assert a connector failure's safe model observation remains
  unchanged while the observer receives the operator diagnostic; assert model
  decode failure sends its wrapped reason to the observer.
- Run focused remediation/observability tests, then the repository Go quality
  gates and `go test ./...`.

## Rollback

Rollback is source-level and additive: remove the new fields and diagnostic
projection, leaving the prior generic error classes. No database, config, or
runtime migration is required.
