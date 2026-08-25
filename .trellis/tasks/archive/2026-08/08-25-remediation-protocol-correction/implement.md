# Implementation Plan: Remediation Protocol Correction

## Step 1: Define Safe Protocol Correction Contracts

- [x] Add a bounded provider-neutral correction value in the remediation
      application layer.
- [x] Add a typed evidence-citation violation for the known `evidenceRef`
      mismatch without accepting it as input.
- [x] Map only recognized typed violations to field-specific feedback and keep
      a generic phase fallback for all other errors.
- [x] Test canonical feedback, strict rejection, bounded values, and absence of
      decoder internals/arbitrary model-controlled field names.

## Step 2: Advertise The Canonical Citation Shape

- [x] Extend the diagnosing prompt with the accepted evidence-citation string
      and `{evidenceId, classification?}` object forms.
- [x] Add a prompt contract test proving no `evidenceRef` alias is advertised.

## Step 3: Commit Conversation Only After Validation

- [x] Move content history commit after successful envelope decode.
- [x] Move native tool-call history commit after all calls validate.
- [x] Preserve pending bootstrap/tool/protocol observations after rejected
      output and acknowledge them exactly once after a corrected success.
- [x] Extend conversation and AgentEngine tests for rejected content, corrected
      content, valid native tools, and invalid/mixed native tool responses.

## Step 4: Bound Consecutive Protocol Failures

- [x] Track consecutive invalid envelopes separately in diagnosing and
      planning.
- [x] Reset on a phase-valid envelope and count all invalid-envelope variants
      toward one limit.
- [x] After the third failure, account usage and transition to
      `blocked_manual_review`; preserve run-budget precedence and provider-error
      failure behavior.
- [x] Add coordinator tests for correction success, three-failure termination,
      changing error kinds, reset behavior, planning isolation, and model-call
      budget precedence.

## Step 5: Production Regression

- [x] Reproduce `evidenceRef` in a scripted diagnosis after a successful native
      tool result.
- [x] Assert the next request contains `evidenceId` guidance, omits the rejected
      assistant response, retains required tool context, and can persist a
      corrected `insufficient_evidence` decision.
- [x] Assert request history does not grow once per rejected assistant payload.

## Step 6: Verification

- [x] Run `gofmt` on changed Go files.
- [x] Run focused remediation domain/application tests.
- [ ] Run the backend quality gate from
      `.trellis/spec/backend/quality-guidelines.md`.
- [x] Run `git diff --check`.
- [x] Run GitNexus `detect_changes`; the report includes the pre-existing dirty
      worktree's cross-module changes, while the task-specific remediation
      impact is limited to the planned conversation/coordinator/envelope flows.

`make generate-check` remains pending because the environment could not verify
the pinned Go toolchain through `sum.golang.org` (DNS/socket restriction first,
then an outbound timeout with escalation). The Go quality commands that do not
depend on that download passed: `go vet ./...`, full `go test ./...`, full
`go test -race ./...`, and `go build ./cmd/...`.

## Risk And Rollback

- Primary risk is acknowledging pending continuation at the wrong time and
  either replaying tool results or losing them. Native tool ordering and
  provider-failure retry tests are mandatory rollback gates.
- A blocked transition must occur only for three consecutive
  `ErrInvalidEnvelope` results; provider errors and global budget exhaustion
  retain their existing terminal semantics.
- No migration is involved. Reverting the application/domain changes restores
  the old behavior.
