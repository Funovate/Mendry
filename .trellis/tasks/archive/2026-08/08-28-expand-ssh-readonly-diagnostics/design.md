# Design: Expanded SSH Read-Only Diagnostics

## Overview

This change extends the existing bounded SSH inspect boundary instead of adding
an arbitrary remote shell. Authorization remains fail-closed and operates on a
parsed pipeline of reconstructed argv segments. Docker deployments receive both
the typed incident-window `docker.logs` tool and the generic read-only
`ssh.inspect` tool.

Successful SSH/Docker results become durable evidence before any success result
is emitted to the observer or model. The persisted payload and model-visible
payload are one canonical sanitized projection, so a citation always refers to
the exact content used by the diagnosis.

## Data Flow

```text
model tool request
  -> ToolCatalog phase/source authorization
  -> parse command without a shell
  -> per-command read-only argument policy
  -> reconstruct quoted argv
  -> SSH/Docker adapter with timeout and byte bounds
  -> canonical runtime-evidence projection
       normalize UTF-8
       redact credentials and temporary key paths
       bound JSON payload
       classify provenance/correlation
  -> AppendEvidence (project + incident + run ownership)
  -> attach persisted evidence ID to ToolResult
  -> emit sanitized observer result
  -> append identical sanitized result + evidence ID to AgentConversation
  -> diagnosis cites evidence ID
  -> EvidenceGate resolves persisted record
```

A persistence or projection failure changes the tool result to a stable,
non-retryable runtime-evidence persistence failure. The raw successful adapter
output is not emitted to the observer or model.

## Command Authorization

### Parser Boundary

The existing tokenizer remains the only model-command entry point. It continues
to reject newlines, NUL, command substitution, backticks, separators,
redirection, environment assignment, unquoted glob expansion, relative parent
segments, empty pipeline segments, and pipelines longer than three segments.
Each pipeline segment is independently authorized before SSH.

The parser reconstructs shell-quoted argv. The SSH adapter executes only that
reconstructed command after its quoted `projectFolder` cwd prefix; it never
executes the raw model string.

### Command Policy Registry

Replace the binary-only map with a registry of per-command validators. Simple
read-only commands may use a shared no-mutation validator; mixed-purpose
commands require explicit semantic validation.

Always-read command families include file/display and system inspection tools:

`ls`, `cat`, `head`, `tail`, `grep`, `egrep`, `fgrep`, `stat`, `wc`, `file`,
`readlink`, `realpath`, `pwd`, `uname`, `df`, `du`, `ps`, `id`, `free`,
`uptime`, `lscpu`, `lsblk`, `lsof`, `netstat`, `getent`, `who`, `w`, `last`.

Mixed-purpose policies:

| Command | Allowed shape | Mandatory rejection |
|---|---|---|
| `hostname` | no argument or read flags such as `-I`, `-i`, `-f`, `-s`, `-d` | any hostname-setting positional argument |
| `date` | display/parse formatting | `-s`, `--set`, file timestamp mutation |
| `find` | predicates and stdout-printing actions | `-delete`, `-exec*`, `-ok*`, `-fprint*`, `-fprintf`, `-fls` |
| `journalctl` | bounded historical/status reads | follow, vacuum, rotate, flush, sync, key/catalog mutation, relinquish operations |
| `dmesg` | display/filter reads | clear/read-clear, console on/off/level mutations |
| `ss` | socket listing/filtering | `-K` / `--kill` |
| `ip` | `addr/address/link/route/rule/neigh/maddr/tunnel` show/list/get forms and `netns list` | add, append, change, delete, del, flush, replace, set, exec, attach, detach, monitor |
| `systemctl` | `status`, `show`, `cat`, `list-*`, `is-*`, `list-dependencies` | start/stop/restart/reload/enable/disable/mask/edit/set-property/daemon-reload and all state changes |
| `docker` | `version`, `info`, `ps`, `inspect`, `top`, non-streaming `stats`, bounded non-following `logs`, and list/inspect forms for image/network/volume/container | exec/run/attach/cp/create/start/stop/restart/kill/rm/update/pause/unpause/rename/wait/commit/build/pull/push/prune, endpoint/context overrides, follow, streaming stats |

`env` and `printenv` are removed. Interpreters, package managers, database
clients, `sudo`, `su`, mutation utilities, and commands that launch nested
arbitrary executables are not registered.

Potentially streaming reads must use a non-following form. The existing 15s
process timeout and 64 KiB combined output cap remain defense in depth, not a
substitute for policy validation.

## Tool Catalog

For an enabled supported SSH source with pull/context collection capability:

- host deployment: advertise `ssh.inspect`;
- Docker deployment: advertise both `ssh.inspect` and typed `docker.logs`;
- planning: preserve the existing repository-only behavior unless a later task
  explicitly authorizes runtime collection during planning.

Cloud, legacy log API, MCP, and repository catalog behavior does not change.

## Canonical Runtime Evidence

Add an application-owned projector for `ssh.inspect` and `docker.logs`. It owns
normalization, redaction, JSON encoding, evidence metadata, and ToolResult
replacement. No adapter or PostgreSQL type crosses this boundary.

### SSH Inspect Evidence

Payload fields:

```json
{
  "command": "reconstructed quoted argv",
  "exitCode": 0,
  "stdout": "sanitized bounded text",
  "stderr": "sanitized bounded text",
  "truncated": false,
  "bytesRetrieved": 1234
}
```

- provider: `ssh`
- kind: `runtime`
- classification: `correlated_supporting`
- primary: `true`
- operational correlation: `true`
- temporal correlation: `false`
- outcome: `success` or `empty`

Generic inspect is supporting evidence because the service cannot infer fault
semantics from an arbitrary read-only command. The model may use it to reconcile
host identity, networking, process state, or configuration, but the persisted
classification remains service-owned.

### Typed Docker Log Evidence

Payload includes the sanitized container identity, stdout/stderr, truncation,
byte count, coverage counters, and normalized query bounds/pattern metadata.

- provider: `docker`
- kind: `runtime`
- classification: `direct_fault` when the bounded result contains returned log
  lines; otherwise `correlated_supporting`
- primary: `true`
- temporal correlation: `true`
- operational correlation: `true`
- outcome: `success` or `empty`

The typed port resolves the saved exact container and enforces incident time,
line, pattern, and byte bounds. Causal closure and citation are still required;
a non-empty log row alone does not authorize planning.

### Sanitization

The canonical projection applies the existing structured/text credential
redaction patterns and additionally removes PEM blocks and `fixthe-ssh*`
temporary key paths. Key-shaped fields (`password`, `secret`, `token`,
`authorization`, `credential`, API-key variants) are replaced recursively.
Both persistence and model context consume this same projection. Raw adapter
stdout/stderr must not reach observer payloads, tool invocation rows, or model
messages.

### Identity And Deduplication

Evidence ownership uses the current `RunIdentity` plus `EvidenceScope`:
project, environment, source, incident, and run must all be non-empty and
consistent. Provenance records the logical tool, phase, catalog version, and a
projection version without storing credentials or raw connection authority.

The content hash is SHA-256 of canonical JSON. The deduplication key contains
the tool identity and content hash. Identical retries in one run return the
same evidence row; changed output creates a new immutable record. The existing
scoped unique index and `AppendEvidence` implementation are reused, so no
schema migration is required.

## Persistence Wiring

Define a narrow application-owned runtime evidence writer interface containing
only `AppendEvidence`. `ToolGateway` receives it through explicit composition
root wiring. Legacy constructors remain source-compatible, but observed runtime
tools fail closed if no writer is configured.

`ExecuteToolObservedWithCatalog` performs this order:

1. execute the authorized adapter;
2. for SSH/Docker success, project and persist canonical evidence;
3. replace `ToolResult.Payload` with canonical content and attach evidence ID;
4. emit the observer result;
5. return to coordinator, which appends the same ToolResult to conversation and
   records the invocation.

## Citation And Correlation

The existing conversation observation already carries `evidenceIds`; no wire
shape change is required. Diagnosis instructions will explicitly require a
citation whenever reasoning uses SSH/Docker content.

Evidence resolution remains authoritative for record ownership and stored
classification. Correlation merging must preserve model-assessed semantic
fields that the persistence schema does not represent, especially
`hostIdentity`, while persisted temporal/operational/direct-bridge facts remain
authoritative. This prevents an empty database boolean projection from erasing
a host identity reconciliation made from persisted SSH evidence.

## Continuation Reuse

Runtime evidence stays owned by the attempt that collected it, but an eligible
continuation may read evidence from earlier attempts in the same immutable
series. PostgreSQL queries must require:

- same project and incident;
- evidence run belongs to the target run's series;
- evidence attempt number is not later than the target attempt;
- pre-run observation evidence remains available as before.

Cross-project, cross-incident, cross-series, and future-attempt evidence remains
unresolvable.

A bounded continuation evidence loader renders prior sanitized runtime records
with their original evidence IDs into diagnosis continuation context. Planning
continuations may retain the existing compact checkpoint path. No evidence row
is copied or reassigned to the child run.

## Compatibility And Migration

- No database schema migration is expected; existing evidence columns and
  scoped dedup index are sufficient.
- SQL ownership queries and generated sqlc code change additively.
- Existing pre-run normalized alert and Tencent detail behavior remains.
- Existing SSH inspect model output becomes more restrictive because secrets
  are redacted and `env`/`printenv` are no longer available.

## Failure Semantics

- Policy rejection: stable `invalid_arguments`, no SSH call.
- Adapter failure: existing safe connector classification.
- Projection/persistence failure: stable non-retryable
  `runtime_evidence_persistence`; raw output is discarded at the application
  boundary and is not model-visible.
- Budget exhaustion and cancellation preserve existing coordinator behavior.

## Rollback

Rollback can remove the expanded catalog entries and runtime writer wiring
without deleting evidence rows. Persisted evidence remains valid historical
data. Do not roll back by weakening command validation or exposing raw output.
