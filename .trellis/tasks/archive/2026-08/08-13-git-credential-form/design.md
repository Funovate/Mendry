# Design: Git credential form by transport

## Boundary

Frontend-only. `POST /api/v1/projects/{key}/secrets` still accepts
`{ name, kind, value }`. Configuration still stores `credentialSecretId`.

## Form composition

`RepositoryStep` stops using generic create fields from `CredentialField`.
Keep the existing-secret select there or extract a thin Git-specific create
panel beside it.

HTTPS:

- name
- username
- password or token (`type="password"`)
- kind forced to `git_credential`
- value = `username.trim() + ":" + secret` when username is non-empty, else
  `secret`

SSH:

- name
- private key (`textarea`)
- optional passphrase (`type="password"`)
- kind forced to `ssh_private_key`
- value = private key; if passphrase is non-empty, append `\n` plus the
  passphrase after the key text

Do not invent a second persisted field. The connector that later consumes the
blob can split `user:token` or read the key block.

## Filtering

`knownSecrets` shown in the Git dropdown:

- https: `kind === "git_credential"`
- ssh: `kind === "ssh_private_key" || kind === "ssh_password"`

When `transport` changes, if the selected id is not in the filtered list, set
the id to `""`.

## Compatibility

Existing `git_credential` / `ssh_private_key` / `ssh_password` rows remain
valid references. Old unstructured values are still selectable; only new
creates use the structured form.

## Tests

Update/add Playwright coverage on the Git repository tab. Keep the webhook
credential flow on the generic field.
