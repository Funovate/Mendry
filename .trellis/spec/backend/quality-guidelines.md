# Quality Guidelines

> Code quality standards for backend development.

---

## Overview

Backend changes must remain buildable as an independent Go module and keep
process construction explicit. Third-party dependencies must belong to the
implementation slice that owns them. Developer-only generators stay in the
isolated `backend/tools` module so they cannot enter production binaries or
raise the production module's Go version.

## Forbidden Patterns

- Mutable package-level clients, registries, or configuration.
- Hidden `os.Getenv` reads outside the command/composition path.
- Teaching Make to `include` or `source` `.env`, or calling `os.Setenv` so later
  hidden environment reads can see file-only values.
- Business behavior in `cmd`, HTTP adapters, or platform packages.
- Generic `utils`, `common`, `helpers`, global model, or catch-all repository
  packages.
- Tests that change global environment/logger state when an injected lookup or
  writer is available.
- Unit tests that require real external endpoints or unconditionally bind a
  network socket.
- Integration tests that skip when an endpoint or isolation marker is absent;
  commands with destructive setup must fail closed.

## Required Patterns

- Constructors validate required dependencies before resource creation.
- Commands handle signals and delegate all substantive behavior to bootstrap or
  command implementation packages.
- Configuration and writers are injected in tests.
- HTTP server tests use an injected listener; production defaults to
  `net.Listen`.
- Release-style Make targets rebuild binaries explicitly; do not model command
  binaries as cacheable targets without complete Go source dependencies.
- Errors wrap causes with operations, and cleanup remains bounded.
- Generated files are reproducible from pinned tools and are never edited
  manually; generated English headers are exempt from the Chinese source
  comment rule, while the package still requires a hand-written Chinese
  `doc.go`.

## Comment And Documentation Contract

Comments are part of the implementation contract, not optional cleanup. A
backend change is incomplete when a future maintainer would need to reconstruct
package ownership, a cross-package contract, or a safety invariant only from
control flow.

### Required Coverage

| Change | Required documentation |
|---|---|
| New non-trivial package | A package comment, normally in `doc.go`, that states ownership, boundary, and important exclusions. |
| New exported type, function, method, constant, or variable used across packages | A Go doc comment that begins with the declaration name and explains its contract. Groups of related constants may share one group comment when their common contract is clear. |
| Lifecycle, concurrency, retry, transaction, idempotency, security, redaction, or trust-boundary logic | A nearby comment stating the invariant, ordering constraint, or failure behavior that the code preserves. |
| Intentional no-op, fallback, compatibility branch, or deferred integration | A comment explaining why the behavior is intentional and what future condition replaces it. |
| Non-obvious test setup or regression case | A comment stating the scenario or invariant being proved, unless the test name and table fields already make it unambiguous. |

Private helpers with obvious behavior do not need ceremonial comments. The
standard is semantic coverage of decisions and contracts, not a target ratio of
comment lines to code lines.

### Comment Content

- Source-code comments use Chinese for explanatory prose. Keep professional
  terms, protocol and product names, code identifiers, types, environment keys,
  and other canonical technical vocabulary in their standard English form,
  such as Go, HTTP, API, PostgreSQL, RabbitMQ, OpenTelemetry,
  `context.Context`, and `FIXTHE_HTTP_ADDR`.
- Go doc comments still begin with the declared identifier, followed by the
  Chinese explanation. For example: `// RunAPI 启动并管理 API 进程生命周期。`
  Do not translate or rewrite identifiers merely to make a comment fully
  Chinese.
- Explain **why**, ownership, invariants, constraints, or failure semantics;
  do not merely paraphrase the next statement in prose.
- Keep comments adjacent to the declaration or branch they constrain. Update
  them in the same change as behavior, signatures, defaults, or ordering.
- Use exact project terms and identifiers where useful, such as environment
  keys, process names, readiness dependencies, lease ownership, and shutdown
  stages.
- Do not leave commented-out code. A future action uses a bounded `TODO` with an
  issue/task reference or is recorded in the owning Trellis task.
- Comments never contain credentials, production endpoints, payload examples
  copied from users, or other sensitive values.

### Wrong vs Correct

#### Wrong

```go
// 关闭服务器。
err := server.Shutdown(ctx)
```

The comment repeats the call and does not preserve the ordering or timeout
contract.

#### Correct

```go
// 先停止接收新请求，再拆除依赖，避免处理中的 handler 使用已被后续
// shutdown 阶段关闭的 client。
err := server.Shutdown(ctx)
```

The comment captures the lifecycle invariant that a refactor must preserve.

### Review Enforcement

- Review every changed package for the required coverage table; required
  comments are a completion gate even when formatting, vet, and tests pass.
- Reject comments that merely restate code, contradict behavior, or describe an
  old implementation.
- Prefer a small number of precise invariant comments over dense line-by-line
  narration, but do not omit required package and exported-contract comments.
- Until an agreed documentation linter is part of `make check`, reviewers must
  enforce this contract explicitly rather than assuming `go vet` checks it.

## Testing Requirements

Run from `backend/`:

```bash
gofmt -w .
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/...
make generate-check
```

Tests assert behavior and safety boundaries: configuration does not echo invalid
values, JSON logs expose stable fields, handlers return exact media/status/body,
and cancellation completes inside a fixed deadline. Tagged integration tests
require explicit isolated endpoints and fail rather than skip when their safety
markers are missing.

## Code Review Checklist

- Dependency direction matches `directory-structure.md`.
- No command or module creates hidden global state.
- New packages, exported cross-package contracts, and non-obvious safety or
  lifecycle invariants satisfy the comment and documentation contract.
- Source comments use Chinese explanatory prose while preserving canonical
  English technical terms and code identifiers.
- Comments explain current intent and constraints; no stale narration,
  commented-out code, or untracked TODO remains.
- Logs contain route patterns and stable events, not raw URLs or payloads,
  except the explicit private-operator contracts in logging-guidelines.md,
  including inbound request debug, original server diagnostics, and Tencent
  CLS request/response plus projected evidence records.
- New environment keys appear in `.env.example`, configuration tests, and the
  backend code-spec.
- Command lookup may overlay a cwd `.env`; tests inject `Lookup` or a temp file
  and never require the developer's real `backend/.env`.
- All shipped commands pass formatting, vet, unit, race, and build checks.
