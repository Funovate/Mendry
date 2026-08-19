# Implement: SSH PEM file import

## Checklist

1. Add `inspectSshPrivateKeyDraft`, `sshPrivateKeyInspectionMessage`, and
   `SSH_PRIVATE_KEY_MAX_BYTES` in
   `frontend/src/features/configuration/configuration.ts`.
2. Cover the helper in `frontend/tests/configuration.test.ts`: valid OpenSSH
   / RSA / PKCS#8 PEM; empty; public key; `.ppk` header; oversized; Git
   compose size includes passphrase.
3. Update `GitSecretInputs` in `RepositoryStep.tsx` to accept a file picker
   beside the existing private-key textarea. Import and Save both run the
   helper. Keep passphrase composition after a valid key.
4. Update `CredentialField.tsx` so `kind === "ssh_private_key"` (create) and
   `selected.kind === "ssh_private_key"` (edit replacement) use a textarea +
   file picker + the same helper. Other kinds stay on the password input.
5. Clear file input, draft, and import error on success, cancel, kind change,
   and credential selection change.
6. Extend `frontend/tests/application.spec.ts`:
   - Git: `setInputFiles` a PEM, assert POST body is the file text (plus
     passphrase when filled), then assert key text is gone from the DOM.
   - Source SSH: switch source type to SSH, create `ssh_private_key` via
     file, assert POST body, assert no leftover key text.
   - Reject a non-PEM file on one surface; assert no secrets write and a
     stable error.
   - Keep the existing Git paste path.
7. Add only the CSS needed for the file button next to credential fields in
   `configuration.css`. Do not copy prototype MCP JSON-import styles unless
   they already match credential-field spacing.

## Validation

From `frontend/`:

```bash
npm run lint
npm run typecheck
npm run test
npm run build
npm run test:e2e
cd ..
git diff --check
```

## Risky files

- `CredentialField.tsx` is shared by Source, Trigger, and LLM. Kind gating
  must be exact so webhook / bearer flows do not grow a file picker.
- Accessible labels in Playwright (`SSH private key`,
  `Source credential value`) must remain unique on the mounted step.
- Do not log or interpolate file contents in errors.

## Rollback

Revert the frontend files listed above. No backend rollback.

## Ready for start

Planning artifacts: `prd.md`, `design.md`, `implement.md`, plus curated
`implement.jsonl` / `check.jsonl`. Wait for user review before
`task.py start`.
