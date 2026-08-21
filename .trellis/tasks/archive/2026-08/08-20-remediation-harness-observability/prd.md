# Remediation harness observability

## Goal

Make every remediation run operationally understandable while it is running and
after it finishes, without expanding credential access beyond the trusted Git,
SSH, and LLM adapters.

Operators must be able to answer which phase a run is in, what bounded action it
just attempted, how long it took, what it consumed, and why it stopped. Operations
and engineering staff with access to the backend console logs must additionally be
able to reconstruct model turns and tool calls from correlated, redacted payload
records.

## Background

- The coordinator persists lifecycle state, budget counters, decisions, plans,
  artifacts, and coarse tool invocation records, but normal `INFO` logs do not
  expose successful run progress.
- `remediation.failed` reports terminal failures with a wrapped cause chain.
- Successful `llm.request.completed` records are `DEBUG` only and contain at most
  a 4 KiB redacted request/response snapshot; this is not a relational agent trace.
- The remediation Git reader does not currently emit the existing
  `git.request.completed` event.
- Credentials are selected by stored secret reference and decrypted only inside
  trusted adapters. The model and coordinator must not receive plaintext tokens,
  passwords, private keys, or bearer authorization values.

## Requirements

- **R1: Structured progress events.** Emit stable `INFO` events for run start,
  state transition, initial context assembly, model-turn completion, tool-call
  completion, and terminal completion. Events identify `run_id`, `series_id` where
  available, `incident_id`, phase, outcome, and duration without storing secret
  values or raw payloads. The shared logger attaches `trace_id`/`span_id` when the
  current context contains a valid OpenTelemetry span.
- **R2: External dependency visibility.** Git, SSH evidence, and LLM adapter calls
  emit bounded structured completion records with operation identity, duration,
  outcome, and low-cardinality failure class. Successful high-volume dependency
  events may remain `DEBUG` when an `INFO` remediation progress event summarizes
  the action.
- **R3: Reconstructable console trace.** Emit ordered model-turn and tool-call
  records under the remediation run so backend console logs can reconstruct
  parent/child relationships, phase, timing, input, output, usage, and outcome.
  Payloads are bounded and redacted before they enter `slog`. Progress and
  metadata are `INFO`; prompt/model/tool payload records are `DEBUG`.
- **R4: Credential non-disclosure.** Plaintext tokens, passwords, SSH private keys,
  bearer authorization values, and authenticated Git URLs must never enter logs,
  console payload records, API responses, audit metadata, or model context. Console
  records may contain secret IDs, credential kind, transport, resolution outcome,
  and authentication outcome.
- **R5: Prompt and evidence traceability.** Full logical prompt/model/tool payloads
  may be emitted at `DEBUG` after deterministic secret redaction and size
  enforcement. This includes the redacted repository/evidence content actually
  read by an adapter, not only its coarse summary. Each payload field is capped at
  64 KiB of UTF-8-safe output. Records expose complete redacted-payload byte count,
  logged byte count, `payload_truncated`, and SHA-256 of the complete redacted
  payload so a partial payload is never presented as complete and the unredacted
  secret-bearing input is never fingerprinted.
- **R6: Console-only delivery.** This task does not add trace persistence, a trace
  query API, or a frontend trace viewer. Access control is the deployment's existing
  backend console/log transport boundary, intended for operations and engineering.
- **R7: Compatibility.** Existing stable event names, remediation lifecycle,
  failure reporting, review response fields, and adapter credential isolation
  continue to work. Existing APIs do not change.

## Acceptance Criteria

- [ ] A successful remediation run emits correlated start, phase/action progress,
  and terminal events at `INFO`; a failed run retains the detailed failure event.
- [ ] Logs distinguish time spent in repository preparation, evidence collection,
  each model turn, each tool call, and plan generation without logging payloads at
  `INFO`.
- [ ] Correlated backend console records reconstruct ordered, bounded, redacted
  model and tool spans for one run, including duration, outcome, token usage, and
  truncation flags.
- [ ] Every logged prompt, response, repository content, and evidence payload is
  at most 64 KiB, is truncated on a valid UTF-8 boundary, and includes complete
  redacted-payload byte count and SHA-256 metadata.
- [ ] Tests inject representative API tokens, passwords, authenticated URLs, PEM
  keys, and secret-like values into every console payload source and prove none
  appear in console or JSON logs.
- [ ] Secret references and non-sensitive credential metadata remain observable so
  authentication failures can be diagnosed without plaintext.
- [ ] Existing remediation, observability, adapter, and bootstrap test suites pass;
  no database migration, API contract, or frontend change is introduced.

## Out Of Scope

- Sending traces to a third-party vendor such as LangSmith in this task.
- Persisting prompt, model response, evidence, or tool payload Trace records in
  PostgreSQL or another application-owned store.
- Adding a Trace API, changing the remediation review response, or adding a
  frontend progress/Trace surface.
- Allowing the model to request, inspect, or return plaintext credentials.
- Logging trace payloads at `INFO`; payload visibility requires
  `FIXTHE_LOG_LEVEL=debug`.
- Implementing patch application, branch creation, publishing, or deployment.
