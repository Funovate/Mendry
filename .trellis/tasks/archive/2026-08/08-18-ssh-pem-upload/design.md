# Design: SSH PEM file import

## Architecture and boundaries

Frontend-only change inside the existing configuration wizard. No backend,
migration, secret kind, or HTTP contract change.

```
frontend/src/features/configuration/
  configuration.ts                 # add parse/validate helpers
  configuration.css                # file-picker styles next to credential fields
  wizard/CredentialField.tsx       # source/trigger/LLM shared editor
  wizard/RepositoryStep.tsx        # GitCredentialField + GitSecretInputs
```

Do not add a third credential editor. Do not import prototype `App.tsx`.

## Data flow

```
operator chooses File
  -> File.size / File.text()
  -> inspectSshPrivateKeyDraft(text)
  -> on success: set draft textarea
  -> on failure: stable inline error, draft unchanged, input.value = ""

operator pastes into textarea
  -> draft updates as today
  -> Store/Save calls the same inspect helper
  -> invalid: inline error, no POST/PATCH
  -> valid: existing createSecret / updateSecret
```

Git still composes key + optional passphrase with
`composeSshPrivateKeyValue` after the key itself passes inspection.
Source `ssh_private_key` stores the inspected PEM text only; no passphrase
field is added.

## Shared helper

Add a pure helper next to the existing compose functions in
`configuration.ts`:

```ts
export const SSH_PRIVATE_KEY_MAX_BYTES = 65519;

export type SshPrivateKeyInspection =
  | { ok: true; text: string }
  | { ok: false; reason: "empty" | "unreadable" | "oversized" | "not_pem" };

export function inspectSshPrivateKeyDraft(raw: string): SshPrivateKeyInspection
export function sshPrivateKeyInspectionMessage(reason): string
```

Acceptance rules:

- Trim only leading/trailing whitespace for emptiness.
- `not_pem` when the text does not contain both
  `-----BEGIN ` + `PRIVATE KEY-----` and a later
  `-----END ` + `PRIVATE KEY-----`.
  This covers OpenSSH, PKCS#1 RSA, and PKCS#8 PEM. It rejects `.ppk`
  (`PuTTY-User-Key-File`) and public keys.
- `oversized` when UTF-8 byte length of the stored secret would exceed
  65519. For Git, measure `composeSshPrivateKeyValue(key, passphrase)`.
  For source, measure the key text alone.
- Messages are stable and must not interpolate file contents.

`File.text()` failure maps to `unreadable`. Empty file maps to `empty`.

## UI contract

When the active kind is `ssh_private_key`:

- Replace the single-line password input with a textarea (create and
  replacement). Accessible names stay compatible with existing Playwright
  labels where possible (`SSH private key`, `Replacement SSH private key`,
  `Source credential value` / `Source replacement value`).
- Add a labeled file input: `accept=".pem,.key,text/plain"` plus no-extension
  keys are still choosable (browser `accept` is a hint, not the validator).
- Show the selected file name only after a successful import, and clear it
  with the draft on success, cancel, kind change, or selection change.
- Keep paste. Import overwrites the current draft key, not the passphrase.

When the kind is anything else, keep today's password input. Switching kind
clears the key draft, file input, and import error.

Create and edit stay mutually exclusive. Edit still starts with an empty
sensitive draft. Empty replacement remains name-only (`value` omitted).

## Compatibility

- Secrets API, encryption, and adapters are unchanged.
- Existing Git SSH e2e paste of an OpenSSH PEM must keep passing.
- Source webhook / LLM / HTTPS Git credential flows must not grow a file
  picker.

## Trade-offs

- Frontend PEM check is shape-only, not cryptographic. That matches current
  backend validation and blocks the common wrong-file cases without a new
  Go contract.
- Encrypted keys with a passphrase still cannot be used at SSH runtime
  (`BatchMode=yes`). This task does not fix that. Git may still append a
  passphrase to the stored secret as it does today.

## Rollback

Revert the frontend files. No data migration. Already-saved non-PEM
`ssh_private_key` rows are untouched; only new writes are gated.
