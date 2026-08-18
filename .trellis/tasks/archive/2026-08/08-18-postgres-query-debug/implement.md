# Implementation Plan

## Checklist

1. Add `PostgresQueryDebugKey = "FIXTHE_POSTGRES_QUERY_DEBUG"` and
   `PostgreSQL.QueryDebug` in `backend/internal/platform/config/config.go`.
   Parse it in `LoadPostgreSQL` with `enumValue(..., "false", {true,false})`.
2. Document the key once in `backend/.env.example` next to
   `FIXTHE_POSTGRES_SLOW_QUERY_THRESHOLD`. Default `false`. Warn that
   enabled logs contain complete SQL and bind values.
3. Extend `LoadPostgreSQL` tests:
   - default / `true` / `TRUE` succeed
   - invalid value fails without echoing the raw input
4. Add `FieldDBQueryText` in `backend/internal/platform/observability/logging.go`.
   Console hides it from tint and writes the statement verbatim after the
   event line.
5. Cover the JSON field with an observability record test, and assert
   physical newlines plus the absence of a quoted `sql=` attribute.
6. Change `NewQueryTracer` to accept `queryDebug bool`. Interpolate `$n`
   into one executable statement at `TraceQueryStart`.
7. Pass `configuration.QueryDebug` from `buildPoolConfig`. Do not put SQL
   or args on the span or metric.
8. Keep the existing default-off leak test. Add:
   - enabled tracer logs original SQL and every bind value
   - enabled tracer does not promote a successful query off `DEBUG`
   - `debug` log level alone does not attach SQL/args
   - enabled span attributes still omit SQL/args
9. Update `.trellis/spec/backend/logging-guidelines.md` and
   `database-guidelines.md` (and the PostgreSQL env table in
   `directory-structure.md` if it lists keys) so the default-off contract
   and this explicit exception are both written down.

## Validation

```bash
cd backend
go test ./internal/platform/config/ ./internal/platform/observability/ ./internal/platform/postgres/
```

If those packages pass, the config-example, logger-contract, and tracer
leak/debug paths are covered. Do not run tagged PostgreSQL integration
tests for this change; the tracer is unit-tested with pgx trace structs.

## Risky Files

- `backend/internal/platform/postgres/tracer.go`
  Default-off leak path must stay identical.
- `backend/internal/platform/observability/logging.go`
  New fields must be constants; console aliases must not hide them.
- `backend/.env.example`
  `TestEnvironmentExampleDocumentsEveryConfigurationKey` fails if the new
  key is missing or duplicated.

## Rollback

Revert the process env / restart with the switch unset. If the change has
already shipped, reverting the commit restores the previous constructor
and specs; there is no data migration.

## Ready Gate

Planning is ready for review when `prd.md`, `design.md`, `implement.md`,
and both jsonl manifests have real entries. Do not run `task.py start`
until the user approves.
