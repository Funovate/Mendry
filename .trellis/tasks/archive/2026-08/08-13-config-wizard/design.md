# Design: configuration wizard

## Architecture and boundaries

No backend, API, or `Configuration`/`ProjectSecret` schema changes. This is a client-side
decomposition of one React component (`ConfigurationEditor` inside
`frontend/src/features/configuration/ConfigurationEditorPage.tsx`) into a step shell plus five step
components plus one shared credential-field component:

```
frontend/src/features/configuration/
  ConfigurationEditorPage.tsx   # unchanged data-fetching wrapper (ConfigurationEditorPage)
                                # + new ConfigurationWizard replacing today's ConfigurationEditor
  ConfigurationPage.tsx         # unchanged (out of scope)
  configuration.ts              # unchanged (buildSourceConfig/buildTriggerConfig/readConfig*)
  configuration.css             # extended: wizard tab bar + credential-field styles
  wizard/
    CredentialField.tsx         # new: reference <select> + inline "+ New credential" mini-form
    RepositoryStep.tsx          # new
    SourceStep.tsx              # new
    TriggerStep.tsx             # new
    ReviewStep.tsx              # new
```

No `EnvironmentStep.tsx` — Environment is never rendered as editable input (see "Environment
defaults" below), so there is nothing to lift into its own step component.

`ConfigurationEditorPage` keeps its current job unchanged: fetch `current` configuration + `secrets`,
handle loading/error/redirect, then render `ConfigurationWizard` (renamed from `ConfigurationEditor`)
with the same `current`/`secrets`/`onCancel` props it passes today.

## State ownership

`ConfigurationWizard` keeps every existing `useState` hook from today's `ConfigurationEditor`
verbatim (environment/repository/source/trigger fields) — no state shape change, so
`buildSourceConfig`/`buildTriggerConfig`/`configurationOrNull` keep working unmodified. It adds one
new piece of state: `activeStep: StepId` (`"repository" | "source" | "trigger" | "review"`),
defaulting to `"repository"`.

### Environment defaults (no step)

`environmentKey`/`environmentName`/`service` remain plain `useState` fields (still needed to build
the `PUT /configuration` payload and to feed `ReviewStep`), but their *initializers* change from the
current hardcoded literals to the current project's own identity:

```ts
const project = useCurrentProject(); // already called in this component today
const [environmentKey] = useState(current?.environment.key ?? project.key);
const [environmentName] = useState(current?.environment.name ?? project.name);
const [service] = useState(current?.environment.service ?? "");
```

No setters are exposed anywhere in the UI (dropping `setEnvironmentKey`/`setEnvironmentName`/
`setService` entirely) — these three become effectively read-only for the lifetime of the wizard.
`ReviewStep` already renders `configuration.environment.{key,name,service}` read-only
(`ReviewStep.tsx:9-14`), so no change is needed there; it now doubles as the only place Environment
is visible at all.

It renders, in order:
1. A step tab bar (`role="tablist"`) — five buttons, always all enabled, `onClick` sets
   `activeStep` directly. No step is ever disabled or skipped based on validity (decision B: free
   jump navigation). The active tab gets `aria-selected="true"`.
2. Exactly one step component, chosen by `activeStep` — the others are not mounted. Each step
   receives only the slice of state/setters it needs as props (grouped by section, same values
   that exist today, just passed down instead of closed over inline).
3. A single shared footer (`<footer className="setup-footer">`), rendered by the wizard shell
   itself, **not** duplicated per step component — Cancel, Save, a save-blocked reason (see below),
   and the mutation error. This is what makes Save available "on every step" (decision D) without
   five copies of the same footer markup: the footer simply doesn't belong to any step's JSX.

Step tabs are plain client-side state, not URL-synced (`?step=...`) — no requirement calls for
shareable/bookmarkable step links, and adding that would be scope creep on a frontend-only,
same-session editing form.

## Credential creation: `CredentialField`

Replaces today's single `SecretSelect` used for the Git/Source/Trigger credential-reference
dropdowns. Combines the existing reference `<select>` with a collapsed-by-default inline creator,
matching the "inline expandable credential form" preview the user approved:

```tsx
<CredentialField
  label="Git credential reference"          // unchanged aria-label, existing tests keep working
  value={repositorySecretId}
  onChange={setRepositorySecretId}
  secrets={knownSecrets}                    // unfiltered, same as today — see "Reference-select
                                             // filtering" note below
  createLabelPrefix="Git"                   // scopes the mini-form's own labels
  allowedCreateKinds={GIT_CREDENTIAL_KINDS}
  onCreate={createCredential}               // parent-supplied, see below
  creating={createSecret.isPending}
  createError={createSecret.error}
/>
```

Internal state (`name`, `kind`, `value`, `expanded`) lives inside `CredentialField`, not in the
wizard — each mounted instance is independent and today's five module-level `secretName` /
`secretKind` / `secretValue` / `setSecretName` / etc. states are deleted entirely. A small
`"+ New credential"` toggle button sits directly under the reference `<select>`; clicking it reveals
three inputs (`${createLabelPrefix} credential name`, `${createLabelPrefix} credential type`,
`${createLabelPrefix} credential value`) and a submit button. On successful creation the field
calls `onChange(created.id)` (auto-select, per confirmed decision), clears `name`/`value`, and
collapses back down — no extra manual re-select step, which is the specific defect the user
originally flagged.

`allowedCreateKinds` per step (frontend-only guidance; backend does not enforce this — see
`ValidateConfiguration` in `project.go`, confirmed during investigation):

| Step | `allowedCreateKinds` |
|---|---|
| Repository | `git_credential`, `ssh_private_key`, `ssh_password` (shown together; not further split by `transport`, since the approved mock showed all three at once) |
| Source | `sourceKind === "ssh"` → `ssh_private_key`, `ssh_password`; `sourceKind === "mcp"` or `"cloud"` → `http_bearer`, `http_header` |
| Trigger | `webhook_hmac` only, and only rendered when `triggerKind === "signed_webhook"` (same conditional as today) |

**Reference-select filtering (explicit design decision, not previously asked):** only the
Trigger step's *existing* behavior of filtering the reference `<select>` to `kind === "webhook_hmac"`
is kept. The Repository/Source reference selects stay unfiltered (show every secret), matching
today's behavior exactly. The confirmed decision (Question C) was scoped to the **creation**
dropdown only ("+ New credential" → `Credential type`), not the reference dropdown. Filtering the
reference dropdown too is a reasonable follow-up but is new scope; flagging it here for review
rather than silently expanding what was asked.

## `createSecret` mutation: signature change

Today `createSecret`'s `mutationFn` closes over single module-level `secretName`/`secretKind`/
`secretValue` state. With up to three independent `CredentialField` instances mounted across steps
(never simultaneously, since only one step is active — but still each with its own local draft
state), the mutation must take its input as an argument instead:

```ts
const createSecret = useMutation({
  mutationFn: (input: { name: string; kind: ProjectSecret["kind"]; value: string }) =>
    api.createSecret(project.key, input),
  onSuccess: (created) => {
    queryClient.setQueryData<ListResult<ProjectSecret>>(queryKeys.secrets(project.key), (current) => ({
      items: [...(current?.items ?? []), created],
      total: (current?.total ?? 0) + 1,
    })); // unchanged cache-update logic
  },
});
const createCredential = (input: { name: string; kind: ProjectSecret["kind"]; value: string }) =>
  createSecret.mutateAsync(input);
```

`createCredential` (typed `(input) => Promise<ProjectSecret>`) is the single function passed as
`onCreate` to every `CredentialField` instance. No other mutation behavior changes (still write-only,
still `POST /secrets`, still independent of the configuration snapshot save).

## Save button, validation note, and Review step

`saveDisabledReason` is a small derived value computed once in `ConfigurationWizard`, replacing
today's inline `disabled={...}` boolean on the submit button with a human-readable reason shown next
to Save on every step:

```ts
const saveDisabledReason =
  sourceCapabilities.length === 0 ? "Select at least one collection capability." :
  triggerKind === "signed_webhook" && !signingSecretId ? "Select a webhook signing credential for the signed webhook trigger." :
  null;
```

`submit()` keeps its current logic (build the four-section payload, call `saveConfiguration.mutate`)
unchanged — it is now reachable from the shared footer regardless of `activeStep`, satisfying
decision D. To let `ReviewStep` show exactly what will be saved, the payload-building logic inside
today's `submit` is factored out into a local `buildConfigurationPayload()` closure (still inside
`ConfigurationWizard`, not exported — it closes over ~20 pieces of state, so an exported
free function taking that many parameters would be worse, not better). `submit` calls
`saveConfiguration.mutate(buildConfigurationPayload())`; `ReviewStep` receives the same
`buildConfigurationPayload()` result as a prop and renders it read-only (env/repo/source/trigger
summary, same shape `ConfigurationPage.tsx` already knows how to summarize, but inline in the
wizard so the user can confirm before saving without leaving the edit flow). Review has no fields of
its own and does not duplicate the shared footer — it reuses it like every other step.

## CSS additions (`configuration.css`)

Kept consistent with existing tokens/classes (`--radius-panel`, `--radius-control`, `#238173`
accent, `.setup-footer` border-top convention):

- `.wizard-steps` — flex row of tab buttons above `.real-config-form`.
- `.wizard-step-tab` / `.wizard-step-tab.active` — button styling, active tab gets the existing
  accent color (`#238173`/`#087468` family) instead of introducing a new palette.
- `.credential-field` — wraps the existing `<select>` + a `.credential-field-toggle` button.
- `.credential-inline-form` — the revealed mini-form, reusing `.source-form`'s two-column grid for
  name/type and a full-width row for value + submit, so it looks like a smaller `.secret-manager`
  section rather than a new visual language.

## Compatibility / migration

- No API contract change: still one `PUT /configuration` call with the same payload shape, still
  independent `POST /secrets` calls.
- `configuration.ts` helpers are untouched and reused as-is.
- Existing reference-select aria-labels ("Git credential reference", "Source credential reference",
  "Webhook signing credential") are preserved unchanged so any code depending on them (only the e2e
  suite) needs updates solely where the *flow* changed (step navigation, mini-form field labels),
  not because a stable label was renamed.
- `frontend/tests/application.spec.ts` must be updated (see `implement.md`) since the single
  continuous fill-in-order flow no longer matches a step-gated UI. This is expected and listed as an
  acceptance criterion in `prd.md`.

## Trade-offs

- Removing Environment as an editable step (instead of keeping it and just pre-filling defaults)
  trades away the ability for a user to pick a custom environment key/name/service through this
  wizard at all. Given the schema only supports one Environment per project today (`UNIQUE
  (project_id)` on `project_environments`), and `key`/`name` duplicating the project's own identity
  was the actual source of user confusion (why type something that already exists on the Project?),
  this is judged worth it — a future "manage environments" feature (if multi-environment support is
  ever built) is the natural place to reintroduce an editable environment key/name/service, not this
  wizard.

- Prop-drilling per step (rather than a context/reducer) is chosen because state shape doesn't
  change from today (still flat `useState`s) and each step only ever needs a handful of them —
  introducing Context here would be new machinery solving a problem (prop count) that isn't actually
  large enough to justify it yet.
- Mounting only the active step (vs. rendering all steps and hiding with CSS) avoids the aria-label
  ambiguity risk entirely (no two mini-forms can ever coexist in the DOM) and matches the "one focus
  at a time" interaction model — the cost is that step-switch does not preserve scroll position
  within a step, which is acceptable for a form of this size.
- `buildConfigurationPayload()` stays an inline closure instead of an exported helper because it
  would need ~20 parameters to be pure/exported; a closure over local state is simpler and the
  function has exactly one real caller context (this wizard).

## Rollback

Single self-contained frontend change; no data migration, no backend deploy coupling. Revert is a
plain `git revert` of the commit(s) touching `frontend/src/features/configuration/**` and
`frontend/tests/application.spec.ts` — no other module depends on the internals of
`ConfigurationEditor`/`ConfigurationWizard` (grep confirms no other file imports it).
