# Implementation Plan: Unified API Success Envelope

## Ordered Checklist

1. Load backend and frontend Trellis specs with `trellis-before-dev`; record the
   final response contract and identify every success writer and list contract.
2. Extend `backend/internal/platform/httpserver` with the success envelope,
   list-total writer, request metadata clock, and focused unit tests. Preserve
   the existing error envelope and `204` behavior.
3. Add `COUNT(*) OVER()` totals to the six PostgreSQL list query families,
   regenerate sqlc output, and update repository/application interfaces,
   implementations, fakes, and tests to return named `{Items, Total}` results.
4. Update project, incident, observation, auth, and system HTTP handlers to use
   the shared success writer. Ensure every JSON list has a server total and every
   object response has request metadata.
5. Update `frontend/src/api.ts` with generic success envelope schemas and
   unwrapping. Return `{items,total}` for list methods and update feature pages,
   query tests, and all Playwright route mocks.
6. Update backend/frontend API specs and endpoint tests to document the wire
   contract, stable `ok`/`OK` values, total semantics, and duration metadata.
7. Run formatting, generated-code validation, backend tests/build, frontend lint,
   typecheck, unit tests, build, and E2E tests. Fix contract drift before review.

## Validation Commands

```bash
cd backend
make check
make build
make generate-check
cd ../frontend
npm run lint
npm run typecheck
npm run test
npm run build
npm run test:e2e
cd ..
git diff --check
```

## Review Gates

1. The public JSON shape is tested once in `httpserver` and no handler manually
   assembles a success envelope.
2. Every limited PostgreSQL list returns a real filtered total, including zero
   for an empty result and a total greater than the page size when applicable.
3. Frontend components receive DTOs/list results, never raw wire envelopes, and
   no old bare-array mocks remain.
4. Error envelope, request-ID equality, status codes, cookies, and 204 behavior
   are unchanged.

## Risky Files And Rollback Points

- `backend/internal/platform/httpserver/{middleware,response}.go`: shared
  response metadata and error behavior; keep focused tests green after every
  change.
- `backend/internal/modules/*/application` and PostgreSQL query/generated
  files: interface fan-out and total correctness; regenerate instead of editing
  generated files manually.
- `frontend/src/api.ts` and feature query consumers: all pages depend on these
  return types; update mocks and schemas together.
- `.trellis/spec/backend/error-handling.md` and
  `.trellis/spec/frontend/type-safety.md`: update only after implementation is
  verified so the docs describe the executable contract.

## Completion Gate

The task is ready to archive only when backend and frontend quality gates pass,
generated code is current, no JSON success route emits a bare object/array, and
the final response contract is recorded in the relevant specs.
