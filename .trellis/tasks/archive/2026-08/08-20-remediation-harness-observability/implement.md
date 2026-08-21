# Remediation Harness Observability Implementation Plan

## 1. Stable Observability Contract

- Add remediation progress/payload and SSH completion event constants and typed
  field constants in `internal/platform/observability`.
- Add the 64 KiB remediation payload snapshot helper with deterministic structured
  serialization, complete-payload redaction, redacted SHA-256, byte counts, and
  UTF-8-safe truncation.
- Extend JSON tests for stable fields and console tests for atomic labeled payload
  blocks without changing existing LLM/Git aliases, stack, SQL, or HTTP-debug
  projections.

Validation:

```bash
go test ./internal/platform/observability
```

Rollback point: observability-only changes compile and existing logging tests pass.

## 2. Application Observation Port

- Define typed, credential-free remediation observation records and a no-op
  implementation in the application package.
- Add observer-aware internal constructors while preserving existing exported
  constructor behavior for tests and frozen ports.
- Instrument coordinator run start/completion and successful state transitions.
- Instrument context assembly, model turns, and tool calls with phase, sequence,
  duration, outcome, usage/byte counters, and safe payload values.
- Keep persisted remediation behavior and budget accounting unchanged.

Validation:

```bash
go test ./internal/modules/remediation/application
```

Rollback point: application tests prove event ordering and no lifecycle changes.

## 3. Logging Adapter And Runtime Wiring

- Implement the observation port in `modules/remediation/adapter/logging`.
- Map typed observations to the stable INFO and DEBUG event contracts.
- Construct the observer in bootstrap and inject it through the observer-aware
  runtime coordinator constructor.
- Add adapter and bootstrap tests proving no-op compatibility and runtime wiring.

Validation:

```bash
go test ./internal/modules/remediation/adapter/logging ./internal/bootstrap
```

Rollback point: a runtime run produces correlated progress logs while existing
failure reporting is unchanged.

## 4. Git And SSH Dependency Visibility

- Add optional logger injection to remediation Git and SSH reader options.
- Instrument Git clone/fetch with public remote identity and the existing safe Git
  completion helper; keep authenticated targets and exec environment private.
- Add metadata-only SSH completion logging for bounded evidence reads; raw output
  remains exclusively in the remediation payload event after redaction.
- Wire the logger from API bootstrap and add success/failure/timeout credential
  isolation tests.

Validation:

```bash
go test ./internal/modules/remediation/adapter/git ./internal/modules/remediation/adapter/sshlog
```

Rollback point: removing logger injection returns adapters to prior no-op logging
without behavior or configuration changes.

## 5. Security And Integration Verification

- Add end-to-end logger capture tests for a successful run, a tool rejection, a
  provider failure, and a terminal non-code/code-fixable outcome.
- Inject API tokens, password assignments, authenticated Git URLs, PEM keys,
  Authorization values, multibyte oversized payloads, and secret references.
- Assert plaintext credentials are absent from console and JSON output; assert
  allowed credential metadata, payload hashes, byte counts, and truncation flags.
- Verify default `INFO` contains progress but no payload; `DEBUG` contains bounded
  redacted payload blocks.

Targeted validation:

```bash
go test ./internal/platform/observability ./internal/modules/remediation/...
go vet ./internal/platform/observability ./internal/modules/remediation/...
```

Full backend gate from `backend/`:

```bash
gofmt -w .
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/...
make generate-check
```

Use a workspace-local or `/tmp` `GOCACHE` if the default cache is read-only.

## 6. Documentation And Change Review

- Update the backend logging spec with the explicit remediation DEBUG payload
  exception, stable events/fields, 64 KiB contract, and credential rules.
- Update the remediation adapter spec for Git/SSH logger injection and payload
  ownership.
- Run GitNexus `detect_changes(scope: "all")`; separate this task's scope from the
  repository's pre-existing dirty-worktree findings.
- Review that no migration, generated SQL, API, or frontend file changed.
