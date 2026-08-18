# Add bounded git outbound request logging

## Goal

Operators diagnosing a failed Git repository probe currently see only the
inbound `http.request.completed` / `http.request.failed` pair. That record
does not describe the `git ls-remote` attempt made on the request's behalf,
so a bad URL, rejected credential, DNS failure, and timeout all collapse to
`ErrGitUnreachable`. Project LLM probes already emit one
`llm.request.completed` record with operation, host, path, duration,
outcome, and redacted request/response. Git probes need the same bounded
outbound record.

## Background

- Inbound HTTP completion is owned by `httpserver.AccessLog`. It must stay
  unchanged. Outbound Git identity belongs on a separate event, matching
  `llm.request.completed`.
- Config-wizard Git probing is `projects.Service.ProbeRepositoryRefs` →
  `projects/adapter/git.Lister.ListRefs` (`lsremote.go`). The lister execs
  `git ls-remote --symref --heads <target>` with a 15s timeout. HTTPS
  injects userinfo via `AuthenticatedHTTPSRemote`; SSH writes a temp key
  and sets `GIT_SSH_COMMAND`. Today the lister has no logger
  (`NewLister()` takes no arguments).
- `.trellis/spec/backend/logging-guidelines.md` already names the gap:
  "Git `ls-remote` remains uninstrumented until it grows the same bounded
  identity contract."
- `observability.LogLLMRequest` / `LLMRequest` / `HTTPIdentity` /
  `SnapshotHTTPPayload` / `ClassifyOutbound` are the contract to mirror.
  Console already maps `llm.operation.name` → `operation`, `http.host` →
  `host`, `http.path` → `path`, `http.request` → `request`,
  `http.response` → `response`.
- `ValidateRepositoryProbe` only accepts credential-free `https://` or
  `ssh://` remotes with a host. SCP-style `git@host:path` never reaches
  the adapter. `HTTPIdentity` therefore works for both transports.
- Returning `ErrGitUnreachable` to the client is unchanged. The outbound
  record is what operators use to distinguish failure classes. The
  adapter must keep the original exec error for classification and must
  not interpolate the authenticated URL, token, or PEM into that error.

## Requirements

- R1. Every `Lister.ListRefs` exec emits exactly one
  `git.request.completed` record when a logger is present. A nil logger
  is a no-op, matching `LogLLMRequest`.
- R2. Required fields: `component=git`, `git.operation.name=ls-remote`,
  `http.host` and `http.path` from `HTTPIdentity` of the **public**
  remote URL, `duration_ms`, `outcome`.
- R3. Optional fields: `http.request` (the public command identity, never
  the authenticated URL), `http.response` (stdout on success; stdout plus
  stderr when a body was captured on failure), `error_class` on failure
  only. Omit `http.status` and `llm.model` — Git has neither.
- R4. Request/response snapshots reuse the LLM 4KB UTF-8 cap and
  redaction pipeline, plus Git-specific shapes: URL userinfo
  (`https://user:token@host` → userinfo secret replaced) and PEM private
  key blocks. Tokens matching `sk-…` / `Bearer …` stay covered by the
  existing pattern.
- R5. Levels match other outbound adapters: success `DEBUG`; `canceled`
  `INFO`; `timeout` `WARN`; remaining failures `ERROR`.
- R6. Failure classification: `canceled` and `timeout` from context;
  other exec failures use a stable `command` class (not the raw git
  stderr). Do not invent an HTTP status from the git exit code.
- R7. New event/field constants live in `observability`. Console maps
  `git.operation.name` → `operation` so the line matches the LLM shape:
  `DBG [git] request completed operation=ls-remote host=… path=… took=… outcome=…`.
- R8. Wire the process logger at the composition root:
  `projectgit.NewLister(logger)`. Do not change `GitRefLister` or the
  client-facing `ErrGitUnreachable` mapping.
- R9. Update `.trellis/spec/backend/logging-guidelines.md` to document
  outbound Git requests and remove the "ls-remote remains uninstrumented"
  sentence.

## Acceptance Criteria

- [ ] A successful `ListRefs` with an injected logger writes one JSON
      `git.request.completed` record: `component=git`,
      `git.operation.name=ls-remote`, public host/path, `outcome=success`,
      `http.request` contains `ls-remote` and the public remote URL,
      `http.response` contains the captured stdout.
- [ ] A failed `ListRefs` whose git stderr mentions an authenticated
      HTTPS URL or PEM material still returns `ErrGitUnreachable` and
      writes `outcome=failure` with a redacted snapshot. The log dump
      contains neither the token/password nor the PEM body.
- [ ] Host/path never include userinfo or query, even if the exec target
      was an authenticated HTTPS URL.
- [ ] Console projection of the new event matches the LLM line shape
      (`[git] request completed operation=… host=… path=…`).
- [ ] `NewLister(nil)` still lists refs and writes no log records.
- [ ] Existing LLM outbound tests keep passing. Client probe API
      behavior is unchanged.

## Out of Scope

- Remediation `adapter/git.Reader` clone/fetch/ls-tree/cat-file/grep/log
  instrumentation. Those commands can reuse `LogGitRequest` later; this
  task only covers the config-wizard `ls-remote` probe.
- Changing `ErrGitUnreachable`, probe HTTP status codes, or returning
  git stderr to the client.
- Logging `GIT_SSH_COMMAND`, temp key paths, headers, or env.

## Notes

- This is a bounded observability contract plus one adapter hook. No
  schema, API, or frontend change.
