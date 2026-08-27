# Implementation Plan: docker.logs Pattern Filtering and Coverage Awareness

## Ordered Work

1. [x] Run GitNexus upstream impact for `ToolGateway.execDockerLogs`,
      `sshlog.Reader.ReadDockerLogs`, `DockerLogQuery`, and
      `agent_engine.buildPrompt`. Record risk before editing; warn on HIGH
      or CRITICAL before proceeding. (all LOW risk, 0 impacted, no warning needed)
2. [x] Extend `domain.DockerLogQuery` with `Pattern`, `ContextBefore`,
      `ContextAfter` and the `Reader` window-line-probe method
      (`sshlog` package + its tests).
3. [x] Change Git repository reads to fetch the configured production branch
      and use its latest `refs/heads/<branch>` for all repository tools; keep
      the stored deployed commit as historical run metadata and validate the
      branch before clone/fetch.
4. [x] Add the pattern whitelist validator in `docker_tool.go` and wire
      `pattern` / `context_after` / `context_before` into the input schema;
      raise `maxDockerLogLines` to 2000 (keep the sshlog runtime constant in
      sync or derive it from the shared bound).
5. [x] Build the filtered remote template and the `wc -l` probe in
      `container_runtime.go`; keep the unfiltered path byte-identical to
      today.
6. [x] Extend the `ToolResult.Summary` with `window_lines`, `returned_lines`,
      `filtered`.
7. [x] Strengthen the diagnosing prompt in `agent_engine.go` (UTC anchor +
      tail coverage + pattern/context guidance).
8. [x] Tests:
      - whitelist rejection table (no adapter calls on rejection),
      - filtered command template shape (quoting, -A/-B emission, final tail),
      - tail/context bounds (2000/100 accepted, out-of-range rejected),
      - summary coverage fields,
      - high-volume regression fixture: panic early in a 386k-line window is
        returned only by the filtered call,
      - latest production branch regression: a stale `RepoRef.Commit` is ignored,
        branch content is fetched after cache reuse, and invalid refs are rejected
        before cloning.
      - prompt wording assertions.
9. [x] Let planning execute catalog-authorized read-only repository tools and
      continue with bounded observations; provide one shared complete planning
      wire contract in both the initial prompt and field/category-specific safe
      protocol corrections.
10. [x] Run the full remediation + sshlog test suites; confirm no unrelated
       worktree changes. (go build ./..., go test -count=1 ./..., go vet all green)

## Validation Commands

```bash
cd backend
go test ./internal/modules/remediation/... ./internal/modules/remediation/adapter/sshlog/...
go vet ./internal/modules/remediation/...
```

## Review Gates

- No shell metacharacter can reach the remote command; the whitelist must be
  the single enforcement point and must run before any adapter call.
- Unfiltered Docker behavior remains byte-identical (existing tests untouched).
- The summary remains parseable by existing log/observer consumers.
- Repository tools must advertise and execute against the current configured
  production branch rather than the historical run commit.

## Rollback Points

- Each step compiles and tests green independently; the prompt change (step
  7) and the branch-read change can be reverted independently from the Docker
  tool changes.
