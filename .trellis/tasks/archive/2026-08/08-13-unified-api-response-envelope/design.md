# Technical Design: Unified API Success Envelope

## Scope And Contract

All JSON success responses from the API use this shape:

```json
{
  "code": "ok",
  "message": "OK",
  "data": {},
  "meta": {
    "requestId": "8a0b1b0f7567f11440f392ada3988b0d",
    "durationMs": 12
  }
}
```

List responses use the same shape, with an array in `data` and a server-derived
total in `meta`:

```json
{
  "code": "ok",
  "message": "OK",
  "data": [],
  "meta": {
    "requestId": "8a0b1b0f7567f11440f392ada3988b0d",
    "durationMs": 12,
    "total": 0
  }
}
```

`code` and `message` are stable success values for this API slice. `requestId`
is the existing validated request ID and must equal `X-Request-ID`. `durationMs`
is a non-negative integer measured at the HTTP boundary from request entry until
the success body is encoded. It is diagnostic metadata, not a business SLA.
`204 No Content` remains bodyless. Existing error responses remain exactly the
current `error` envelope and do not gain a `data` field.

## Backend Data Flow

```text
handler -> httpserver.WriteJSON/WriteListJSON
        -> success envelope + request metadata
        -> frontend api.ts unwraps and validates
```

The shared HTTP boundary starts a request clock together with request ID setup.
The response writer carries private metadata through the existing `Unwrap`
chain (`statusRecorder`, recovery tracker, and mux normalizer), so `WriteJSON`
can add request ID and elapsed milliseconds without changing every handler to
pass a request argument. Direct helper tests without the boundary use safe
zero/default metadata; production routes always run inside `Boundary`.

`WriteJSON` remains source-compatible for object responses and wraps its value
as `data`. A dedicated `WriteListJSON` (or equivalent options helper) accepts a
server-computed `total` and uses the same envelope implementation. No handler
constructs the envelope manually.

## List Totals

The current application and repository contracts return only slices, while
bounded list routes use `LIMIT`. Each list contract will return a named result
containing `Items` and `Total` so the HTTP adapter cannot accidentally infer a
total from page length.

For PostgreSQL list queries, add `COUNT(*) OVER() AS total_count` to the filtered
query before `LIMIT`. Repository mapping reads the first row's count and returns
zero for an empty result. This keeps count and page selection in one statement,
preserves project authorization filters, and avoids an inconsistent second count
query. The affected queries are projects, project members, project secrets,
project audit events, incidents, and observations. Lists without an explicit
limit still expose the same total contract.

The application layer normalizes nil item slices to empty slices. HTTP adapters
map only DTOs and pass the result total to the shared writer. Fakes and tests
must use the named list result rather than returning a slice plus an inferred
length.

## Frontend Contract

`frontend/src/api.ts` owns one Zod schema for the success envelope and one
generic unwrapping helper. Object methods continue to return their existing DTO;
list methods return `{ items, total }` so components remain unaware of the wire
envelope while retaining access to the server total. Existing components that
only iterate arrays will switch to `.items`; pages may display/use `.total` when
needed. Route mocks and API unit tests return the wire envelope and assert that
invalid envelope metadata raises `ApiContractError`.

## Compatibility And Risks

- This is a coordinated API contract change for the current frontend; there is
  no dual response mode or backward-compatible bare JSON response.
- Error mapping, status codes, cookies, authorization, project scoping, and DTO
  field names remain unchanged.
- Window counts add one scalar to generated sqlc rows but do not expose database
  fields in HTTP DTOs. `make generate-check` is required after query changes.
- Duration metadata is added only to JSON successes; access logs already record
  the same timing for operational diagnosis.

## Verification

Cover the common writer (code/message/data/meta and duration), all bounded lists
with empty/non-empty totals, all object/singleton endpoints, system health, 204,
frontend parsing/unwrapping, and route mocks. Run backend and frontend quality
gates plus generated-code checks.
