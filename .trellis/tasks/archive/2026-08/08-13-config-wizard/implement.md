# Implementation plan: configuration wizard

Frontend-only. No files under `backend/` should change; verify with `git status`/`git diff --stat`
before committing.

## Ordered checklist

1. **`configuration.css`** — add `.wizard-steps`, `.wizard-step-tab` (+ `.active`),
   `.credential-field`, `.credential-field-toggle`, `.credential-inline-form`, and matching
   `@media (max-width: 720px)` rules, per `design.md`'s CSS section.

2. **`ConfigurationEditorPage.tsx` — mutation signature.** Change `createSecret`'s `mutationFn` to
   accept `{ name, kind, value }` as an argument instead of reading `secretName`/`secretKind`/
   `secretValue` from closure state. Delete those three `useState` hooks and their setters — they
   move into `CredentialField`. Add `createCredential = (input) => createSecret.mutateAsync(input)`.

3. **New `wizard/CredentialField.tsx`.** Props: `label`, `value`, `onChange`, `secrets`, `required?`,
   `createLabelPrefix`, `allowedCreateKinds: { value: ProjectSecret["kind"]; label: string }[]`,
   `onCreate: (input) => Promise<ProjectSecret>`, `creating`, `createError?`. Renders the existing
   `<select>` markup (same as today's `SecretSelect`) plus a collapsed-by-default inline creator
   toggled by a `"+ New credential"` button. On successful create: call `onChange(created.id)`,
   clear local `name`/`value`, collapse. Delete the old `SecretSelect` function once every call site
   is migrated (step 6).

4. ~~**New `wizard/EnvironmentStep.tsx`.**~~ **Superseded — see step 11.** Environment is no longer a
   step at all; do not create this file (or delete it if it already exists from an earlier pass).

5. **New `wizard/RepositoryStep.tsx`.** Lift the Git repository `<section>` JSX; replace its
   `SecretSelect label="Git credential reference"` with `CredentialField` using
   `createLabelPrefix="Git"` and `allowedCreateKinds = [git_credential, ssh_private_key,
   ssh_password]` (see `design.md` table). Props: remote URL / scmProvider / transport /
   repositorySecretId / productionBranch / deployedCommit values + setters, `knownSecrets`,
   `createCredential`, `createSecret.isPending`, `createSecret.error`.

6. **New `wizard/SourceStep.tsx`.** Lift the Collection source `<section>` JSX (including all
   per-kind conditional fields and the capability `<fieldset>`); replace its `SecretSelect` with
   `CredentialField` using `createLabelPrefix="Source"` and `allowedCreateKinds` computed from the
   current `sourceKind` per the design table (`ssh` → private-key/password; `mcp`/`cloud` →
   bearer/header). Same prop shape pattern as step 5, scoped to source fields.

7. **New `wizard/TriggerStep.tsx`.** Lift the Trigger `<section>` JSX; keep the existing
   `signed_webhook`/`custom_rule` conditional. Replace the webhook-signing `SecretSelect` with
   `CredentialField` (`createLabelPrefix="Webhook"`, `allowedCreateKinds = [webhook_hmac]`, `secrets`
   still pre-filtered to `kind === "webhook_hmac"` exactly as today, `required`).

8. **New `wizard/ReviewStep.tsx`.** Read-only. Takes the `buildConfigurationPayload()` result
   (typed `ProjectConfiguration`-shaped object) as a single prop and renders a summary of all four
   sections (reuse the descriptive style of `ConfigurationPage.tsx`'s table where it fits, but this
   stays inside the edit flow — do not navigate away). No inputs, no mutation calls of its own.

9. **`ConfigurationEditorPage.tsx` — wizard shell.** *(Superseded in part by step 11: `activeStep`
   defaults to `"repository"`, not `"environment"`, and there are four tabs, not five — apply step 11
   on top of this step.)* Rename `ConfigurationEditor` → `ConfigurationWizard`. Add `activeStep` state. Extract
   `buildConfigurationPayload()` from today's `submit` body (same logic, now callable from both
   `submit` and the `ReviewStep` prop). Render, in order: the step tab bar (five buttons, always
   enabled, `role="tablist"`/`aria-selected`), the single active step component (pass only the props
   it needs), and the shared footer (`Cancel`, `Save configuration` — always enabled to click but
   showing `saveDisabledReason` text when non-null and kept `disabled` on that condition exactly as
   today's boolean did, `ErrorNotice` for `validationError`/mutation errors). Delete the old
   `secret-manager` bottom section and the four inline `<section>` blocks now that they live in step
   files.

10. **Rewrite `frontend/tests/application.spec.ts`.** Update the two affected tests
    (`:252-259`, `:261-297`):
    - Keep the empty-state test's assertions on the entry button unchanged (it doesn't touch the
      form).
    - Rewrite the persistence test's configuration portion: click the `"Repository"`-equivalent
      step tab, expand `"+ New credential"`, fill `"Git credential name"` / select
      `"Git credential type"` / fill `"Git credential value"`, submit the mini-form, assert the
      credential now appears selected in `"Git credential reference"`. Then navigate the Trigger
      step tab, fill trigger fields, use the Trigger step's own `CredentialField` (or select an
      already-created `webhook_hmac` secret if one exists from an earlier step) for
      `"Webhook signing credential"`, then click `"Save configuration"` from whichever step is
      active (do this from a non-Review step at least once, to cover decision D). Keep the final
      assertions on `state.writes` (`POST /secrets`, `PUT .../configuration`) unchanged — the wire
      contract didn't change, only how the user gets there.
    - Add a small new assertion that clicking a step tab switches the visible fields to cover the
      "only active step mounted" requirement — **see step 11** for the field pair to use (Environment
      fields no longer exist to assert on).

11. **Remove the Environment step (this revision — apply after/instead of steps 4 and 9's step list).**
    - Delete `frontend/src/features/configuration/wizard/EnvironmentStep.tsx`.
    - In `ConfigurationEditorPage.tsx`: drop the `EnvironmentStep` import; remove `"environment"` from
      the `StepId` union and from the `STEPS` array (four tabs left: Git repository, Collection
      source, Trigger, Review); remove the `activeStep === "environment" && <EnvironmentStep .../>`
      render branch; change `useState<StepId>("environment")` to `useState<StepId>("repository")`.
    - Change the three environment field initializers to drop their setters and derive from the
      project instead of hardcoded literals:
      ```ts
      const [environmentKey] = useState(current?.environment.key ?? project.key);
      const [environmentName] = useState(current?.environment.name ?? project.name);
      const [service] = useState(current?.environment.service ?? "");
      ```
      (`project` is already in scope via `useCurrentProject()`.) `buildConfigurationPayload`'s
      `environment: { key: environmentKey.trim(), ... }` line is unchanged — it already reads these
      three variables.
    - No change to `ReviewStep.tsx` — it already renders Environment's key/name/service read-only.
    - Update `application.spec.ts:287,290` (see step 10): replace the two
      `page.getByLabel("Environment key")` assertions with `page.getByLabel("Git remote URL")` (the
      new default-tab field), since the wizard now lands on "Git repository" first, not "Environment".
      Everything else in that test file is unaffected — `mockApi`'s fixture at `:29` still returns a
      full `environment` object, it's just never rendered as an input anymore.

## Validation commands

Run from `frontend/`:

```bash
npm run typecheck
npm run lint
npm run test        # vitest unit tests, if any cover this feature
npm run test:e2e     # playwright — includes the rewritten application.spec.ts
```

Then manually: `npm run dev`, open the app, navigate to an existing project's
`configuration/edit`, and click through all five step tabs, create a credential inline from at
least the Repository step, confirm auto-select, and save — per the repo-wide rule that UI changes
must be exercised in a real browser, not just type-checked.

## Risky files / rollback points

- `ConfigurationEditorPage.tsx` is being substantially rewritten — keep the diff reviewable by doing
  steps 4–8 (extracting step components) as separate commits/checkpoints before step 9 (shell
  rewrite) so a bad extraction is easy to bisect.
- `frontend/tests/application.spec.ts` rewrite is the highest-risk step for silently losing coverage
  (e.g. dropping the assertion on `state.writes`) — diff it against the original test carefully
  rather than deleting and re-writing from scratch.
- No backend files should be touched; `git status` before commit should show changes only under
  `frontend/src/features/configuration/**` and `frontend/tests/application.spec.ts`.
