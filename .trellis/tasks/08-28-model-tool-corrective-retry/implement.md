# Model-driven tool corrective retry implementation plan

## Checklist

- [x] Load backend remediation, error-handling, quality, and cross-layer specs.
- [x] Run GitNexus upstream impact for every edited function/method.
- [x] Extend `AgentConversation` with bounded per-tool/error failure counters.
- [x] Add safe recovery-action derivation without exposing raw adapter errors.
- [x] Enrich failed `tool_observation` JSON with action and counters.
- [x] Extend diagnosis prompt with the corrective retry decision matrix.
- [x] Add focused conversation tests for correctable, transient, repeated, and
      non-retryable failures.
- [x] Add coordinator coverage for a corrected model-issued tool request and
      accurate tool-call budget accounting.
- [x] Update prompt contract tests.
- [x] Run focused remediation tests, race tests, backend-wide tests, vet, builds,
      formatting/diff checks, and Trellis quality verification.
- [x] Run GitNexus `detect_changes` and review only expected execution flows.

## Validation Commands

```bash
cd backend
go test ./internal/modules/remediation/application
go test -race ./internal/modules/remediation/application
go test ./internal/modules/remediation/...
go test ./...
go vet ./...
go build ./cmd/...
git diff --check
```

## Risk And Rollback Points

- `conversation.go`: preserve safe error redaction and native tool-call pairing.
- `agent_engine.go`: keep protocol JSON and all existing evidence instructions
  intact while adding recovery guidance.
- `coordinator_test.go`: assert physical tool attempts are counted individually;
  do not weaken global budget behavior.
- Rollback is code-only; no migration or stored row rewrite is required.
