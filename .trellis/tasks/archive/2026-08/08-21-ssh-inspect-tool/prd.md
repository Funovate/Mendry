# Expose a bounded SSH inspect tool

This slice was absorbed into `08-21-remediation-tool-driven-context` after the
user chose to amend that in-progress task instead of landing inspect as a
follow-up.

Do not implement from this directory. The SSH inspect contract, allowlist,
bootstrap hints, raw bounded output, and acceptance criteria now live in:

- `.trellis/tasks/08-21-remediation-tool-driven-context/prd.md`
- `.trellis/tasks/08-21-remediation-tool-driven-context/design.md` (D5)
- `.trellis/tasks/08-21-remediation-tool-driven-context/implement.md` (Step 9)

## Absorbed Decisions

- SSH sources advertise `ssh.inspect` only; no `evidence.search`/`context`.
- Model writes a command string; gateway parses then allowlists; SSH executes
  reconstructed quoted argv, never `bash -c` of the raw string.
- Path jail is the SSH user; cwd is `projectFolder`.
- Bootstrap hints host/user/`projectFolder`/`logPath`; model must `ls` before
  reading; harness never auto-tails `logPath`.
- Core inspect binaries including `env`/`printenv`; `|` pipelines up to 3
  segments; 64KiB raw stdout/stderr; 15s timeout.
- No secret-pattern redaction on inspect output (accepted LLM leak risk).
- Cloud/MCP/Git unchanged.
