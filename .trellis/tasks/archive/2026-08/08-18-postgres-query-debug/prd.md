# PostgreSQL query debug SQL logging

## Goal

Operators can turn on an explicit PostgreSQL query debug switch and see the
complete SQL text plus bind arguments on existing `db.query.completed` records,
so a slow or failing query can be reproduced from logs.

## Background

Default query telemetry stays data-minimized. `QueryTracer` records only a
stable `db.operation.name`, duration, rows, outcome, and error class. It
deliberately drops `pgx.TraceQueryStartData.SQL` and `Args`. That is enforced by
`backend/internal/platform/postgres/tracer.go:26-36`,
`tracer.go:48-56`, `tracer_test.go:16-42`, and
`.trellis/spec/backend/{logging,database}-guidelines.md`.

On 2026-08-14 this same gap was discussed. The then conclusion was that
operation names already identify which query is slow or failing, so only the
HTTP failure snapshot was added. That is not enough when the operator needs the
exact statement and bind values.

This task adds an opt-in exception. It does not change the default-off
contract, and it does not change query log levels.

Successful queries remain `DEBUG`. Local default `FIXTHE_LOG_LEVEL=info`
therefore still hides successful SQL unless the operator also lowers the log
level. Slow and failed queries keep their current `WARN` / classified levels
and will show SQL/args whenever the debug switch is on.

## Requirements

- **R1.** Add `FIXTHE_POSTGRES_QUERY_DEBUG`, default `false`. Accepted values
  are `true` and `false` (case-insensitive). Invalid values fail config load
  with a field error that does not echo the raw value. `FIXTHE_LOG_LEVEL=debug`
  alone does not enable SQL or arguments.
- **R2.** When the switch is off, query logs, spans, and metrics remain
  unchanged: no SQL text, no bind arguments, no database error messages.
- **R3.** When the switch is on, every `db.query.completed` log record that is
  actually emitted includes one interpolated SQL statement: `$n` placeholders
  are replaced with PostgreSQL literals so the text can be copied into `psql`.
  Existing fields (`db.operation.name`, duration, rows, outcome, error class)
  stay present. Console writes the statement on following physical lines
  instead of a quoted `sql=` attribute.
- **R4.** Debug fields are written only to the application logger (console or
  JSON). OpenTelemetry spans and metrics never receive SQL or arguments.
- **R5.** Bind values are interpolated as-is. Do not redact, hash, truncate, or
  omit values because they look like secrets.
- **R6.** Specs and `.env.example` document the switch as an explicit, unsafe
  debug exception. Default comments continue to say ordinary query records do
  not contain SQL or parameters.
- **R7.** All processes that load `LoadPostgreSQL` honor the same switch.
- **R8.** The switch does not change query log levels. Successful queries stay
  `DEBUG`; slow queries stay `WARN`; failures keep the existing classified
  levels.

## Technical Notes

- Config keys live in `backend/internal/platform/config/config.go`. New keys
  must also appear once in `backend/.env.example`;
  `TestEnvironmentExampleDocumentsEveryConfigurationKey` enforces that.
- `LoadPostgreSQL` is shared by API, migrate, seed, and bootstrap-admin. There
  is no boolean parser today; helpers are `enumValue`, `durationValue`, and
  `integerValue`.
- Pool construction in `backend/internal/platform/postgres/pool.go:110-116`
  creates one `QueryTracer` per process and assigns it to
  `poolConfig.ConnConfig.Tracer`.
- Query start currently stores only `started`, `operation`, and `span`. SQL and
  args are available on `TraceQueryStart` and must be retained on the trace
  state if they are to be logged at `TraceQueryEnd`.
- New stable field names must be constants in `observability` and covered by a
  JSON record test. Console may use shorter display keys.
- Existing default-off tests must keep failing if SQL text or bind values leak
  when the switch is off.

## Acceptance Criteria

- [x] AC1. Unset or `false` `FIXTHE_POSTGRES_QUERY_DEBUG` keeps the current
      tracer test invariant: SQL text and bind values never appear in logs.
      (R2)
- [x] AC2. `FIXTHE_POSTGRES_QUERY_DEBUG=true` causes an emitted
      `db.query.completed` JSON record to contain one interpolated SQL
      statement with `$n` replaced by PostgreSQL literals. (R1, R3, R5)
- [x] AC3. The same enabled query still produces a span/metric without SQL or
      bind arguments. (R4)
- [x] AC4. `FIXTHE_LOG_LEVEL=debug` alone does not attach SQL or arguments.
      (R1)
- [x] AC5. Invalid switch values fail `LoadPostgreSQL` without echoing the raw
      value. (R1)
- [x] AC6. `backend/.env.example` documents the new key exactly once, default
      `false`, with a warning that enabled logs contain interpolated SQL.
      (R6)
- [x] AC7. Logging and database specs describe the default-off contract and
      this explicit debug exception, including that levels do not change. (R6,
      R8)
- [x] AC8. Enabling the switch does not promote a successful query from
      `DEBUG` to `INFO`. (R8)

## Out Of Scope

- Environment-based deny list (`production` may still enable the switch).
- Redaction or truncation of secret-looking or large bind values.
- Putting SQL or arguments on spans, metrics, or database error messages.
- Logging returned rows.
- Redis command argument logging.
- Changing HTTP request/response snapshot policy.
- Changing query log levels so default `info` shows successful SQL.
