# Upload PEM file for SSH private key

## Goal

An operator can store an SSH private key from the usual cloud or jumphost
artifact — a `.pem` / OpenSSH key file — or by pasting the same PEM text.
Wrong files and non-key pastes are rejected before the secrets API is called.

## Background

Production stores only the key *text* as an encrypted `ssh_private_key` secret.
There is no file picker today.

The two write surfaces are unequal:

- Git SSH (`GitCredentialField` in
  `frontend/src/features/configuration/wizard/RepositoryStep.tsx`) already has
  a private-key textarea plus optional passphrase. Playwright pastes a PEM in
  `frontend/tests/application.spec.ts:379`.
- SSH log source (`SourceStep` → shared `CredentialField`) uses a generic
  `type="password"` single-line input for every secret kind, including
  `ssh_private_key` (`CredentialField.tsx:105`). A PEM pasted there loses
  newlines.

Backend already accepts PEM text. `ValidateSecret` only checks size
(`1..65519` bytes) in `backend/internal/modules/projects/domain/project.go:209-216`.
Git and SSH adapters extract the `-----BEGIN …-----` / `-----END …-----` block
and write a temp file for `ssh -i`. No new secret kind or persistence contract
is required.

Component guidelines require extending `CredentialField` and
`GitCredentialField`. Do not add a third credential editor. Secret plaintext
may exist only in the active write form and the outgoing create/update
request; clear it after success and never render stored key material.

## Requirements

- R1. Both production `ssh_private_key` write surfaces support file import and
  paste: Git SSH create/edit, and SSH log-source create/edit when the selected
  kind is `ssh_private_key`.
- R2. File import fills the same draft field as paste. It does not replace the
  secrets API. The source editor must accept a multi-line PEM; a single-line
  password input is not enough for `ssh_private_key`.
- R3. After a successful create or update, the file input and draft key are
  cleared. The UI shows only secret metadata.
- R4. Import is client-side `File` → text. The file is not uploaded as
  multipart and is not stored as a blob.
- R5. Existing secret encryption, size limits, and adapter PEM extraction stay
  unchanged. No new endpoint or secret kind.
- R6. File import is rejected inline, without filling the draft, when the file
  is empty, unreadable, larger than 65519 bytes, or does not contain a
  `-----BEGIN …PRIVATE KEY-----` / `-----END …PRIVATE KEY-----` pair. The
  error must not include file contents or key material. PuTTY `.ppk` is
  rejected, not converted.
- R7. The same PEM-looking check blocks Save / Store on a pasted
  `ssh_private_key` create, and on an edit that supplies a replacement value.
  An empty replacement remains a name-only edit.
- R8. Non-key credential kinds keep their current single-line secret input and
  do not gain a key-file picker.

## Acceptance Criteria

- [x] AC1. Choosing a UTF-8 PEM / OpenSSH private-key file populates the
      `ssh_private_key` draft on Git SSH create/edit and on SSH log-source
      create/edit when the kind is `ssh_private_key`. Maps to R1, R2, R4.
- [x] AC2. Saving still sends the composed plaintext through the existing
      secrets API; no new endpoint or secret kind. Maps to R5.
- [x] AC3. After success, neither the file name plus contents nor the key text
      remain in the form. Maps to R3.
- [x] AC4. Paste-only create/update of a valid PEM still works, including the
      existing Git SSH Playwright path. Maps to R2, R7.
- [x] AC5. Non-key kinds (`git_credential`, `ssh_password`, `http_bearer`,
      `http_header`, `webhook_hmac`) do not gain a key-file picker. Maps to R8.
- [x] AC6. An empty, unreadable, oversized, or non-PEM file is rejected with a
      stable inline error; the draft key is left unchanged and the file input
      is cleared. The error text does not include file contents. Maps to R6.
- [x] AC7. Store / Save is blocked, and no secrets write is sent, when the
      `ssh_private_key` draft (create) or replacement (edit) is present but
      fails the PEM-looking check. Name-only edits still omit `value`.
      Maps to R7.

## Out of scope

- Uploading log files or any other evidence payload.
- A new `pem` / `ppk` secret kind.
- Backend PEM-format validation beyond the existing size check.
- Using the appended Git passphrase at SSH runtime (`BatchMode` still cannot
  unlock an encrypted key).
- Adding a passphrase field to the source `CredentialField`.
- PuTTY `.ppk` conversion.
- Changing SSH log runtime (`tail` of remote `logPath`).
- Wiring MCP / cloud sources.
- Prototype `frontend/src/App.tsx`.
