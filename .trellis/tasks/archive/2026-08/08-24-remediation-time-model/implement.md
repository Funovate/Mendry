# Implementation Plan: Remediation Time Model

## Step 1. Add Validated API Configuration

- [x] Add `FIXTHE_REMEDIATION_MODEL_TIMEOUT` and
      `config.Remediation.ModelTurnTimeout`.
- [x] Parse a `5m` default with `30s..20m` deployment bounds.
- [x] Cover default, override, below-minimum, above-maximum, and safe error
      behavior in config tests.
- [x] Document the setting in `backend/.env.example`.

Primary files:

- `backend/internal/platform/config/config.go`
- `backend/internal/platform/config/config_test.go`
- `backend/.env.example`

Rollback point: config tests pass before runtime behavior changes.

## Step 2. Make OpenAI Timeout Logical-Turn Scoped

- [x] Add a timeout option/client field with a five-minute adapter fallback.
- [x] Clone injected HTTP clients and remove their per-attempt total timeout.
- [x] Derive one timeout context around the complete logical turn, including
      config/secret loading, attempts, response reads, and retry backoff.
- [x] Preserve three-attempt retry/status behavior and per-attempt logging.
- [x] Return a stable safe logical-turn timeout cause that remains compatible
      with `context.DeadlineExceeded` classification.
- [x] Test slow success, shared-deadline exhaustion, earlier parent deadline,
      injected-client cloning, retry compatibility, and non-retryable errors.

Primary files:

- `backend/internal/modules/remediation/adapter/openai/client.go`
- `backend/internal/modules/remediation/adapter/openai/client_test.go`

Rollback point: OpenAI adapter and observability attempt tests pass.

## Step 3. Enforce The Run Work Ceiling

- [x] Change the default run elapsed budget to 20 minutes.
- [x] Add remaining-time/operation-context behavior to `runBudget` without
      using an expired context for state persistence.
- [x] Pass run-bounded contexts to model turns and tool executions.
- [x] Detect run-deadline expiry after each operation, persist its effect, and
      transition to `budget_exhausted reason=elapsed`.
- [x] Keep a logical model timeout within remaining run time classified as a
      provider failure.
- [x] Update elapsed-budget tests to cover in-flight model/tool cancellation,
      terminal persistence, and successful work within the limit.

Primary files:

- `backend/internal/modules/remediation/application/budget.go`
- `backend/internal/modules/remediation/application/budget_test.go`
- `backend/internal/modules/remediation/application/coordinator.go`
- `backend/internal/modules/remediation/application/coordinator_test.go`

Rollback point: coordinator state-machine and budget tests pass.

## Step 4. Wire Production And Preserve Webhook Timing

- [x] Pass the validated model-turn timeout through API bootstrap to the
      remediation OpenAI adapter.
- [x] Verify the webhook analyzer's existing parent timeout still wins when
      earlier than the remediation adapter timeout.
- [x] Add or update bootstrap tests for the new configuration wiring.

Primary files:

- `backend/internal/bootstrap/api.go`
- `backend/internal/bootstrap/bootstrap_test.go`
- focused hook tests only if existing coverage cannot prove parent precedence

Rollback point: bootstrap and webhook tests pass.

## Step 5. Quality Gate

- [x] Run `gofmt` on changed Go files.
- [x] Run focused config, remediation adapter, budget, coordinator, bootstrap,
      and hook tests.
- [x] Run `go test ./...` from `backend`.
- [x] Run repository-required static checks discovered by
      `trellis-before-dev` / backend quality guidelines.
- [x] Run GitNexus `detect_changes(scope="compare", base_ref="main")` and
      review every affected symbol and execution flow before commit.
- [x] Update backend specs with the landed timing contracts through
      `trellis-update-spec` during finish work.

Validation commands:

```bash
cd backend && go test ./internal/platform/config ./internal/modules/remediation/adapter/openai ./internal/modules/remediation/application ./internal/bootstrap ./internal/modules/hooks/...
cd backend && go test ./...
```

Final review must confirm:

- one logical model timeout cannot multiply by retry count;
- the run ceiling is 20 minutes and still persists a terminal state;
- model timeout and run elapsed exhaustion remain distinct;
- webhook normalization retains its independent timeout;
- prompt/cache behavior and sensitive-data boundaries are unchanged.

## Validation Record

- Passed focused config, remediation OpenAI adapter, remediation application,
  bootstrap, and hook tests.
- Passed `go vet ./...`, `go test ./...`, `go test -race ./...`,
  `go build ./cmd/...`, `make generate-check`, and `git diff --check`.
- GitNexus compare reported aggregate `critical` risk across 272 changes in the
  pre-existing dirty worktree. Targeted review confirmed this task's relevant
  affected flows are the expected API composition/config loading and
  remediation budget drive paths; unrelated hook, HTTP logging, frontend, and
  PostgreSQL changes remain outside this task.
