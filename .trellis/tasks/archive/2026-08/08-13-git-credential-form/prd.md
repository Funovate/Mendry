# Structure Git credential form by transport

## Goal

The project configuration Git step asks for familiar HTTPS and SSH credentials
instead of a generic name/type/value blob. The backend secret contract stays
`name + kind + value`; only the form composition changes.

## Background

`RepositoryStep` reuses `CredentialField`, which always renders one name field,
one kind select, and one password input. Allowed kinds are `git_credential`,
`ssh_private_key`, and `ssh_password`, independent of the selected Git
transport. The prototype in `frontend/src/App.tsx` already splits HTTP username
plus password/token from an SSH private-key textarea. Secret responses remain
metadata only and must never echo the stored value.

## Requirements

- R1. HTTPS transport shows username plus password/token fields. Saving creates
  a `git_credential` secret whose value is `username:secret` when a username is
  present, otherwise the token alone.
- R2. SSH transport shows a private-key textarea and optional passphrase. Saving
  creates an `ssh_private_key` secret. A passphrase is appended only when
  provided; the stored value remains a single opaque blob.
- R3. The Git kind select is removed. Transport chooses the secret kind and
  visible fields.
- R4. Existing saved secrets stay selectable by name. The dropdown filters to
  kinds valid for the current transport: HTTPS uses `git_credential`; SSH uses
  `ssh_private_key` and `ssh_password`.
- R5. Changing transport clears an incompatible selected secret id so HTTPS
  cannot keep an SSH secret and the reverse.
- R6. After a successful store, inputs clear and the raw secret is not rendered.
- R7. Source and webhook credential forms keep the generic `CredentialField`.
- R8. E2E covers creating one HTTPS Git credential and one SSH private key, and
  asserts the POST body kind/value plus that the secret text is not left on
  screen.

## Acceptance Criteria

- [x] HTTPS Git create form has username and password/token, not a kind select.
- [x] SSH Git create form has a private-key textarea and optional passphrase.
- [x] Store HTTPS credential POSTs `kind: "git_credential"`.
- [x] Store SSH key POSTs `kind: "ssh_private_key"`.
- [x] Secret values disappear from the form after store.
- [x] Switching transport hides incompatible existing secrets.
- [x] Source/webhook credential UI is unchanged.
- [x] Frontend lint, typecheck, unit, build, and E2E pass.

## Out of Scope

- New backend secret kinds or structured secret JSON
- Changing encryption, list, or configuration APIs
- SSH password-only Git login as a first-class create form
- Auto-detecting transport from the remote URL
