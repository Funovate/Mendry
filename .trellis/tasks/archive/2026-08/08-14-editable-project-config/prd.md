# Enable credential and project name editing

## Goal

Allow users to correct previously saved project configuration instead of being
locked into the values chosen during initial setup.

## Background

The current configuration UI exposes two one-way setup flows:

- A Git credential can be added, but an existing credential cannot be edited.
- A project name cannot be changed after the project has been created.

Repository inspection confirms that both gaps span the API, application, and
persistence layers rather than being hidden frontend controls:

- The project module exposes project create/list/get operations but no update
  operation (`backend/internal/modules/projects/application/service.go`,
  `backend/internal/modules/projects/adapter/http/handler.go`).
- Project secrets expose create/list operations but no update operation in the
  same files. Secret list responses intentionally omit plaintext, ciphertext,
  and nonce values.
- The configuration wizard exposes `New credential` beside each credential
  reference. The Git-specific form is implemented in
  `frontend/src/features/configuration/wizard/RepositoryStep.tsx`.
- Project configuration persists an environment name separately. Existing
  configuration reuses that persisted name, while first-time configuration
  derives it from the project name.

## Requirements

- Users can open an existing credential from every configuration reference
  field (Git repository, collection source, and webhook trigger), modify its
  editable fields, and save the updated credential.
- Credential updates preserve the existing secret ID so repository, source, or
  trigger references to that ID remain valid.
- Existing secret material remains write-only and is never returned to or
  prefilled by the frontend. A user supplies complete replacement material
  when rotating a credential.
- A credential display name can be changed without rotation. An omitted
  replacement value preserves the existing encrypted material; a supplied
  value replaces it atomically.
- Credential kind is immutable during editing so existing configuration
  references cannot become incompatible with their field.
- Users can modify the name of an existing project and save the new name.
- The project `Configuration` page contains a `Project identity` section. It
  shows the stable key read-only and lets project administrators enter an
  explicit edit mode for the name; other roles see read-only identity data.
- Project rename leaves the stable project key and its API/browser paths
  unchanged.
- When an existing configuration environment name still equals the old project
  name, project rename synchronizes it to the new name. A distinct environment
  name is preserved as an intentional legacy/custom value.
- Only project administrators can update project identity or credentials,
  matching the existing configuration-write permission.
- Successful edits are persisted and remain visible after the relevant data is
  reloaded.
- Existing create, selection, and configuration behavior continues to work.

## Acceptance Criteria

- [ ] Existing Git repository, collection source, and webhook trigger
  credentials each have an edit entry point beside their reference field.
- [ ] Saving valid changes updates that credential rather than creating a
  duplicate credential.
- [ ] Credential edit responses and subsequent list responses do not contain
  plaintext, ciphertext, nonce, or any previously saved secret value.
- [ ] Saving a name-only credential edit preserves its encrypted value and key
  version; saving replacement material re-encrypts it and advances its stored
  version without changing its ID or kind.
- [ ] Existing configuration references retain the same credential ID after a
  credential update.
- [ ] An existing project name has an edit entry point.
- [ ] The rename entry point is in `Configuration` > `Project identity`, is
  available to project administrators, and leaves the project key read-only.
- [ ] Saving a valid project name change updates the existing project rather
  than creating a new project.
- [ ] Renaming a project does not change its stable project key or route.
- [ ] Renaming a project synchronizes an environment name derived from the old
  project name, while preserving a distinct environment name.
- [ ] Reloading the affected view shows the saved credential metadata and
  project name.
- [ ] Focused automated tests cover both update flows and their persistence.

## Out Of Scope

- Redesigning unrelated project configuration steps.
- Changing transport, source, or trigger authentication semantics beyond
  making existing credentials editable.
- Editing the stable project key.

## Notes

- Repository inspection found new cross-layer update contracts are required,
  so this is a complex task and will include `design.md` and `implement.md`.
