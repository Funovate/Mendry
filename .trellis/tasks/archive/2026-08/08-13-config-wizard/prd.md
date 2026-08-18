# Project configuration page redesign: step-by-step wizard

## Goal

Replace the single giant `ConfigurationEditorPage` form with a guided, step-by-step wizard so a user
configuring a project's Environment, Git repository, Collection source, and Trigger is never left
guessing what to fill in next or where to find a related control (most acutely: credential creation,
which today lives in an unrelated section far below the fields that reference it).

## Background / Confirmed Facts

- File in scope: `frontend/src/features/configuration/ConfigurationEditorPage.tsx` (currently one
  React component, one `<form>`, ~130 lines, four config sections + one secret-manager section,
  all committed to backend state in a single submit).
- Read-only summary page `frontend/src/features/configuration/ConfigurationPage.tsx` is **not**
  in scope — user's complaint was specifically about the edit page.
- Helper module `frontend/src/features/configuration/configuration.ts` holds pure functions
  (`buildSourceConfig`, `buildTriggerConfig`, `readConfig*`) — reusable as-is.
- Backend contract (`backend/internal/modules/projects/domain/project.go:114-119,195-239`):
  `Configuration` = `{ Environment, Repository, Source, Trigger }`, validated and persisted as
  **one transactional snapshot** via a single `PUT /api/v1/projects/{key}/configuration`
  (`frontend/src/api.ts:264-265`). There is no partial/per-section save on the backend — the
  wizard's step transitions must be client-side only; only the final step calls `putConfiguration`.
- Secrets are a separate resource: `POST /api/v1/projects/{key}/secrets` (`api.ts:261-262`),
  created independently and referenced by opaque `credentialSecretId` from `Repository`, `Source`,
  and `Trigger`. Secret values are write-only (never re-fetched/decrypted for display).
- Secret kinds (`project.go:46-55`): `ssh_password`, `ssh_private_key`, `http_bearer`,
  `http_header`, `webhook_hmac`, `git_credential`. The backend does **not** restrict which kind may
  be referenced by which config section (`ValidateConfiguration` only checks the reference is a
  valid UUIDv7) — any kind-to-context filtering is a frontend UX affordance only, not a backend rule.
- Current known UX defect (why the user calls it "unreasonable"): the "Git credential reference"
  / "Source credential reference" dropdowns sit near the top of the form, but the only way to
  create a secret is the "Add credential reference" block at the very bottom, past two unrelated
  sections (Collection source, Trigger). Nothing on the page hints that this is required, or that
  switching Transport to `ssh` implies a specific credential kind is needed.
- No existing wizard/stepper/modal/dialog component exists anywhere in `frontend/src` today
  (`grep` for wizard/stepper/step-index came back empty; `src/shared/ui.tsx` only exports
  `IconButton`, `LoadingState`, `ErrorNotice`, `PageError`, `StatusPill`). This is a net-new UI
  pattern for this codebase.
- Existing e2e coverage that exercises this page in one continuous, non-stepped flow and **will
  need updating** for a multi-step layout: `frontend/tests/application.spec.ts:261-297`
  (`"administrator persists credentials, configuration, members, and incident lifecycle"`), plus
  `:252-259` (`"distinguishes an existing project with no configuration..."`) which only checks the
  entry button, not the form.

## Requirements

- Split the current single form into a step-based wizard with steps, in this order: Git repository,
  Collection source, Trigger, Review. Steps are tabs/labels the user can click in any order at any
  time (no forced linear Next/Back gate) — this is an edit form for an already-configured project
  most of the time, not a mandatory first-run-only funnel.
- Environment is **not** a wizard step and is never shown as editable input. Its three fields are
  derived automatically and submitted as part of the same snapshot, exactly as before:
  - `key` / `name`: when editing an existing configuration, reuse the persisted
    `current.environment.key` / `current.environment.name` unchanged; for a project with no
    configuration yet, default to the current project's own `key` / `name`
    (`useCurrentProject()` — already available in `ConfigurationEditorPage.tsx`). This is safe
    because `Project.Key` already satisfies the stricter `projectKeyPattern`, which is a subset of
    `environmentKeyPattern` (`project.go:133-134`), and `Project.Name` is bounded 1–120, same as
    `Environment.Name`.
  - `service`: keeps its existing default (empty/`null`) — never prompted for.
  - This is a pure frontend UX simplification, not a backend/schema change: the same three string
    fields are still computed and sent in the `PUT /configuration` payload, just no longer typed by
    the user. `ValidateConfiguration` (`project.go:195-200`) is untouched.
  - The Review step keeps showing Environment's Key/Name/Service read-only (it already does,
    `ReviewStep.tsx:9-14` — no change needed there) so the user can see what will be saved without
    being able to edit it from this wizard.
- Credential (secret) creation is **inline within each step that references one**, not a separate
  dedicated step and not relocated to the bottom of the page:
  - Git repository step: its own "+ New credential" mini-form, `Credential type` limited to
    `git_credential`, `ssh_private_key`, `ssh_password`.
  - Collection source step: its own "+ New credential" mini-form, `Credential type` limited by the
    selected source kind (`ssh` → `ssh_private_key`/`ssh_password`; `mcp`/`cloud` →
    `http_bearer`/`http_header`).
  - Trigger step: its own "+ New credential" mini-form, `Credential type` fixed to `webhook_hmac`
    (only relevant when Trigger type is `signed_webhook`).
  - This kind filtering is frontend guidance only — the backend does not restrict which secret kind
    a `credentialSecretId` reference may point to (`ValidateConfiguration` only checks UUIDv7
    shape), so no backend change is implied.
  - After a credential is created successfully inside a step, that step's own credential-reference
    dropdown auto-selects the newly created secret (removes the extra manual re-select the current
    page requires).
- `Save configuration` submits the whole snapshot (`PUT /configuration`, unchanged single
  transactional call) and is available from **every** step's footer, not just Review — consistent
  with free step-jump navigation (a user jumping straight to Trigger to fix one field should be able
  to save from there). Review is an optional step offering a full read-back summary before saving,
  not a required gate.
- Save button validation/disablement mirrors today's rule (source capabilities non-empty; signed
  webhook trigger requires a `webhook_hmac` signing credential) and must surface *which* condition
  is unmet near the button when disabled, regardless of which step is currently active.
- Preserve today's all-fields-editable-with-current-values-as-defaults behavior when editing an
  existing configuration, **except** Environment, which is no longer user-editable in this wizard
  (see above) — its persisted values are simply carried forward unchanged.
- Preserve existing behavior: secret values are cleared from their mini-form immediately after a
  successful create and are never displayed again (write-only).
- Only one step's fields are mounted in the DOM at a time (the active step) — this both matches the
  "no forced order but one focus at a time" interaction and avoids duplicate `aria-label` collisions
  across steps that each have their own "Credential name"/"Credential type"/"Credential value"
  mini-form (labels should be step-scoped, e.g. "Git credential name", to stay unambiguous for
  screen readers and `getByLabel` lookups even if two mini-forms were ever visible together).
- Update `frontend/tests/application.spec.ts` (`:252-259`, `:261-297`) to drive the new step
  navigation instead of the old single continuous form fill.

## Out of Scope

- Backend changes (validation, storage, API shape) — this is a frontend-only redesign.
- The read-only `ConfigurationPage.tsx` summary view.
- Restructuring the `Configuration` data model itself (still one Environment + one Repository +
  one Source + one Trigger per project).

## Acceptance Criteria

- [x] Editing an existing configuration shows four clickable steps (Git repository, Collection
      source, Trigger, Review), defaulting to Git repository; clicking any step's label switches to
      it regardless of whether other steps are "complete", and only the active step's fields are in
      the DOM. Environment has no step and no input fields anywhere in the wizard.
- [x] Environment key/name/service are still included in the submitted `PUT /configuration` payload:
      unchanged from `current.environment.*` when editing, or defaulted to the project's own
      `key`/`name` (empty `service`) when configuring for the first time. The Review step shows
      these three values read-only.
- [x] Each of Git repository / Collection source / Trigger has its own inline credential-creation
      mini-form (name, type, value, submit) that does not require navigating elsewhere; the
      `Credential type` options shown are limited to the kinds listed above for that step.
- [x] Creating a credential from a step's mini-form clears the mini-form, adds it to that step's
      credential-reference dropdown, and auto-selects it.
- [x] `Save configuration` appears on every step and successfully performs the existing single
      `PUT /configuration` call with the same payload shape as today; the Review step shows a
      read-only summary of all four sections plus the same button.
- [x] Save is disabled with a visible reason when source capabilities are empty or a signed webhook
      trigger has no `webhook_hmac` signing credential selected, matching current validation.
- [x] `frontend/tests/application.spec.ts` passes with the wizard flow (updated selectors/steps).
- [x] No backend files change.
