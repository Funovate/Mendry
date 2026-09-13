# Project overview

The project entry route is `/projects/:projectKey/overview`. The page reads a project-scoped snapshot and a separately paginated task list. Visible tabs refresh every five seconds; returning to the tab refreshes immediately. Failed refreshes retain the last successful snapshot and display an error.

## Deployment

Apply migration `000021_project_overview.up.sql` with the existing migration command before deploying the API and frontend. The migration adds Token usage accounting and a phase-entry clock. It does not backfill historical time-series events or infer phase-entry timestamps for existing runs.

Existing cumulative input/output Token values remain authoritative for all-time totals. Positive changes to those counters append usage events in the same database transaction. Identical updates do not add events; rollback removes both the counter update and its event. Counter decreases are rejected. Cascading run deletion removes associated events.

`overview_collection.enabled_at` records the collection epoch independently of the first model call. Empty periods before that epoch are unknown, not zero. A bucket intersecting the epoch is marked partial. Usage timestamps represent database accounting time rather than the provider's request start time. Provider calls that never commit usage to a run are not included; totals are reported usage, not a billing estimate.

## HTTP contracts

Both endpoints require the existing session and resolve the project through the project service. They use the standard success/error envelopes. Query values are validated; arbitrary sort expressions are never accepted.

- `GET /api/v1/projects/{projectKey}/overview?timezone=Asia%2FShanghai&range=7d`
  - `timezone`: IANA timezone, default `UTC`; `Local` is rejected.
  - `range`: `today`, `7d` (default), or `30d`.
  - Returns counts, all-time and today Token totals, bounded stage cards, trend buckets, and operational attention metrics in one SQL snapshot.
  - `today` uses hourly buckets and a seven-day performance window. Other ranges use calendar-day buckets and the selected performance window. DST hours are not flattened to an assumed 24-hour day.
- `GET /api/v1/projects/{projectKey}/overview/tasks?scope=all&sort=recent&page=1&pageSize=20`
  - `scope`: `all`, `flow` (active attempts plus latest outcome), `attention` (latest waiting result on open incidents), `failed`, or `budget`.
  - Optional `state`: bounded lowercase identifier; unknown future states remain visible/filterable.
  - `sort`: `recent` or `tokens`, with stable timestamp/attempt/ID tie breakers.
  - `page`: 1–100000; `pageSize`: 1–100. Empty pages still return the complete matching `meta.total`.

## Counting rules

Processed incidents are distinct incidents with any terminal attempt. Today counts distinct incidents whose attempts ended within the selected local day. Active incidents are distinct incidents with nonterminal attempts. Success/failure use the latest attempt across all series/generations of each incident.

Delivery success includes diagnosis ready for review, non-code conclusion, and repair awaiting human review. It does not imply production recovery. Failures include failed runs and budget exhaustion; blocked manual review is separate. Recovered counts only current `Recovered` incident status, not `Closed`. These measures overlap and cannot be added together.

The diagram shows active attempts and latest outcomes. Each stage returns at most three task cards plus its full count. Terminal outcomes can occur early; the diagram is not a percentage-complete estimator. Historic phase-entry times remain unknown until a new transition occurs.

## Verification

```sh
cd backend
go test ./internal/modules/overview/... ./internal/commands/migrate/... ./internal/modules/remediation/adapter/postgres/...
# Point only at a PostgreSQL role allowed to create temporary test databases.
# The test creates/drops a unique database and never resets the URL's database.
OVERVIEW_TEST_POSTGRES_URL=postgres://user@localhost/postgres go test -tags integration ./internal/modules/overview/...

cd ../frontend
npm run test
npm run lint
npm run build
npx playwright test
```

The integration test applies all migrations to a fresh database, keeps a pre-migration task for historical accounting checks, verifies rollback/deduplication/phase clocks, checks project isolation and pagination, and runs `EXPLAIN ANALYZE` on the actual snapshot SQL. Browser tests cover node clicks, polling between phases, pagination, task dialogs, error retention, session expiry and mobile layout. Browser API fixtures do not substitute for database integration tests.
