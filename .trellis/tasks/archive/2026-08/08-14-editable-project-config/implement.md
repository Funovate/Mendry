# Implement: Editable project configuration

## Backend

1. Extend project-domain validation so secret metadata can be validated for a
   name-only update while create/rotation still require valid plaintext.
2. Add project-name and credential-update methods to the application service
   and repository interfaces. Enforce project-admin access, preserve project
   key and secret ID/kind, and encrypt only supplied replacement material.
3. Add sqlc `UpdateProjectName` and `UpdateProjectSecret` queries. Increment
   versions, conditionally synchronize an environment whose name equals the
   old project name, and write safe audit events atomically.
4. Regenerate the checked-in project sqlc package with `make generate` from
   `backend/` and update the PostgreSQL repository mappings/error handling.
5. Register the two `PATCH` routes, decode the narrow request DTOs, clear any
   replacement plaintext after use, and return only existing safe response
   DTOs.
6. Add focused domain/application/HTTP tests for validation, permission gates,
   identity preservation, encryption behavior, and non-disclosure. Extend
   `backend/tests/integration/postgres_test.go` to reload both updates from
   PostgreSQL and verify conditional environment sync, preserved references,
   version increments, and safe audit metadata.

## Frontend

7. Add typed `updateProjectName` and `updateSecret` calls in `src/api.ts`, with
   API-boundary tests for methods, paths, optional value omission, and response
   validation.
8. Add the capability-gated `Project identity` edit surface to
   `ConfigurationPage`. Update the projects cache on success and invalidate
   configuration so conditional environment synchronization becomes visible.
9. Extend the generic `CredentialField` with mutually exclusive create/edit
   modes, an immutable kind display, optional rotation input, draft clearing,
   and safe errors for Source and Webhook references.
10. Extend `GitCredentialField` with the same edit lifecycle and kind-specific
    replacement fields for `git_credential`, `ssh_private_key`, and
    `ssh_password`.
11. Add the update mutation to `ConfigurationEditorPage`, replace updated
    secrets in the scoped query cache, and thread the update callback/status to
    Repository, Source, and Trigger steps.
12. Update feature CSS with responsive, stable controls that match the current
    configuration UI and keep viewer identity data read-only.
13. Extend route-mocked Playwright coverage for project rename, conditional
    environment refresh, Git/Source/Webhook credential edits, name-only versus
    rotation payloads, secret non-disclosure, and missing viewer controls.

## Validation Gates

14. From `backend/`, run `make generate-check`, `make fmt`, `make vet`,
    `make test`, `make test-race`, and `make build-check` (or the equivalent
    `make check` plus `make generate-check`). Run the tagged PostgreSQL
    integration suite when the repository's isolated test endpoint is
    available; otherwise report that environmental gap explicitly.
15. From `frontend/`, run `npm run lint`, `npm run typecheck`, `npm test`,
    `npm run build`, and `npm run test:e2e`.
16. Run `git diff --check`, review that no response/log/audit payload contains
    credential material, and visually inspect the Configuration page at
    desktop and mobile widths.

## Rollback Points

- The new routes are additive; removing their registration and frontend calls
  restores the previous behavior without data migration.
- SQL changes update existing rows only. No schema rollback is needed.
- Stop and repair before continuing if sqlc regeneration changes modules
  outside the projects query package.
