# Technical Design: docker.logs Pattern Filtering and Coverage Awareness

## Boundaries

The change stays inside the remediation module:

- `backend/internal/bootstrap/remediation_loaders.go` — pass the configured
  production branch into the Git adapter.
- `backend/internal/modules/remediation/adapter/git/reader.go` — fetch and read
  the current production branch tip for repository tools.
- `backend/internal/modules/remediation/application/agent_engine.go` and
  `tool_gateway.go` — keep model-visible repository guidance aligned with the
  current-branch behavior.
- `backend/internal/modules/remediation/application/docker_tool.go` — tool
  schema, parameter validation, summary formatting.
- `backend/internal/modules/remediation/adapter/sshlog/container_runtime.go`
  — remote command templates for the filtered read and the window line probe.

No other module changes. The security posture is unchanged: the remote side
still only ever receives fixed command templates with `shellQuote`d
arguments.

## Repository Read Selection

The project configuration's `production_branch` is the source for repository
reads. Before each Git read session, the adapter fetches remote heads with
`git fetch --prune` and operates on `refs/heads/<production_branch>` for
`list_tree`, `read_file`, `search`, and `history`. The incident/run
`deployed_commit` remains historical metadata and series identity; it is not
used to select the Git object. This lets remediation inspect the code currently
on the configured production branch while keeping the existing run records
backward-compatible.

## Data Flow

```text
model
  -> docker.logs{since, until, tail, pattern?, context_after?, context_before?}
  -> docker_tool.execDockerLogs
     -> whitelist validation of pattern (reject before any I/O)
     -> bounds: since/until within incident ±15min, interval <= 30min,
        tail 1..2000, context 0..100
     -> sshlog.Reader.ReadDockerLogs (extended DockerLogQuery)
        -> fixed template:
           docker logs --since <q> --until <q> --tail <N> <id> 2>&1 \
             | grep -E -- <q(pattern)> [-A K] [-B K] | tail -<tail>
        -> bounded bytes (existing maxReadBytes / 1 MiB)
  -> window line probe (same SSH session or second bounded command):
           docker logs --since <q> --until <q> <id> 2>&1 | wc -l
  -> ToolResult{Payload, Summary} with coverage fields
  -> conversation + observer log
```

## Contracts

### DockerLogQuery extension

Add three optional fields: `Pattern string`, `ContextBefore int`,
`ContextAfter int`. Zero values mean "no filter", preserving the exact
current remote command for backward-compatible behavior and existing tests.

### Pattern whitelist

Validation function on the application side, before the adapter call:

- reject empty-after-trim values when `pattern` is present,
- reject any byte outside `[a-zA-Z0-9 ._\-|()*?]`,
- reject strings longer than a small cap (suggest 256 bytes),
- the whole pattern must pass through `shellQuote` untouched (single quotes
  wrap; the whitelist already excludes `'`).

No `regexp.Compile` on the Go side is required — grep owns matching; the
whitelist only guarantees the pattern cannot break out of the quoted
argument or smuggle shell syntax. Rejection returns the existing
`ToolRejection{Code: RejectArguments}` shape so no adapter I/O happens.

### Remote templates

`ReadDockerLogs` keeps its current unfiltered path and gains a filtered
path:

```text
docker logs --since 'T' --until 'T' --tail 'N' '<id>' 2>&1 | grep -E -A 'K' -B 'K' -- 'pattern' | tail -'tail'
```

`grep` flags only emitted when context values are positive. The final
`tail` uses the same bounded `tail` parameter so total output is
predictable: at most `tail * (1 + context_after + context_before)` lines.

Window probe, separate method on `Reader` (bounded, same 1 MiB cap):

```text
docker logs --since 'T' --until 'T' '<id>' 2>&1 | wc -l
```

Returns an integer; the byte cap bounds the stream itself, not the count.

### Summary format

Current: `docker logs container=%s bytes=%d truncated=%t`.

Extended: append `window_lines=%d returned_lines=%d filtered=%d` (filtered
count only when a pattern was used; returned lines counted from the final
stdout). Keep the existing prefix so log parsers and existing tests stay
stable.

## Prompt Change

In `agent_engine.go` diagnosing prompt, extend the Docker sentence:

- detail `AnalysisOriginal.time` is UTC; use it directly as the
  `since`/`until` anchor (narrow window around the anchor, e.g. a few
  minutes),
- `docker.logs` returns only the tail of the requested window; if the
  summary shows `window_lines` far above `returned_lines`, narrow the window
  or add a `pattern`,
- a panic or stack anchor from provider detail should be requested with
  `pattern` plus `context_after` to capture the goroutine frames.

## Compatibility and Rollback

- New params are optional; the unfiltered Docker template is byte-identical to
  today's command, so existing Docker behavior remains compatible.
- Repository reads intentionally follow the current configured production
  branch; the stored commit remains available for historical run identity but
  is no longer a read selector.
- Rollback = revert the two commits' worth of diff; no schema or data
  migration is involved.

## Tests

- Whitelist table test: allowed patterns pass, each rejected class
  (`;`, `&`, `|` outside parens, `<`, `>`, backtick, `$`, quotes, newline,
  backslash) fails with RejectArguments and zero adapter calls.
- Template test: filtered command contains exactly one quoted pattern, no
  `-A`/`-B` when zero, correct flags when positive, final `tail` present.
- Bounds: tail 2000 accepted / 2001 rejected; context 100 accepted / 101 and
  -1 rejected; non-integer rejected.
- Summary: coverage fields present for filtered and unfiltered results.
- Regression: a 386k-line-shaped fixture with the panic early in the window
  — unfiltered tail misses it, filtered call with `pattern` and
  `context_after` returns it within the byte cap.
- Repository-read regression: a stale `RepoRef.Commit` is ignored; after fetch,
  `list_tree`, `read_file`, `search`, and `history` use the latest configured
  production branch. Invalid branch refs fail before clone/fetch.
- Prompt: diagnosing prompt contains the UTC-anchor and tail-coverage
  wording.
