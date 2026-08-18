# Implementation Plan: Backend-Aligned Frontend Scaffold

## Ordered Checklist

- [x] Pin existing direct dependencies and add React Router, TanStack Query,
      Zod, ESLint, Vitest, and Testing Library with explicit scripts for lint,
      type-check, unit tests, build, and E2E.
- [x] Add application composition, query-client configuration, root error
      boundary, and URL route definitions without changing the production
      entry point yet.
- [x] Refactor `src/api.ts` into the runtime-validated request boundary with
      cancellable reads, stable `ApiError`, `ApiContractError`, typed inputs,
      and tests for success, `204`, backend errors, non-JSON errors, malformed
      success payloads, and unauthorized responses.
- [x] Implement session query/login/logout behavior and central `401` cache
      invalidation; cover initial unauthenticated state and mid-session expiry.
- [x] Implement the project directory and route-owned project gate, including
      empty, unavailable, unknown-key, creation, and first-project redirects.
- [x] Extract the authenticated shell, project switcher, responsive
      navigation, shared controls, and generic page states. Preserve the
      current visual hierarchy and capability display.
- [x] Migrate incidents to project-keyed queries and URL-owned selection;
      preserve list filtering and authorized lifecycle updates.
- [x] Migrate observations and audit into independently loading routes so a
      failure in either cannot block other features.
- [x] Migrate members and membership mutations with server capability gates
      and project-keyed cache updates.
- [x] Migrate configuration summary/editor and secret creation. Preserve all
      connector-specific fields, conditional secret reads, plaintext clearing,
      and the `configuration_not_found` state.
- [x] Split styles into tokens/base, shell/shared, and feature ownership while
      preserving desktop/mobile layout and removing production dependence on
      prototype-only remediation styles.
- [x] Switch `main.tsx` to the composed application and remove `RealApp.tsx`
      only after all existing workflows pass through feature routes.
- [x] Expand route-mocked Playwright coverage for direct URLs, history,
      project-switch isolation, partial feature failure, and session expiry;
      add focused Vitest coverage for API schemas, query keys, and extracted
      state/projection logic.
- [x] Run the full quality gate and inspect desktop/mobile screenshots for
      overflow, stale project content, inaccessible navigation, and secret
      leakage.

## Validation Commands

```bash
cd frontend
npm run lint
npm run typecheck
npm run test
npm run build
npm run test:e2e
cd ..
git diff --check
```

## Review Gates

1. API schemas match the Go HTTP response structs and route mocks.
2. Session expiry and project switching pass before feature migration expands.
3. Read-only routes pass before configuration and membership mutations move.
4. Configuration round-trip and secret-redaction assertions pass before the
   old production entry point is removed.
5. Full desktop/mobile E2E passes after the entry-point switch.

## Risky Files And Rollback Points

- `frontend/src/api.ts`: keep backend handler structs and existing E2E mocks as
  the contract reference; do not accept malformed success data to make a test
  pass.
- `frontend/src/RealApp.tsx` and `frontend/src/main.tsx`: delay removal/switch
  until every supported route is migrated and green.
- `frontend/src/styles.css`: split mechanically first, then make only scoped
  compatibility fixes. Avoid a visual redesign in the same change.
- Configuration editor: compare the complete outgoing payload before and
  after migration, especially source/trigger `config` fields and secret IDs.

## Completion Gate

Planning is complete when the user approves `prd.md`, `design.md`, and this
execution plan. Implementation starts only after `task.py start` changes the
task status to `in_progress`.

## Verification Record

Verified on 2026-08-13:

- `npm run lint`, `npm run typecheck`, `npm run test`, and `npm run build` pass.
- Vitest: 11/11 tests pass.
- Playwright: 11/11 route-mocked E2E tests pass.
- `npm audit --omit=dev`: 0 vulnerabilities.
- `git diff --check` passes for the complete worktree.
- Desktop and 390x844 mobile screenshots were inspected after navigation
  transitions settled; no overlapping controls, document overflow, stale
  project content, or rendered secret plaintext was found.
- Real Vite-to-Go proxy smoke verifies the remote PostgreSQL/Redis-backed API
  is ready and browser-origin login reaches authentication without
  `origin_not_allowed`.
