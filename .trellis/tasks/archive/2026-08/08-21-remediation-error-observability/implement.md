# Implementation Plan: Remediation Error Observability

## Preconditions

- Keep the existing worktree changes intact; inspect overlapping diffs before
  editing any file.
- Run GitNexus upstream impact analysis for every existing function or method
  that will be changed. Warn if any target reports HIGH or CRITICAL risk.
- Load the backend `trellis-before-dev` guidance before source edits.
- Do not modify envelope parsing or implement a `type` compatibility path.

## Ordered Checklist

### 1. Freeze and inspect current behavior

- [x] Run the focused remediation, SSH, logging, and observability tests before
      editing; record the baseline result.
- [x] Inspect current uncommitted diffs for all planned files.
- [x] Run GitNexus impact for `Reader.exec`, `LogSSHEvidenceRequest`,
      `ToolGateway.ExecuteToolObserved`,
      `ToolGateway.ExecuteToolObservedWithCatalog`,
      `AgentEngine.TurnObservedWithConversationAndTools`,
      `Observer.ToolCompleted`, and `Observer.ModelTurnCompleted`.

### 2. Add bounded diagnostic primitives

- [x] Add stable observability constants for `error_code`, `retryable`, and
      `error_message_truncated`.
- [x] Add the shared bounded diagnostic projection used by SSH and remediation
      logging, preserving UTF-8 boundaries and an explicit truncation marker.
- [x] Add additive `ErrorMessage` fields to the application observations.

### 3. Preserve SSH command diagnostics

- [x] Update `Reader.exec` to retain a bounded stderr/exit diagnostic and the
      actual remote command on non-context command failures.
- [x] Keep cancellation, timeout, credential, and command construction behavior
      unchanged.
- [x] Update SSH tests for actionable failure text, actual command visibility,
      and bounded output.
- [x] Extend the SSH observability assertion to cover `error_message` and the
      truncation marker where applicable.

### 4. Project tool failure details

- [x] Populate `ToolObservation.ErrorMessage` in both observed gateway methods
      from the adapter/rejection error while preserving safe classification.
- [x] Extend `Observer.ToolCompleted` with `error_code`, `retryable`, and a
      bounded `error_message` on failure.
- [x] Keep policy rejection codes and adapter failure classes distinguishable.
- [x] Add observer/application tests proving operator logs receive diagnostics
      while the next model turn contains only the safe error object.

### 5. Project model-turn failure details

- [x] Populate `ModelTurnObservation.ErrorMessage` on provider, native protocol,
      and envelope decode failures.
- [x] Extend `Observer.ModelTurnCompleted` with the bounded error message and
      truncation marker.
- [x] Add a decode failure test using a generic unknown-field fixture; do not
      change strict decoding or infer behavior for the observed `type` field.
- [x] Assert the final `remediation.failed` behavior and run state remain
      unchanged.

### 6. Quality verification

- [x] Run `gofmt` on changed Go files.
- [x] Run focused tests for remediation application, logging, SSH adapter, and
      observability.
- [x] Run `go test ./...` from `backend`.
- [x] Run required backend lint/vet/build checks from the project Makefile or
      quality guidelines.
- [x] Run `git diff --check`.
- [x] Run GitNexus `detect_changes(scope: "all")` before commit and inspect
      affected symbols/processes. Do not commit in this task unless separately
      requested.

## Risk Gates

- Do not log tool parameters, tool result payloads, model prompts, or evidence
  bodies as part of this change.
- Do not pass operator `ErrorMessage` into `AgentConversation` or any provider
  request.
- Do not change the strict envelope decoder, provider response normalization,
  retry classification, or remediation state transitions.
- If an existing test conflicts with the new explicit diagnostic contract,
  update only that test's expected diagnostic behavior after confirming the
  production boundary remains scoped and bounded.
