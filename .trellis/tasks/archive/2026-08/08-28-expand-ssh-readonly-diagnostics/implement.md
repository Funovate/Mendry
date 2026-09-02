# Implementation Plan

## 1. Command Policy

- [x] Replace the SSH inspect binary set with a command-policy registry.
- [x] Add reusable exact-option/subcommand helpers without introducing a generic
      utility package.
- [x] Add host/network/process/system/Docker read-only command families listed
      in the design.
- [x] Remove `env` and `printenv`.
- [x] Add table-driven parser tests for every accepted command family and every
      known mutating, streaming, privilege, nested-execution, and shell-escape
      variant.
- [x] Prove every pipeline segment is validated independently and rejection
      occurs before the inspect adapter is called.

## 2. Catalog Expansion

- [x] Change Docker SSH source catalog construction to advertise both
      `ssh.inspect` and typed `docker.logs` during diagnosis/context collection.
- [x] Update Docker/host catalog tests and tool descriptions.
- [x] Verify planning, Cloud, MCP, legacy evidence, and repository catalogs are
      unchanged.

## 3. Canonical Runtime Evidence

- [x] Add one application-owned canonical projector for SSH inspect and Docker
      log results.
- [x] Normalize UTF-8, recursively redact credential-shaped fields/text, remove
      PEM material and temporary SSH key paths, and enforce payload bounds.
- [x] Include Docker query/coverage metadata and SSH reconstructed command in
      the persisted payload.
- [x] Compute canonical content hash, scoped deduplication key, classification,
      outcome, correlation flags, and safe provenance.
- [x] Add unit tests proving raw secrets never enter canonical payloads and the
      model-visible payload exactly matches persisted JSON.

## 4. Persist Before Observation

- [x] Add a narrow runtime evidence writer boundary to `ToolGateway` with
      explicit coordinator/composition-root wiring.
- [x] In `ExecuteToolObservedWithCatalog`, persist successful SSH/Docker output
      before emitting a success observation.
- [x] Replace the returned ToolResult payload with the canonical projection and
      attach the persisted evidence ID.
- [x] On projection/persistence failure, return a stable non-retryable error,
      emit no raw result, and do not append unpersisted content to conversation.
- [x] Extend fake stores and coordinator/gateway tests for success ordering,
      evidence IDs, idempotency, and persistence failure.

## 5. Citation And Correlation

- [x] Update diagnosis guidance to require citations for conclusions that use
      SSH/Docker results.
- [x] Preserve model-assessed `hostIdentity` when merging persisted correlation
      fields that do not represent host identity; keep persisted temporal,
      operational, source coverage, and classification authoritative.
- [x] Add evidence-gate regression tests for a persisted host identity inspect
      record plus Docker/Tencent runtime evidence.

## 6. Continuation Ownership And Context

- [x] Add bounded loading of prior runtime evidence for diagnosis continuations
      in the same series.
- [x] Update evidence resolution SQL so a child attempt can cite earlier-attempt
      evidence from the same series and cannot cite cross-series/future-attempt
      evidence.
- [x] Regenerate sqlc output; do not hand-edit generated files.
- [x] Add PostgreSQL query/repository tests for same-series reuse, unchanged
      evidence IDs, no row reassignment, and cross-boundary rejection.
- [x] Add continuation tests proving sanitized prior evidence content and IDs
      reach the diagnosis context while planning checkpoint behavior remains
      unchanged.

## 7. Documentation

- [x] Update remediation adapter and evidence specs to describe expanded
      command semantics, durable sanitized runtime evidence, and same-series
      continuation ownership.
- [x] Update stale task/design text that says Docker deployments suppress
      generic inspect or SSH output is unredacted.
- [x] Keep all new/changed Go security-boundary comments in Chinese while
      retaining canonical identifiers.

## 8. Validation

Run from `backend/` unless noted:

```bash
gofmt -w internal/modules/remediation internal/bootstrap
go test ./internal/modules/remediation/application ./internal/modules/remediation/adapter/sshlog ./internal/modules/remediation/adapter/postgres
go test -race ./internal/modules/remediation/application ./internal/modules/remediation/adapter/sshlog
go test ./...
go vet ./...
go build ./cmd/...
make generate-check
git diff --check
```

- [x] Run GitNexus `detect_changes({scope:"compare", base_ref:"main"})` after
      implementation and review affected symbols/flows.
- [x] Inspect the final diff against the dirty-worktree baseline; do not include
      unrelated user changes.

## Validation Results

- `go test ./internal/modules/remediation/...`: passed.
- `go test -race ./internal/modules/remediation/application ./internal/modules/remediation/adapter/sshlog`: passed.
- `go test ./...`: passed.
- `go vet ./...`: passed.
- `go build ./cmd/...`: passed.
- `$HOME/go/bin/sqlc version`: `v1.31.1`; direct generation with
  `backend/sqlc.yaml` completed and left generated output consistent.
- `make generate-check`: could not complete because the Go toolchain attempted
  to verify/download `go1.26.0` through unreachable `sum.golang.org`; this is an
  environment/network failure, not a generated-code mismatch.
- `git diff --check`: passed.
- GitNexus per-symbol impact was LOW. Repository-wide `detect_changes` reported
  CRITICAL across 67 files because the pre-existing dirty worktree is included;
  the task-local affected flows are the expected remediation catalog,
  execution/persistence, evidence resolution, and continuation paths.

## Risk And Rollback Points

- Command validators are the primary security boundary. Stop if a command
  family cannot be proven read-only from parsed argv; omit it rather than rely
  on the process timeout.
- Runtime evidence persistence sits on the core diagnosis loop. Never expose a
  successful raw result when persistence fails.
- Same-series evidence queries must remain monotonic by attempt number and must
  not widen to arbitrary incident/run IDs.
- The worktree already contains unrelated changes. Do not revert, reformat, or
  stage files outside this task's implementation and documentation scope.
