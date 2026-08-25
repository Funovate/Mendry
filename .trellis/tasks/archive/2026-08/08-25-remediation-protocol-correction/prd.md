# Harden remediation protocol correction

## Goal

Prevent a correct remediation diagnosis from consuming the full model-call
budget when its JSON envelope violates a repairable structural contract. The
model must receive a safe, actionable correction and the conversation must not
reinforce rejected output.

## Background

- Production run `9988e75b-b75e-447d-a04d-cf2885c8e896` called
  `docker.logs` successfully and then produced a substantively reasonable
  `insufficient_evidence` diagnosis.
- Diagnosis sequences 3 through 18 were all rejected with
  `json: unknown field "evidenceRef"`; the canonical citation field is
  `evidenceId`.
- The coordinator had the concrete decoder error but called
  `AppendProtocolError(phase)`, which replaced it with a generic correction
  about `diagnosis` and `fixability` that did not identify the invalid field.
- `AgentEngine` recorded each assistant response before envelope decoding, so
  every rejected response became provider-native history. Request size grew
  from 82,521 bytes to 205,477 bytes before history eviction.
- The run stopped after 17 provider calls with
  `budget_exhausted_reason=model_calls`; no decision was persisted.

## Requirements

### R1. Safe actionable protocol feedback

- The diagnosing prompt must state the canonical evidence-citation shape before
  the first diagnosis response: a citation may be a persisted evidence ID
  string or an object using `evidenceId` with optional `classification`.
- Repairable envelope failures must be normalized into a bounded,
  provider-neutral correction containing a stable error code and actionable
  schema guidance.
- For the observed citation mismatch, the next model turn must learn that
  `diagnosis.evidenceCitations[].evidenceRef` is invalid and `evidenceId` is
  the canonical field.
- Feedback must not include the raw model response, provider response body,
  credentials, arbitrary decoder text, Go type paths, or unbounded field
  values.
- Unknown or unclassifiable validation failures must retain a safe generic
  fallback.
- `evidenceId` remains the only canonical object field for an evidence
  citation. Do not accept `evidenceRef` as a compatibility alias: accepting
  model-invented aliases would hide protocol drift and teach the model an
  incorrect contract.

### R2. Conversation commit semantics

- Provider-native assistant content must enter accepted conversation history
  only after the envelope has decoded and validated successfully.
- A rejected assistant response must not be replayed as a successful prior
  answer or repeatedly enlarge the model-visible history.
- Native tool-call conversation groups must retain their existing valid
  assistant-tool/result ordering.
- Provider failures must leave pending continuation data available for the
  existing retry behavior.

### R3. Bounded protocol recovery

- A phase may observe at most three consecutive invalid envelopes, including
  the initial invalid response and two corrective attempts.
- The third consecutive invalid envelope must transition the run to
  `blocked_manual_review` without generating a decision from invalid content.
- Any valid envelope resets the consecutive protocol-failure count. A phase
  change also starts a fresh count for the new phase.
- All repairable envelope failures count toward the same consecutive limit;
  changing from one validation error to another must not bypass the bound.
- The existing run-wide model-call budget remains an independent hard ceiling
  and still accounts for every attempted correction.

### R4. Compatibility and observability

- Preserve strict envelope decoding, native tool calls, reasoning-output retry,
  provider timeout behavior, and run budget accounting.
- Operator logs must retain the concrete local diagnostic while model-visible
  feedback uses only the safe normalized form.
- No raw remediation payload or model reasoning may be added to terminal
  records, audit metadata, or non-debug logs.
- Work with the existing uncommitted remediation changes and do not overwrite
  unrelated edits.

## Acceptance Criteria

- [ ] A diagnosis using `evidenceRef` receives a bounded correction identifying
      `evidenceId` as the canonical replacement on the next logical turn.
- [ ] The first diagnosing turn exposes the canonical `evidenceCitations`
      shape without advertising `evidenceRef` or another alias.
- [ ] The rejected assistant response is absent from accepted provider-native
      history, while the safe correction and original pending context remain
      available.
- [ ] A corrected next response can be decoded, persisted, and routed normally.
- [ ] The third consecutive invalid envelope transitions to
      `blocked_manual_review` before the 16-call run budget is consumed and
      without growing history on every retry; a valid envelope before that
      point resets the counter and follows normal routing.
- [ ] Generic malformed JSON and unrelated validation failures remain
      fail-closed with safe fallback feedback.
- [ ] Native tool-call history and output-budget retry behavior remain covered
      by regression tests.
- [ ] Focused remediation application/domain tests and the backend quality gate
      pass.

## Out Of Scope

- Relaxing evidence authority, evidence-gate rules, or accepting
  model-invented citation aliases such as `evidenceRef`.
- Semantic conversation summarization or a redesign of provider prompt caching.
- Persisting raw model responses or decoder internals for later replay.
- Changing project-selected models or global remediation token/cost limits.
