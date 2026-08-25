# Technical Design: Remediation Protocol Correction

## Scope And Boundaries

This task changes provider-neutral envelope diagnostics, conversation commit
timing, and coordinator recovery control. It does not relax the JSON contract,
change provider transport behavior, or persist model payloads.

The existing ownership boundaries remain:

- `AgentEngine` builds and validates one logical model turn.
- `AgentConversation` owns pending continuation and accepted provider-native
  history.
- `RemediationCoordinator` owns phase loops, run transitions, and retry limits.
- Evidence-domain types own the canonical evidence citation JSON contract.

GitNexus reports LOW upstream risk for the four primary symbols. The widest
symbol, `handleTurnError`, has two direct callers (`drive` and `plan`) and one
affected application process (`drive`).

## Data Flow

```text
model returns diagnosis with evidenceRef
  -> evidence citation decoder returns typed known-field violation
  -> AgentEngine logs the complete local error for operators
  -> rejected response is not committed to accepted conversation history
  -> coordinator accounts the model call
  -> safe correction mapper emits allowlisted evidenceId guidance
  -> correction plus still-pending context enters the next continuation
  -> corrected envelope succeeds, commits once, and resets the phase counter

third consecutive invalid envelope
  -> account the third model call
  -> do not append another correction
  -> transition active phase to blocked_manual_review
  -> emit the normal correlated terminal observations/notification
```

## D1. Typed Safe Corrections

Introduce a small provider-neutral `ProtocolCorrection` value containing only
bounded service-authored fields such as `code`, `path`, `expectedField`, and
`message`. `AgentConversation.AppendProtocolError` accepts this value rather
than deriving a generic message from phase alone.

The raw `error` remains available to `ModelTurnObservation.ErrorMessage` for
operator logging. A mapper converts only recognized typed violations into
specific model-visible corrections. Unknown failures use the current generic
phase correction. It must never copy `err.Error()` or a model-controlled field
name into conversation text.

For `EvidenceCitation`, inspect decoded object keys through `encoding/json`
before the existing strict struct decode. The exact known key `evidenceRef`
returns a typed domain violation that maps to the constant path
`diagnosis.evidenceCitations[].evidenceRef` and replacement `evidenceId`.
Other unknown keys remain strict failures but receive generic feedback. The
accepted forms remain:

- a non-empty persisted evidence ID string;
- an object with `evidenceId` or the already-supported legacy `id`, plus an
  optional authoritative classification hint.

No `evidenceRef` alias is added. The phase prompt also advertises the canonical
shape so a conforming model need not fail once to discover it.

## D2. Commit Accepted Conversation Only

Move `RecordModelTurn` out of the pre-decode position in
`TurnObservedWithConversationAndTools`:

- Native tool calls commit only after every call has passed protocol
  validation.
- Content commits only after `DecodeAgentEnvelope` succeeds.
- Mixed content/tool calls, invalid tool calls, and invalid envelopes do not
  commit assistant content or acknowledge prepared continuation.

This preserves bootstrap, tool observations, and protocol corrections as
pending input for a corrective turn. Successful turns retain the current
append-only provider-native history and complete native tool groups.

## D3. Three-Failure Phase Bound

Maintain a consecutive invalid-envelope counter in each active model phase.
The diagnosing and planning loops each start at zero. Accepted phase-valid
envelopes reset the counter; changing phase naturally creates a fresh counter.
All `ErrInvalidEnvelope` variants increment the same counter.

`handleTurnError` continues to persist model usage before deciding recovery, so
the run-wide model-call/cost budget remains authoritative. If the run budget is
exhausted while recording an invalid turn, `budget_exhausted` wins. Otherwise:

- failures 1 and 2 append one safe correction and retry;
- failure 3 transitions to `blocked_manual_review` and stops;
- provider/infrastructure errors retain the existing `failed` path.

An unexpected but decodable envelope kind in the current phase also counts as
an invalid envelope. A valid `requestTool`, `diagnosis`, `stop`, or
`planCandidates` in its owning phase resets the relevant counter.

## D4. Observability And Safety

Existing model-turn failure logs remain the source of the concrete decoder
diagnostic. Model-visible corrections contain only allowlisted service text.
The third failure is followed by the existing state-transition and terminal
events, making the stop reason reconstructable from the correlated final model
failure without adding raw payload fields or a database migration.

Tests must prove that decoder internals and arbitrary unknown field names do
not enter conversation text. The production `evidenceRef` case must prove that
the safe canonical replacement does enter it.

## D5. Compatibility And Rollback

- Strict JSON decoding and evidence authority remain unchanged.
- OpenAI `json_object`, native tool schemas, output-exhaustion retry, cache
  accounting, and timeouts remain unchanged.
- No database, API, or frontend contract changes are required.
- Rollback is code-only: restore generic corrections, pre-decode conversation
  recording, and run-budget-only protocol retries. No persisted data requires
  reversal.
