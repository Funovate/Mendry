# Design: read Git baseline from remote

## Boundary

New project application use case plus HTTP probe. Configuration PUT keeps
the existing snapshot fields. Frontend stops editing those fields by hand.

## HTTP

`POST /api/v1/projects/{projectKey}/repository/refs`

Request:

```json
{
  "remoteUrl": "https://git.example/app.git",
  "transport": "https",
  "credentialSecretId": "uuid"
}
```

Success data:

```json
{
  "defaultBranch": "main",
  "deployedCommit": "0123456789abcdef0123456789abcdef01234567",
  "branches": [
    { "name": "main", "commit": "0123456789abcdef0123456789abcdef01234567" }
  ]
}
```

Auth: same as configuration write (`manageConfiguration`). Missing or
foreign secret is `invalid_input`. Remote/auth failures are `git_unreachable`
without echoing the URL userinfo or command.

## Application flow

1. Authorize project admin / manageConfiguration.
2. Load the encrypted secret by id in that project.
3. Decrypt in process. Clear plaintext after the Git call.
4. Ask `GitRefLister` for refs. Production adapter runs
   `git ls-remote --symref <url>` with a timeout.
5. HTTPS embeds `username:token` in the URL userinfo only for that process.
   SSH writes a 0600 temp key and uses `GIT_SSH_COMMAND`. Never log the
   URL with userinfo, env, or temp path contents.
6. Parse `ref: refs/heads/<name>\tHEAD` as default branch; fall back to
   `main`, then `master`, then the first `refs/heads/*`.
7. Return heads only. Commits must match `^[0-9a-fA-F]{7,64}$`.

`GitRefLister` is injected so tests never talk to a real remote or mutate
process environment.

## Frontend

`RepositoryStep` replaces the two inputs with:

- Read-only / select branch from `branches`
- Read-only commit for the selected branch
- `Read from remote` button

Probe when the operator clicks the button, and once automatically when
remote URL, transport, and credential become complete. If any of those
change, clear the previous probe.

Save uses the selected branch name and its commit. No probe, no save.

## Compatibility

Existing saved configurations still display their stored branch/commit
until the operator re-reads the remote. A new probe overwrites the draft
baseline.
