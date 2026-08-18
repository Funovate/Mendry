# Technical Design: Backend-Aligned Frontend Scaffold

## Scope And Approach

This is an in-place architecture migration of the current backend-connected
console. It retains React, TypeScript, Vite, the backend REST contract, the
approved visual language, and all currently working workflows. It does not
introduce new incident-domain capabilities.

The migration uses three focused platform dependencies:

- React Router for URL-owned navigation and route error boundaries.
- TanStack Query for session and project-scoped server state.
- Zod for runtime validation at the HTTP boundary.

Vitest and Testing Library provide fast tests below the Playwright layer.
ESLint enforces TypeScript and React hook rules. Direct dependency versions are
pinned so a clean install cannot silently change the toolchain.

## Module Boundaries

```text
src/
  app/                 composition, router, query client, root boundaries
  api.ts               sole HTTP client, DTO/schema, and API error owner
  layouts/             authenticated operational shell and navigation
  shared/              reusable UI, formatting, and generic page states
  features/
    auth/              login and session gate
    projects/          project directory, creation, route project lookup
    incidents/         list, detail, status mutation
    observations/      event stream
    configuration/     summary, editor, secret write flow
    members/           membership table and mutations
    audit/             audit-event list
```

Feature modules may import `api.ts` and `shared/`. `shared/` must not import a
feature. The application layer composes features but owns no feature-specific
forms or DTO copies. Prototype `App.tsx` and `data.ts` remain isolated
references and are not imported by the production graph.

## Route Model

```text
/login
/projects
/projects/new
/projects/:projectKey/incidents
/projects/:projectKey/incidents/:incidentId
/projects/:projectKey/observations
/projects/:projectKey/configuration
/projects/:projectKey/configuration/edit
/projects/:projectKey/members
/projects/:projectKey/audit
```

`/` resolves the session and project directory, then redirects to the first
accessible project's incident route or to `/projects` when the list is empty.
Unknown project keys render a recoverable project-not-found state. The active
project and selected incident are projections of route parameters, not a
second mutable cursor.

Browser-history routes require an SPA fallback in the eventual production web
server. That packaging is outside this task; Vite dev/preview and Playwright
provide the fallback during this work.

## Server State And Session

TanStack Query owns all remote state. Query keys are factories and always
include the stable project key for project resources:

```text
session
projects
project/{projectKey}/configuration
project/{projectKey}/secrets
project/{projectKey}/observations
project/{projectKey}/incidents
project/{projectKey}/members
project/{projectKey}/audit-events
```

Each route fetches only the resource it renders. Configuration fetches secrets
only when the server capability permits it. Mutations update or invalidate the
owning project key; they do not modify a shared `ProjectData` aggregate.

Query functions pass TanStack Query's `AbortSignal` into the central request
helper. Route changes therefore cancel obsolete reads where possible. Even if
a transport completes late, its key prevents it from becoming another
project's data.

The session query maps the expected `/auth/me` unauthenticated response to a
signed-out state. Global query and mutation error handlers recognize other
`401` responses, clear session/project caches, and let the auth gate redirect
to `/login`. Successful login seeds the session cache; logout clears all
session-owned data in `finally`.

## API Contract Boundary

`src/api.ts` remains the sole request helper and HTTP DTO owner to comply with
the current frontend specification. It defines Zod schemas for every response
shape currently consumed and derives exported DTO types from those schemas.

The request flow is:

```text
feature query -> typed API method -> fetch -> status/error decode
              -> runtime response parse -> typed feature data
```

Backend error envelopes are validated leniently enough to preserve a stable
fallback for proxy or non-JSON failures. Malformed successful JSON produces a
distinct `ApiContractError`; it is never cast to a DTO. Request bodies remain
explicit typed inputs. Secret plaintext is accepted only by the create-secret
input and is absent from every response schema.

## UI And Styling

The existing operational-console composition remains the visual baseline.
The stylesheet is reorganized around tokens/base styles, shell/shared states,
and feature-owned styles. Reusable controls cover icon buttons, command
buttons, status pills, notices, loading/empty states, and table frames.

This task does not create a general-purpose component library. A shared
component is extracted only when it already has multiple production consumers
or encodes accessibility/state behavior that must stay consistent.

Route navigation exposes the active location with semantic state. Loading and
mutation messages use appropriate live regions. Root and route-level error
boundaries provide recovery for unexpected rendering failures separately from
recoverable API failures.

## Compatibility And Migration

The production entry point changes from `RealApp` to the composed application.
Features are migrated without changing backend paths or payload fields. The
existing route-mocked E2E suite is adapted to stable URLs and remains the
behavioral regression gate.

No temporary second production entry point is retained. `RealApp.tsx` is
removed only after every current workflow is represented by a feature route.
The static prototype files remain available for future visual comparison but
cannot enter production imports.

## Risks And Rollback

- Routing can expose missing SPA fallback behavior. Keep deployment packaging
  out of scope and validate direct loads through Vite/Playwright.
- Query invalidation mistakes can leave stale UI. Central query-key factories
  and project-switch regression tests are required before deleting `RealApp`.
- Runtime schemas can reveal existing backend/mock drift. Backend handlers are
  authoritative; update mocks or frontend schemas rather than weakening all
  validation.
- The configuration editor has the widest round-trip contract. Migrate it
  after the API and query layers, and retain the existing hidden-field
  preservation assertion as a rollback gate.

The refactor is rollbackable until the final entry-point switch. Keep commits
or review checkpoints aligned with platform setup, shell/session, read routes,
and mutation routes so a failed later slice does not invalidate earlier work.
