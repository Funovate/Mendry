# Type Safety

## Overview

The frontend uses strict TypeScript and Zod runtime validation. HTTP DTOs,
schemas, the only request helpers, and stable API error classes live in
`src/api.ts`; rendering components consume inferred DTOs rather than casting
raw response fields.

## Type Organization

- API DTOs: `src/api.ts`.
- View-only types: next to the owning component.
- Prototype fixture types: `src/data.ts`; do not import them into the real API
  boundary.
- Keep API names aligned with JSON fields (`occurrenceCount`, `remoteUrl`,
  `manageConfiguration`). Presentation projections belong in named functions.

## Validation

Server validation owns project keys, URLs, commits, source configs, triggers,
and secret references. The UI constrains obvious inputs and shows the server's
stable error message. Locally parsed JSON, such as MCP non-secret headers, must
be checked as an object with string values before making the request.
`updateSecret` accepts `{ name, value? }` and must omit `value` rather than send
an empty string. `updateProjectName` sends only `{ name }`.

Every successful JSON response is parsed as the shared `code: "ok"`,
`message: "OK"`, `data`, and `meta` envelope in `src/api.ts`. `meta` requires
the request ID and non-negative integer duration; list envelopes also require a
server-provided non-negative integer `total`. The API layer unwraps object DTOs
and returns `{ items, total }` for lists so components never parse wire envelopes.
Malformed JSON or a schema mismatch becomes `ApiContractError`; do not weaken a
schema to accommodate a fixture that disagrees with the Go HTTP response type.

## Common Patterns

Use `ApiError` for HTTP status/code branching. Unknown exceptions pass through
`messageFromError`. Do not duplicate error-envelope parsing in components.

## Forbidden Patterns

- `any`, `@ts-ignore`, and raw response casts in components.
- A second definition of project roles, capabilities, configuration, or API
  error envelopes outside `src/api.ts`.
- Copying database/sqlc field names into UI contracts when the HTTP JSON name
  differs.
