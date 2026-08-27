# Fix remediation docker.logs window coverage blind spot

## Goal

Make the remediation `docker.logs` tool let the model use the Tencent CLS
detail anchor (log event time, source path, line number) to pull the panic and
goroutine stack from the container, even when the incident window holds
hundreds of thousands of lines. Today the tool can only return the window
tail, which silently drops the decisive evidence.

## Evidence From Production

Incident `01a03d53-ccc4-781e-8325-f603acae0cf8`, remediation run
`29025d9b-9326-4ccd-bff4-1ea47a348b98` (2026-08-26):

- `docker logs --since 2026-08-26T08:53:14Z --until 2026-08-26T09:23:14Z`
  holds **386,130 lines** (~214 lines/s).
- `--tail 500` therefore returns only the **last ~2.3 s** of the window.
- The panic (`TriggerNilPointerFault` at `common.go:64`, 09:06:15Z) sits
  13 minutes into the window and is always cut off.
- The model had the detail anchor (`time: 2026-08-26 09:06:15.466`,
  `path: .../common.go:59`) and the prompt already says "treat detail error,
  stack, source path, or line number as an anchor", but the tool has **no
  channel to act on the anchor**: no filter, no context lines, and no
  coverage report.
- Result: four `insufficient_evidence` loops with empty tool calls, then
  `blocked_manual_review` / `outcome=stopped`.

## Requirements

- Add an optional `pattern` parameter to `docker.logs`: a bounded regex
  matched against log lines on the remote side before any tail limit
  applies. Only characters from `[a-zA-Z0-9 ._\-|()*?]` are accepted; shell
  metacharacters are rejected before any SSH call.
- Add optional `context_after` / `context_before` parameters (integer
  bounds 0–100) so one call can return a panic line plus the following
  goroutine stack frames.
- Raise the `tail` maximum from 500 to 2000. The tail applies after the
  filter, so this remains bounded and safe.
- Extend the tool result summary with coverage information: total window
  line count, returned line count, filtered line count, and the truncated
  flag. The model must be able to see that it received only the tail of the
  window and react by narrowing the window or adding a pattern.
- Keep every existing safety bound: allowlisted command templates only
  (`docker ps/inspect/logs` plus the bounded `grep` and `wc -l` forms),
  `shellQuote` on all arguments, the ±15-minute incident window expansion,
  the 30-minute max interval, and the byte cap.
- Strengthen the diagnosing prompt: the detail `AnalysisOriginal.time`
  field is the UTC log-event time and should be used directly as the
  `since`/`until` anchor; `docker.logs` only returns the tail of the window,
  so early window content requires a pattern or a narrower window; the
  coverage numbers in the result summary must be considered before judging
  "no failure in the window".
- Preserve backward compatibility: the new parameters are optional; calls
  using only `since`/`until`/`tail` keep today's exact behavior.
- Keep planning tool-driven: advertise and execute the phase-authorized
  read-only repository tools through the existing catalog/gateway, append
  bounded tool observations to the conversation, and continue until the model
  returns a valid plan.
- Give every planning turn the complete `planCandidates` wire contract. When a
  response fails validation, return a safe field/category-specific correction
  plus the same complete contract; never echo raw model values or credentials.

## Acceptance Criteria

- [ ] A `docker.logs` call with `pattern` matching the panic text returns the
      panic line and its requested context lines from a window holding
      386k+ lines, without exhausting the byte cap.
- [ ] Injection attempts (`;`, `&`, `<`, `>`, `` ` ``, `$`, quotes,
      backslashes, newlines) in `pattern` are rejected with a safe argument
      error and never reach the SSH command.
- [ ] `context_after`/`context_before` accept 0–100 integers only; anything
      else is rejected.
- [ ] `tail` accepts 1–2000; values above 2000 are rejected.
- [ ] The result summary reports window lines, returned lines, filtered
      lines, and truncation for both filtered and unfiltered calls.
- [ ] The diagnosing prompt tells the model that detail log times are UTC
      anchors and that `docker.logs` covers only the window tail.
- [ ] Existing sshlog, docker tool, coordinator, and prompt tests pass
      without changing unrelated worktree changes.
- [x] Planning can execute one or multiple catalog-authorized repository reads,
      account for model/tool budgets and invocations, feed success or rejection
      observations back to the model, and then accept `planCandidates`.
- [x] A valid planning tool request resets the consecutive protocol-failure
      counter; invalid planning output still reaches the bounded manual-review
      stop only after three consecutive failures.
- [x] The planning prompt and every planning protocol correction share one
      complete JSON contract, while known validation failures identify a safe
      code/path/expected field without replaying model-provided values.

## Out Of Scope

- Arbitrary shell commands or any new command beyond the allowlisted
  `docker ps/inspect/logs` + bounded `grep`/`wc -l` forms.
- Widening the incident window expansion beyond ±15 minutes or the 30-minute
  max interval.
- Auto-sharding the window into multiple SSH reads; the model narrows the
  window itself using the coverage numbers.
- Changing the byte cap, container identity resolution, or the repository
  tool set.
