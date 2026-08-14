# State Management

## Scenario: Project-Scoped Server State

### 1. Scope / Trigger

Use this contract whenever a screen reads or mutates authentication, projects,
members, configuration, secrets, observations, incidents, or audit events. The
React prototype and its fixtures are visual references, not server state.

### 2. Signatures

The typed boundary is `frontend/src/api.ts`. Project business data uses nested
routes:

```text
GET  /api/v1/auth/me
GET  /api/v1/projects
GET  /api/v1/projects/{projectKey}/configuration
GET  /api/v1/projects/{projectKey}/observations
GET  /api/v1/projects/{projectKey}/incidents
GET  /api/v1/projects/{projectKey}/members
GET  /api/v1/projects/{projectKey}/audit-events
```

Writes use the matching typed functions on `api`, including
`putConfiguration`, `createSecret`, `updateProjectName`, `updateSecret`,
`upsertMember`, and `updateIncidentStatus`.

```text
PATCH /api/v1/projects/{projectKey}
PATCH /api/v1/projects/{projectKey}/secrets/{secretId}
```

### 3. Contracts

- React Router owns active project, incident selection, and create/edit modes.
- `Project.role` is `admin | operator | viewer`.
- `Project.capabilities` contains `read`, `writeIncidents`, `manageMembers`,
  and `manageConfiguration`.
- Components use capabilities for action availability. A local role selector
  must never authorize a business action.
- `ProjectConfiguration` owns one environment, repository, source, and trigger
  snapshot. Source and trigger config objects must round-trip every field for
  their selected kind.
- Secret values exist only in the write form and outgoing create/update request.
  Responses are metadata only; clear the input after success. `updateSecret`
  omits `value` for a name-only edit and never sends `kind`.
- Git HTTPS compose `username:token` into one `git_credential` value. Git SSH
  compose the private key and optional passphrase into one `ssh_private_key`
  value. Configuration still stores only `credentialSecretId`. Incomplete Git
  replacement drafts are not complete and must not omit `value`.
- After `updateProjectName`, replace the matching item in the `projects` cache
  and invalidate configuration so a synchronized environment name is fetched.
  After `updateSecret`, replace the matching item in the project-scoped secrets
  cache without changing selection.
- Git baseline comes from `POST /api/v1/projects/{projectKey}/repository/refs`.
  Save sends the probed `productionBranch` and `deployedCommit`; the request
  never includes secret plaintext.
- `credentials: "include"` is set in the central request helper.
- Every project resource query key starts with `project`, then the stable
  project key. A response for one key can never populate another project's UI.
- Any project/business query or mutation returning `401` sets the observed
  Session query to `null`, cancels non-session queries, and removes only
  non-session queries. The auth route then redirects to `/login`.

### 4. Validation & Error Matrix

| Condition | UI state |
|---|---|
| `/auth/me` returns 401 | Login form |
| Project list is empty | Explicit no-project state |
| Project list request fails | Error + retry, never no-project state |
| Configuration returns `configuration_not_found` | Project-not-configured state |
| Other project resource fails | Project page error + retry |
| Project resource returns 401 | Session becomes `null`; non-session cache clears; redirect to login |
| Successful response fails Zod parsing | `ApiContractError`; route error + retry |
| Capability is false | Mutation control hidden or disabled |

### 5. Good/Base/Bad Cases

- Good: an admin creates a write-only secret, links its returned ID, and saves
  a complete configuration snapshot.
- Good: an admin renames a project or credential in place; the stable project
  key and secret ID stay in the URL and configuration references.
- Base: a viewer reads project members, events, incidents, and audit data but
  sees no mutation controls.
- Bad: using fixture projects or a local role dropdown after authentication.
- Bad: removing the observed Session query before setting its data to `null`.

### 6. Tests Required

- Route-mock E2E must assert the exact project-scoped request path and body.
- Cover login, empty project list, configured project, missing configuration,
  admin mutations, viewer denial, and mobile navigation.
- When editing one field, assert unedited source/trigger config fields survive
  the `PUT configuration` round trip.
- Assert entered secret text is absent from rendered content after success.
- Cover project rename, name-only versus rotation payloads, incomplete Git
  replacement drafts, and missing viewer identity/credential edit controls.
- Unit-test that a business `401` retains an observed Session query with
  `null` data and removes project data. E2E-test the redirect to `/login`.

### 7. Wrong vs Correct

```tsx
// Wrong: local UI state pretends to be authorization.
const [role, setRole] = useState("admin");
const canEdit = role === "admin";

// Correct: authorization display follows the server projection.
const canEdit = project.capabilities.manageConfiguration;
```

```ts
// Wrong: removes the observer that the auth gate relies on.
client.removeQueries();

// Correct: notify Session observers, then clear only business state.
client.setQueryData(queryKeys.session, null);
client.removeQueries({ predicate: (query) => query.queryKey[0] !== "session" });
```
