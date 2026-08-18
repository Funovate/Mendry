# PostgreSQL query debug SQL logging

## Boundaries

- `config` owns the new key, default, and validation. `LoadPostgreSQL` stays
  the single loader for API, migrate, seed, and bootstrap-admin.
- `postgres.QueryTracer` is the only place that may attach SQL or bind
  arguments to a log record. Repositories and generated sqlc code stay
  unaware of the switch.
- `observability` owns the stable field names and console display aliases.
  Console/JSON handlers do not invent local keys.
- OpenTelemetry spans and metrics stay on the existing safe contract. The
  debug switch never crosses that boundary.

## Config

Add `FIXTHE_POSTGRES_QUERY_DEBUG` beside the other PostgreSQL keys.

- Default: `false` when unset.
- Allowed values: `true`, `false` (case-insensitive) via the existing
  `enumValue` helper. Do not add a generic bool parser unless a second
  caller appears.
- Invalid values: `fieldError` with the key and a constraint, never the raw
  value.
- Surface: `config.PostgreSQL.QueryDebug bool`.

`backend/.env.example` documents the key once, default `false`, and warns
that enabled logs contain complete SQL and bind values.

## Tracer Data Flow

```text
TraceQueryStart
  operation = operationFromContext(ctx, data.SQL)
  if QueryDebug:
      interpolate $n placeholders into one executable statement
  start span with only db.system.name + db.operation.name

TraceQueryEnd
  build existing safe attrs and level exactly as today
  if QueryDebug:
      append db.query.text
  record metric + end span without SQL
  observability.Log(... existing level ...)
```

`queryTraceState` grows an optional interpolated `statement`. It stays empty
when the switch is off so a disabled tracer cannot accidentally log SQL.

Interpolation happens at start, not at end. pgx may reuse the caller's `Args`
slice after the query returns. `$n` is replaced from the complete placeholder
so `$10` is not truncated by `$1`. Values become PostgreSQL literals: strings
are quoted and doubled, nil/`pgtype` invalid become `NULL`, booleans become
`TRUE`/`FALSE`, UUIDs and timestamps become quoted literals. No redaction,
hashing, or truncation.

Console removes `db.query.text` from tint's quoted attributes and writes the
statement verbatim after the event line, the same way stacks are projected.

## Log Contract

| Format | SQL key |
|---|---|
| JSON | `db.query.text` |
| Console | following physical lines, no `sql=` attribute |

Constants live in `observability`:

- `FieldDBQueryText = "db.query.text"`

A JSON record test must cover the interpolated statement. Console tests must
assert physical newlines and the absence of a quoted `sql=` attribute.

Levels do not change:

- success: `DEBUG`
- slow success: `WARN`
- classified failures: existing `INFO` / `WARN` / `ERROR`

`FIXTHE_LOG_LEVEL=debug` therefore remains required to see successful SQL.
The debug switch only adds fields to records that already pass the level
filter.

## Compatibility

- Default-off behavior is unchanged. Existing
  `TestQueryTracerLogsSafeOperationWithoutStatementOrArguments` remains the
  leak-detection gate.
- `NewQueryTracer` gains a `queryDebug bool` argument. The only production
  caller is `buildPoolConfig`.
- Specs keep the default "never log raw SQL/args" rule and add this as a
  third explicit exception, next to the HTTP failure snapshot and original
  error diagnostics.

## Rollback

Unset or set `FIXTHE_POSTGRES_QUERY_DEBUG=false` and restart the process.
No schema, API, or client change is involved.

## Out Of Design

- Environment deny list
- Span/metric enrichment
- Redis argument logging
- Returned rows
- Promoting success logs to `INFO`
