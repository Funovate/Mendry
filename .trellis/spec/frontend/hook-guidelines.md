# Hook Guidelines

## Server State

TanStack Query owns authentication, projects, and every project resource. A
route reads only the resource it renders and passes the query-provided abort
signal into `api.ts`:

```tsx
const incidents = useQuery({
  queryKey: queryKeys.incidents(project.key),
  queryFn: ({ signal }) => api.listIncidents(project.key, signal),
});
```

Never fetch server state in `useEffect`, mirror query data into component
state, or construct query keys inline. Use the factories in `app/query.ts`.

## Shared Hooks

Shared server-state hooks live in `app/queries.ts`. Current shared hooks are
`useSessionQuery()` and `useProjectsQuery()`. Keep a query local to a feature
when only one route consumes it. Extract a hook only when multiple production
consumers need the same behavior or the hook encodes a shared boundary.

## Mutations

- Capture `project.key` in the mutation and update only the matching key.
- Use `setQueryData` for a complete mutation response that can deterministically update a list or record.
- Use invalidation when the response does not contain enough data for a correct cache update.
- Render mutation error and pending state in the owning feature.
- Remediation start/continue mutations invalidate the project-scoped incident
  remediation query after the server acknowledges the action; the continue
  endpoint returns a durable queued attempt before its long-running coordinator
  work, so the owning component must use `mutate` and never await model or
  connector execution in the click handler. The response does not carry the
  full review payload, so `setQueryData` is not appropriate. Submit
  `generation`, `runId`, and `version` read from the review response, never from
  component state.
- Render the `Continue analysis` control only when the server response says
  `continuationAvailable` (write capability AND eligible terminal state); on
  conflict/forbidden/unsupported responses disable further stale clicks while
  preserving the returned safe message.
- Secret plaintext remains form-local and is cleared in `onSuccess`.
- Project rename lives in `layouts/ProjectSwitcher`. It replaces the matching
  `projects` item and invalidates configuration. Secret update replaces the
  matching secrets-list item.

```tsx
queryClient.setQueryData<ListResult<ProjectMember>>(
  queryKeys.members(project.key),
  (current) => updateMembers(current, member),
);
```

List query data is `{ items, total }`. Deterministic mutation updates must
preserve the server total and adjust it only when an item is actually added or
removed; never replace it with `items.length`.

## Authentication Expiry

Global query and mutation caches handle business-request `401` responses. The
order is a contract:

```ts
client.setQueryData(queryKeys.session, null);
void client.cancelQueries({ predicate: (query) => query.queryKey[0] !== "session" });
client.removeQueries({ predicate: (query) => query.queryKey[0] !== "session" });
```

Do not call `removeQueries()` for Session before setting its observed data to
`null`. Removing the observed Session query first leaves the auth gate attached
to a detached query and can strand the UI in its loading state.

## Common Mistakes

- Omitting `projectKey` from a project-resource query key.
- Updating a generic project cache after a mutation instead of the exact resource key.
- Swallowing all configuration errors as "not configured"; only the stable
  `configuration_not_found` API error maps to `null`.
- Using local role state for authorization instead of `project.capabilities`.
