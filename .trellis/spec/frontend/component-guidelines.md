# Component Guidelines

## Production Route Components

Route components resolve URL params, query the one resource they render, and
own the screen's loading, empty, error, and mutation states. Route selection
must be restorable from the URL; do not keep an active project, incident, or
create/edit mode only in component state.

```text
/projects
/projects/new
/projects/:projectKey/incidents/:incidentId
/projects/:projectKey/configuration/edit
```

`AuthenticatedRoute` owns session gating, `ProjectRoute` resolves the project
from the backend directory, and `AppShell` composes authenticated navigation.

## Shared Production Components

`shared/ui.tsx` owns controls or states with multiple production consumers,
including `IconButton`, `LoadingState`, `ErrorNotice`, `PageError`, and
`StatusPill`. Keep feature-specific tables and forms in their feature.

Props are explicitly typed at the component boundary:

```tsx
export function ErrorNotice({ message, onRetry }: {
  message: string;
  onRetry?: () => void;
}) { /* ... */ }
```

Do not add a generic component abstraction for a one-off layout.

## Authorization And Sensitive Data

- Render business actions from `project.capabilities`, not role comparisons.
- Role text may explain access but must not grant it.
- Secret plaintext may appear only in the active write control (password input
  or `ssh_private_key` textarea) and the outgoing request.
- After a successful secret write, clear the input, file picker, and import
  error, then render only returned metadata.
- Project identity edits live in the authenticated shell project switcher and
  are capability-gated by `manageConfiguration`. The project key is always
  read-only in the topbar. Viewers see the current name without an edit control.
  Configuration remains environment, Git, source, trigger, and LLM only.
- Extend `CredentialField` and `GitCredentialField` for create and edit. Do not
  add a third credential editor. `SshPrivateKeyDraftField` is a shared textarea
  plus file input used by those two editors, not a third editor. Create and
  edit modes are mutually exclusive. Edit mode shows the immutable kind,
  pre-fills only the display name, and starts sensitive inputs empty. Switching
  the selected credential, allowed kind set, Git transport, or closing edit
  mode clears every sensitive draft, file name, and import error.

## Styling

- Use stable class names and feature-owned CSS files.
- Shared controls use `shared/ui.css`; shell selectors use `layouts/AppShell.css`.
- Tokens and element resets live in `styles/tokens.css` and `styles/base.css`.
- Keep cards at 6px or less to match the operational console.
- Fixed navigation and control dimensions must not shift when labels or counts change.

## Accessibility

- Icon-only buttons require an accessible label and tooltip (`IconButton`).
- Loading states use `role="status"`; request failures use `role="alert"`.
- Active navigation uses `NavLink` state and semantic anchors.
- Form fields require accessible labels; native select, radio, and checkbox controls are preferred.
- Mobile navigation must close after route selection, and the stable state must not overflow the viewport.

## Prototype Reference Patterns

### Paired Diff Views

Keep review fixtures in the data module as paired rows, with an `original`
and `modified` line (or `null`) for every visual row. A diff component should
render both panes from the same selected file so additions and removals remain
vertically aligned. File selection belongs to the viewer's local state; it
does not need to be promoted outside the remediation view.

For narrow viewports, retain each code pane's horizontal scrolling and stack
the two panes rather than shrinking code until it becomes unreadable.

### Guided Configuration Flows

Keep new-project creation separate from editing an existing project. A create
flow owns its draft state and advances through explicit gates for identity,
repository, credentials, immutable baseline, evidence scope, and final review.
The Continue action is disabled until the current gate is satisfied.

Source configuration components may expose controlled `source` and trigger
values plus callbacks such as `onSourceChange` and `onVerified`. When a source
changes, the parent wizard must clear the prior verification state. Secret
inputs are cleared after local save and subsequent UI state may render only a
credential reference and permission summary.

The Git repository step chooses create fields from transport, not from a kind
select. HTTPS asks for username plus password/token and stores
`git_credential`. SSH asks for a private-key textarea, optional passphrase,
and a local `.pem` / OpenSSH file picker, then stores `ssh_private_key`.
Source `ssh_private_key` uses the same textarea plus file picker and does not
add a passphrase field. Other secret kinds stay on a single-line password
input. The existing-secret dropdown lists only kinds valid for the current
transport or source.

Production branch and deployed commit are not typed. The operator reads them
from the remote; branch is a select of returned heads and commit is read-only.

For project composition, model required Git separately from the selected
signal paths. Log source (`ssh`, `cloud`, `mcp`) and trigger mode (`webhook`,
`custom`) are each single nullable choices rendered as native radio groups.
Selecting a new value replaces the previous value and only its configuration
panel is mounted. The final review renders the selected source and trigger
labels.

The production Trigger step for `signed_webhook` shows a read-only inbound
URL plus copy and generate/regenerate. Do not render HMAC, event-types, or
deduplication fields. Do not let the operator edit the URL. Show the URL
only when the configuration API returned `trigger.inboundUrl` (project
admin). Viewers and operators never see the token. The Trigger block saves
independently through `PUT /api/v1/projects/{projectKey}/configuration/trigger`;
its own first save creates the token. Generate stays disabled until the saved
trigger kind is `signed_webhook`; a draft select is not enough. Generate/Regenerate
then calls `POST /api/v1/projects/{projectKey}/configuration/webhook-token` to
rotate it. A saved `custom_rule` is `400 invalid_request`. The editor reads
partial state from `/configuration/draft`, while the overview's complete
configuration remains unavailable until required component rows exist.
The browser path for a guided flow should assert both the blocked/ready state
of required gates and the absence of the entered secret from rendered text.

## Common Mistakes

- Importing prototype fixtures into a production route to fill a missing backend capability.
- Making a non-functional icon button appear actionable without a backend or UI contract.
- Letting a feature failure replace the entire shell instead of its route content.
- Collecting an `ssh_private_key` in a single-line password input. PEM text
  loses newlines and cannot be inspected.
- Uploading a private-key file as multipart or storing it as a blob. Read the
  file to text in the browser and send the existing secrets API value.
- Treating `SshPrivateKeyDraftField` as a third credential editor, or leaving
  an SSH create form open when the source kind or Git transport changes.
- Nesting route-owned business forms inside the app composition layer.

## Evidence-Aware Configuration Controls

When editing the project source or trigger controls for remediation evidence:

- A signed webhook uses a native provider control with `Generic webhook` and
  `Tencent Cloud CLS alert`. The Tencent choice explains that `DetailUrl` and
  multidimensional results are retrieved by the trusted backend adapter; it
  never renders a free-form URL fetch control or asks for `TopicId`.
- An SSH source uses a deployment control for `Host process` or `Docker`. Docker
  mode loads a bounded backend inventory through an explicit refresh and a
  native select. Each option shows exact name, image, and runtime status,
  including stopped/restarting entries; there is no free-form container name
  field or raw Docker output in browser state.
- Changing host, port, user, credential, deployment kind, or refresh identity
  clears stale container options and selection. Save is disabled until Docker
  has a returned exact name. Review/overview uses the saved name, never the
  ephemeral container ID.
