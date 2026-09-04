# Model-driven tool corrective retry

## Goal

Make remediation agents recover from tool failures deliberately instead of
immediately abandoning evidence collection. The model must receive a bounded,
structured recovery hint that distinguishes correctable request errors from
transient connector failures and terminal failures, while remaining responsible
for choosing corrected parameters or an alternate evidence path.

## Background

- Every built-in remediation tool request reaches
  `RemediationCoordinator.runTool`, which executes the request once and returns
  a safe `tool_observation` to the model. There is no application-level blind
  retry loop.
- The observation already contains the redacted request parameters plus
  `error.code`, `error.retryable`, and a safe message, but it does not state the
  expected recovery action or whether the same failure has already consumed its
  bounded corrective retry.
- `INC-2268` made one `docker.logs` request, received a retryable connector
  failure, and then abandoned Docker evidence collection. The run ended as
  `insufficient_evidence` even though a second model-selected attempt could
  have recovered a transient failure.
- Some adapters own narrower retries already: Streamable HTTP MCP transport,
  Tencent CLS eventual-consistency polling, and the OpenAI provider. Those
  retries must remain independent from model-driven corrective tool turns.

## Requirements

- R1. Failed remediation tool observations MUST classify the next safe action
  separately from the existing `retryable` transport indicator.
- R2. `invalid_arguments` and `path_out_of_scope` MUST tell the model to inspect
  the echoed sanitized parameters and tool schema, correct the request, and
  issue a new tool call rather than replaying unchanged parameters.
- R3. Transient connector failures (`connector_timeout`, `transport`,
  `rate_limit`, `remote_execution`, and retryable connector failures) MUST tell
  the model that one same-request or safely adjusted retry is available.
- R4. Authorization, authentication, cancellation, budget, persistence,
  unavailable capability, and other non-retryable failures MUST not recommend
  replaying the failed request; the model must use an available fallback or
  report the evidence gap.
- R5. Corrective retry state MUST be bounded per conversation and per
  `tool name + safe error code`. The first failed attempt may advertise one
  corrective retry; a repeated failure with the same key MUST advertise that
  no further retry is available.
- R6. The diagnosis prompt MUST explicitly require the model to inspect the
  structured recovery hint before finalizing. It MUST distinguish corrected
  parameter retry, transient retry, and fallback/stop behavior.
- R7. The coordinator MUST continue to execute only model-requested tools. It
  MUST NOT automatically replay a tool call or invent replacement parameters.
- R8. Every model-requested retry remains a normal tool call and consumes the
  existing run tool-call, elapsed-time, evidence-byte, and repository-byte
  budgets.
- R9. Existing adapter-local retry behavior, tool schemas, credential
  isolation, redaction, evidence persistence, and provider-native message
  pairing MUST remain unchanged.
- R10. Recovery metadata MUST contain only bounded safe enums, counters, and
  guidance. Raw connector errors, credentials, unrestricted output, and
  unredacted parameters MUST not cross into model context or persistence.

## Acceptance Criteria

- [x] AC1. A correctable policy error observation contains the safe error code,
      `recoveryAction=correct_request`, failure attempt `1`, and one remaining
      corrective retry.
- [x] AC2. A retryable connector error observation contains
      `recoveryAction=retry_transient` and one remaining retry without exposing
      the raw adapter error.
- [x] AC3. The second failure for the same tool/error key contains
      `recoveryAction=use_fallback` and zero remaining retries.
- [x] AC4. A non-retryable failure immediately contains
      `recoveryAction=use_fallback` and zero remaining retries.
- [x] AC5. A coordinator test proves a model can receive an invalid-argument
      observation, issue a corrected request, consume two tool calls, and
      continue diagnosis successfully.
- [x] AC6. A coordinator/conversation test proves a repeated transient failure
      is bounded and presented as fallback rather than inviting an infinite
      retry loop.
- [x] AC7. Prompt contract tests cover the corrective retry matrix and the rule
      that the model, not the coordinator, selects revised parameters.
- [x] AC8. Existing remediation application tests, race tests, backend-wide
      tests, `go vet`, command builds, and `git diff --check` pass.

## Out Of Scope

- Automatic coordinator replay with identical parameters.
- New retry configuration, database columns, migrations, or frontend controls.
- Changing MCP, Tencent CLS, OpenAI, SSH, Docker, Git, or evidence adapter-local
  retry schedules.
- Persisting raw tool error text or a full retry transcript outside existing
  bounded observations and tool-invocation metadata.

## Notes

- Parent context: `08-14-agentic-remediation-harness`.
- The retry allowance is one corrective model turn after the initial failure,
  keyed by tool name and safe error code. A different safe error code starts a
  distinct recovery decision because it represents a different failure class.
