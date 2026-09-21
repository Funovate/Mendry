# Mendry operator console

This package contains the React and TypeScript console for the existing Mendry
incident application. It provides the single-user login, project configuration,
event stream, incident review, remediation controls, and project notification
screens. It is not the planned generic Agent Harness run/artifact UI.

## Development

Use Node.js `22.19.0` and npm. Install the locked dependencies and start the
development server from this directory:

```bash
npm ci
npm run dev
```

Open the URL printed by Vite (normally `http://localhost:5173`). The development
server proxies `/api` requests to `http://127.0.0.1:8080`, so start and bootstrap
the backend first by following the [root setup guide](../README.md#start-the-api).
Public `/hooks/{token}` requests do not pass through this proxy and must reach
the backend directly.

The browser client uses same-origin cookie authentication and validates API
responses with Zod. Do not put credentials or provider tokens into frontend
environment variables or browser storage; project secrets are submitted to the
backend and returned only as metadata.

## Structure

```text
src/app/            application composition, routes, query client, error boundary
src/features/       auth, projects, configuration, observations, incidents, notifications
src/layouts/        authenticated workspace shell
src/shared/         reusable console components
src/styles/         global and feature-facing styles
tests/*.test.*      Vitest unit and component tests
tests/*.spec.ts     Playwright browser flows with mocked API routes
```

`src/api.ts` owns the typed HTTP boundary. Keep feature-specific state and UI in
the corresponding `src/features/` directory, and keep server-owned validation
and authorization in the backend.

## Verification

Run the standard package checks:

```bash
npm run lint
npm run typecheck
npm test
npm run build
```

Run browser tests separately:

```bash
npx playwright install chromium
npm run test:e2e
```

The Playwright configuration starts Vite on `http://127.0.0.1:4173`. Browser
tests intercept their API requests, so they do not require a running backend,
PostgreSQL, or Redis. On Linux machines missing browser system libraries, use
`npx playwright install --with-deps chromium` for the initial installation.

For implemented feature boundaries and operator workflows, see the
[feature map](https://www.mendry.net/docs/reference/features/) and
[operator workflow](https://www.mendry.net/docs/guides/operator-workflow/).
