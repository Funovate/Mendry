# Read Git branch and commit from remote

## Goal

The Git repository step no longer asks operators to type a production branch
or deployed commit. Those values are read from the remote with the selected
credential and shown as resolved data.

## Background

`PUT /configuration` still requires `productionBranch` and a hex
`deployedCommit`. The editor currently exposes both as free-text inputs.
Secret plaintext never returns to the browser, so the frontend cannot call
`git ls-remote` itself. The prototype already treated the commit as resolved
metadata after a fetch.

## Requirements

- R1. The Git step has no free-text branch or commit inputs.
- R2. After a remote URL and Git credential are present, the UI can read
  remote refs. The default branch and its HEAD commit fill the displayed
  baseline.
- R3. Branch choices come from the remote ref list. Changing the selected
  remote branch updates the displayed commit from that probe result.
- R4. The backend decrypts the selected project secret, lists refs, and
  never returns the secret value.
- R5. Probe failures name a stable error without remote credentials, raw
  secret values, or git command lines.
- R6. Saving configuration still sends `productionBranch` and
  `deployedCommit`; those values must come from the last successful probe.
- R7. Save stays disabled until a probe has produced a valid branch and
  commit for the current remote URL, transport, and credential.

## Acceptance Criteria

- [x] Git step shows no `Production branch` or `Deployed commit` text inputs.
- [x] A successful probe fills default branch and commit from the remote.
- [x] Selecting another remote branch updates the commit without typing.
- [x] Changing remote URL or credential clears stale branch/commit until
      the next successful probe.
- [x] Probe and configuration responses never include secret plaintext.
- [x] Backend unit tests cover HTTPS value composition, default-branch
      selection, and safe probe errors.
- [x] Frontend lint, typecheck, unit, build, and E2E pass.

## Out of Scope

- Changing the stored configuration schema
- Cloning the repository
- Yunxiao/GitHub deployment APIs beyond Git refs
- Auto-probing on every keystroke
