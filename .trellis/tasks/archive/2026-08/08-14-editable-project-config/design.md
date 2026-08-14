# Design: Editable project configuration

## Scope And Boundaries

This task adds two project-admin mutations to the existing projects module:

1. Rename an existing project while keeping its stable key unchanged.
2. Rename or rotate an existing project credential while keeping its ID and
   kind unchanged.

The work stays inside the existing project domain/application/PostgreSQL/HTTP
module and the frontend project/configuration features. The two deliverables
share the same authorization, API DTO, query-cache, audit, and configuration
UI surfaces, so they remain one task rather than separate child tasks.

No schema migration is required. The current `projects`,
`project_environments`, `project_secrets`, and `audit_events` tables already
contain the fields and version columns needed for in-place updates.

## HTTP Contracts

### Rename project

```http
PATCH /api/v1/projects/{projectKey}
Content-Type: application/json

{"name":"New project name"}
```

The response is the existing safe project DTO. The project key, description,
role, and capabilities are unchanged. Invalid names return the existing
`400 invalid_request`; non-admin project members receive `403 forbidden`.

### Update credential

```http
PATCH /api/v1/projects/{projectKey}/secrets/{secretId}
Content-Type: application/json

{"name":"New display name"}
```

For rotation, the request also includes a complete replacement value:

```json
{"name":"New display name","value":"replacement secret material"}
```

Omitting `value` preserves the current ciphertext, nonce, and key version.
Supplying a non-empty value validates and encrypts it using the existing
secret ID and kind, then atomically replaces the encrypted material. An empty
supplied value is invalid. The response remains the existing safe secret DTO;
plaintext, ciphertext, and nonce are never returned.

The kind is not accepted in the update request and therefore cannot change.
Project and credential update responses increment their resource `version`.

## Backend Flow

### Project rename

`Service.UpdateProjectName` resolves the project through `requireAdmin`, trims
and validates the new name against the existing `domain.Project`, allocates an
audit ID, and calls the repository.

One sqlc query performs the mutation atomically:

1. Read the existing project name.
2. Update `projects.name`, `version`, and `updated_at`.
3. Update the single `project_environments` row only when its current name
   equals the old project name; distinct environment names are preserved.
4. Insert a `project.renamed` audit event without secret or high-cardinality
   request data.
5. Return the updated project row.

### Credential update

`Service.UpdateSecret` resolves admin access and loads the project-scoped
encrypted secret. Domain validation is split so metadata can be validated
without requiring replacement plaintext. For rotation, the service validates
the complete replacement value and encrypts it with the same project ID,
secret ID, and kind. For a name-only edit, it retains the loaded encrypted
fields unchanged.

One sqlc query updates `project_secrets`, increments `version`, and inserts a
`project.secret.updated` audit event. Audit metadata contains only the safe
name, kind, and a `rotated` boolean. The unique project/name constraint keeps
rename conflicts consistent with credential creation.

## Frontend Flow

### Project identity

`ConfigurationPage` renders an unframed `Project identity` section above the
configuration summary, including the editable project name and read-only key.
The edit control is capability-gated by `manageConfiguration`; other roles see
read-only values.

On success, the mutation replaces the matching item in the `projects` query
cache. Because `ProjectRoute` derives context from that list, the sidebar,
breadcrumbs, headers, and project directory update without navigation. The
configuration query is invalidated so a conditionally synchronized
environment name is fetched from the server.

### Credential fields

Both credential field implementations gain an edit action for the currently
selected secret:

- `CredentialField` covers collection source and webhook credentials.
- `GitCredentialField` covers HTTPS Git credentials plus SSH private-key and
  SSH-password credentials.

Edit mode pre-fills only the safe display name and shows the immutable kind.
Sensitive inputs start empty. Saving with all sensitive inputs empty omits
`value`; entering replacement material sends the same complete encoded value
used by the corresponding create form. Successful updates replace the item in
the project-scoped secrets cache without changing selection.

Create and edit modes are mutually exclusive within a field. Switching the
selected credential or closing edit mode clears every sensitive draft value.

## Compatibility And Failure Behavior

- Existing project URLs and API paths remain valid because project keys do not
  change.
- Existing repository/source/trigger references remain valid because secret
  IDs and kinds do not change.
- Existing create/list/get/configuration behavior and response shapes remain
  compatible.
- A failed mutation keeps the persisted value and exposes the existing safe
  API error message in the form.
- Concurrent writes follow the module's current last-write-wins convention;
  each successful update still advances the server version.

## Validation

Backend unit tests cover authorization, validation, name-only credential
updates, rotation encryption, safe HTTP responses, and audit inputs. The
existing tagged PostgreSQL integration suite exercises both repository updates,
conditional environment synchronization, reference preservation, persisted
versions, and safe audit metadata. Frontend API and route-mocked Playwright
tests cover request shapes, cache-visible project rename, all three credential-
reference contexts, write-only fields, and viewer access.
