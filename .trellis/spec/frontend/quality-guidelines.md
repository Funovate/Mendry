# Quality Guidelines

## Required Patterns

Real application E2E tests use same-origin Playwright route mocks for
`/api/v1/**`. Mocks return backend-shaped JSON and record writes so tests can
assert method, nested project path, and request body. A click-only assertion is
insufficient for configuration, membership, secret, or incident lifecycle
changes.

## Testing Requirements

The complete frontend gate is:

```bash
npm run lint
npm run typecheck
npm run test
npm run build
npm run test:e2e
cd ..
git diff --check
```

Vitest covers API schema/error behavior, project-scoped query keys and `401`
cache semantics, plus pure configuration projections. Playwright covers the
same-origin `/api/v1/**` boundary, stable direct URLs and history, project
switch isolation, feature-local failures, Session expiry, mutations, and
mobile navigation.

Route-mocked E2E does not exercise the Vite-to-Go proxy. Before handing out a
local URL, start the real API and Vite server, then verify both a read and a
write through `http://127.0.0.1:4173/api/v1/**`. The Vite `/api` proxy must
preserve the browser Host; do not set `changeOrigin: true`. With the backend's
default empty CORS allowlist, rewriting Host to `127.0.0.1:8080` makes browser
`Origin: http://127.0.0.1:4173` fail as `origin_not_allowed`.

Run `npm audit --omit=dev` when direct production dependencies change. Inspect
the advisory before changing versions; do not run a forced audit fix.

## Code Review Checklist

- `src/main.tsx` imports the modular app and `styles/production.css`, not the
  prototype `App.tsx`, `data.ts`, or `styles.css`.
- Project data requests include the active stable project key.
- Server capabilities, not hidden controls alone, define displayed actions.
- Loading, empty, unauthenticated, not-configured, and error states are distinct.
- Secret values are cleared and never appear in response state or rendered text.
- Project rename and credential edit stay on the same project key and secret ID.
- Incomplete Git replacement drafts disable Save instead of sending name-only.
- Editing one configuration field does not reset hidden connector fields.
- A business `401` leaves Session observed with `null` data and clears only
  non-session queries.
- Desktop and 390px mobile screenshots have no overlapping controls or
  horizontal document overflow after navigation transitions settle.
- The actual Vite proxy reaches a ready Go API, returns
  `authentication_required` for an anonymous read, and reaches authentication
  (`invalid_credentials` or success) for a login write without a CORS error.
