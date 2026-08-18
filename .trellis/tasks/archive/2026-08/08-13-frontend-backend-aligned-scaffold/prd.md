# Backend-Aligned Frontend Scaffold

## Goal

Turn the existing backend-connected React console into a maintainable
production frontend foundation without losing its proven authentication,
project-scoped authorization, configuration, and incident workflows.

## Background

- `frontend/` already uses React, strict TypeScript, Vite, Lucide icons, and
  Playwright. `src/main.tsx` renders the backend-connected `RealApp` while
  `App.tsx` and `data.ts` remain prototype references.
- `RealApp.tsx` currently owns the application shell, boot/session state,
  project selection, server-state loading, five feature views, mutations, and
  a large configuration form in one 472-line module.
- `src/api.ts` is the current typed HTTP boundary. It uses same-origin
  `/api/v1/**` requests, includes the session cookie, normalizes backend error
  envelopes, and keeps project DTOs separate from prototype fixtures.
- The Go backend currently exposes login/logout/session, project CRUD,
  project members, write-only secrets, project configuration, observations,
  incidents and status changes, audit events, and system status. Evidence,
  reports, remediation, and notification-policy HTTP contracts are not yet
  implemented.
- The route-mocked Playwright suite already covers authentication, backend-
  shaped project data, configuration round trips, secret redaction, role
  capabilities, empty project state, errors, mutations, and mobile navigation.

## Requirements

- Preserve the existing React/TypeScript/Vite delivery model and the current
  backend HTTP contract unless repository evidence shows a contract defect.
- Establish explicit application, shared UI, API/infrastructure, and
  domain-feature boundaries so new console capabilities do not accumulate in
  a single root component.
- Give each existing backend-backed screen a stable URL. Navigation, refresh,
  browser history, project selection, and incident selection must restore from
  the URL rather than component-local view state.
- Keep authentication and active-project context above project feature
  screens. Project feature reads and writes must use the stable project key
  and server-projected capabilities.
- Treat session, project directory, and project resources as server state.
  Cache keys must include the project key, requests must be cancellable, and a
  project switch must never render or mutate data owned by another project.
- Handle authentication expiry centrally: any backend `401` invalidates the
  current session and returns the application to the login route without each
  feature implementing private session logic.
- Keep backend DTO ownership and error-envelope decoding at one typed API
  boundary. Validate successful and error responses at runtime; components
  must not cast raw responses or redefine backend DTOs.
- Preserve distinct booting, unauthenticated, empty-project, project-not-
  configured, loading, and failure states.
- Give independently useful feature screens independent loading, error, retry,
  and mutation states. A failure in audit, members, or configuration must not
  prevent the incident screen from loading.
- Preserve write-only secret handling: plaintext exists only in the active
  form and request payload, is cleared after success, and is never copied into
  server response state or rendered metadata.
- Preserve the verified visual hierarchy and responsive operational-console
  behavior unless a scaffold change requires a small compatibility adjustment.
- Keep `App.tsx` and fixture data isolated as prototype/reference code during
  migration; production modules must not import fixture business data.
- Do not invent client calls, local fixture state, or placeholder mutations for
  backend capabilities that do not yet expose an HTTP contract.
- Retain route-mocked E2E coverage at the HTTP boundary and add focused tests
  where extracting state or contract logic creates independent behavior.
- Add repeatable lint, type-check, unit-test, production-build, and E2E
  commands with pinned direct dependencies.

## Acceptance Criteria

- [x] The production entry point renders a modular application composition
      rather than a monolithic feature implementation.
- [x] Every current backend-backed screen has a stable project-scoped URL and
      supports direct load plus browser back/forward navigation.
- [x] Authentication, project selection, incident list/status updates,
      observations, configuration/secrets, membership, and audit workflows
      continue to use the existing backend routes and DTO shapes.
- [x] Changing the active project cannot display or commit stale data from a
      previous project request.
- [x] Authorization controls derive from backend capabilities, and no
      production component imports prototype roles or business fixtures.
- [x] Missing configuration, empty project access, authentication expiry, and
      non-JSON/backend error responses remain distinguishable and recoverable.
- [x] A project resource request returning `401` clears session-owned and
      project-owned state and redirects to login.
- [x] A failed non-active feature request does not block the current feature,
      and stale responses cannot cross project boundaries.
- [x] Malformed successful API payloads fail at the API boundary with a stable
      client contract error rather than reaching rendering components.
- [x] Configuration edits round-trip connector-specific fields without
      resetting fields that were not edited, and successful secret creation
      leaves no plaintext in rendered content.
- [x] Desktop and mobile primary navigation remain usable with no viewport
      overflow or overlapping controls.
- [x] `npm run lint`, `npm run typecheck`, `npm run test`, `npm run build`,
      `npm run test:e2e`, and `git diff --check` pass.

## Out Of Scope

- New backend endpoints or database changes.
- Implementing evidence, reports, remediation, notifications, or connector
  execution before their backend contracts exist.
- A visual redesign of the approved console prototype.
- Production deployment, reverse-proxy packaging, or generated OpenAPI clients.
- Adding business state or fixture-backed previews for backend capabilities
  that do not yet exist.
