# Expand SSH read-only diagnostics

## Goal

Give the remediation agent enough bounded, read-only SSH access to inspect a
host and correlate runtime evidence itself, including when the configured
deployment is Docker. The harness must support host, network, process, file,
system-log, and container diagnostics without granting mutation, privilege
escalation, shell escape, or unbounded execution capabilities.

## Background

- Docker deployments currently advertise typed `docker.logs` but suppress the
  general `ssh.inspect` tool, so the agent cannot inspect host identity such as
  `hostname -I` or `ip addr show`.
- Incident `INC-2267` exposed the consequence: Tencent CLS identified
  `VM-6-17-tencentos` / `10.16.6.17`, while the SSH source used public address
  `43.131.29.186`; the model had no host-network observation with which to
  reconcile those identities.
- The existing inspect parser reconstructs quoted argv and rejects shell
  substitution, redirection, mutation-oriented `find` options, `sudo`, and
  relative parent traversal. It already bounds output and execution time.
- A binary-name allowlist alone is insufficient. Existing allowed binaries
  such as `date`, `hostname`, and `env` have argument forms that mutate the host
  or execute another program. New command families such as `ip`, `docker`, and
  `systemctl` also mix read and write operations.
- The user selected an expanded read-only allowlist rather than a permissive
  denylist or per-command human approval.

## Requirements

- R1. Every enabled, supported SSH source MUST advertise `ssh.inspect`,
  including sources whose deployment kind is Docker. Typed `docker.logs` MUST
  remain available for bounded incident-window log collection.
- R2. The inspect policy MUST authorize common bounded diagnostics across host
  identity, networking, processes, files, system logs, and Docker runtime
  state. Required examples include `hostname -I`, `ip addr show`, `ss -lntp`,
  `ps`, `cat`/`head`/`tail`/`grep`/`find`/`stat`, bounded `journalctl`, and
  read-only Docker `ps`/`inspect`/`logs`/`top`/`stats` forms.
- R3. Authorization MUST be based on the complete parsed command, not only the
  first binary. Each command family with mutating forms MUST have explicit
  read-only subcommand and option validation.
- R4. The gateway MUST continue to parse without executing a model-provided
  shell string and MUST reconstruct quoted argv before SSH. Shell chaining,
  command substitution, redirection, environment assignment, unquoted glob
  expansion, and unsupported pipeline forms remain rejected before SSH.
- R5. Mutation and privilege escalation MUST be rejected, including file
  creation/deletion/rename, in-place editing, process or socket termination,
  service state changes, host/network configuration changes, Docker lifecycle
  or `exec` operations, system-time/hostname changes, package management,
  interpreters, and nested arbitrary command execution.
- R6. Existing output byte limits, command timeout, run budgets, project/source
  authorization, credential isolation, and model-boundary sanitization MUST not
  be weakened. `env` and `printenv` MUST no longer be authorized because their
  output can disclose credentials and unrelated process configuration.
- R7. Cloud, MCP, and repository tools MUST retain their existing behavior.
- R8. Policy rejections MUST remain inspectable as stable tool rejection
  outcomes and MUST not open an SSH connection.
- R9. Every successful `ssh.inspect` and `docker.logs` result used for
  diagnosis MUST be persisted as bounded, sanitized, project/source/run-owned
  `remediation_evidence` before the next model turn. The model-visible tool
  observation MUST expose the resulting evidence ID, and a diagnosis relying
  on that result MUST cite the persisted ID. The persisted payload and the
  model-visible payload MUST be the same canonical projection.
- R10. Persisted SSH/Docker evidence MUST be reusable by review and eligible
  continuation attempts under the existing lifecycle-series ownership rules;
  retries MUST remain idempotent and must not duplicate identical evidence.

## Security Constraints

- "Read-only" means the allowed command and its accepted arguments do not
  intentionally alter host, process, network, service, container, repository,
  or filesystem state. Merely omitting `rm -rf` is not sufficient.
- Commands capable of launching another executable are rejected unless the
  nested operation is independently parsed and authorized; the initial scope
  does not require nested execution.
- Potentially streaming operations such as Docker logs/stats and journal reads
  must use non-following forms and remain bounded by the harness timeout and
  output cap.
- Existing inspect output handling is tightened for durable evidence: PEM
  material, temporary key paths, tokens, passwords, authorization values, and
  other credential-shaped fields/text are redacted before persistence and
  before the model sees the result. Raw unredacted SSH/Docker output MUST NOT be
  written to evidence, tool-invocation rows, or operator logs.

## Acceptance Criteria

- [x] AC1: A Docker-backed SSH source advertises both `ssh.inspect` and
      `docker.logs`; a host-backed SSH source continues to advertise
      `ssh.inspect`.
- [x] AC2: Unit tests demonstrate successful parsing and gateway execution of
      the required host/network/process/file/log/Docker read-only examples.
- [x] AC3: Table-driven tests reject mutating variants for every mixed-purpose
      command family, including `hostname <name>`, `date --set`, `env <cmd>`,
      `ss --kill`, `ip ... add/del/set`, mutating `find` actions,
      `journalctl --vacuum-*`/state-changing options, and Docker
      `exec`/`run`/`stop`/`restart`/`rm`/write-oriented operations.
- [x] AC4: Tests prove shell substitution, redirection, command separators,
      unsupported pipelines, interpreters, privilege escalation, and nested
      command execution are rejected before the SSH adapter is called.
- [x] AC5: Read-only pipelines of at most three segments continue to work only
      when every segment independently satisfies its command policy.
- [x] AC6: Docker and host deployment catalog tests cover the expanded tool
      advertisement without changing Cloud/MCP/Git catalogs.
- [x] AC7: Inspect output remains bounded; SSH and Docker evidence share one
      canonical secret-redacted projection for persistence and model context.
      `env`, `printenv`, and streaming/follow options are rejected.
- [x] AC8: Successful `ssh.inspect` and `docker.logs` calls create bounded,
      sanitized evidence rows with stable classification, provenance,
      deduplication key, ownership, content hash, and model-visible evidence ID.
- [x] AC9: A diagnosis that relies on SSH/Docker output cites the corresponding
      persisted evidence ID; citation resolution, review reads, retries, and
      continuation ownership checks succeed without duplicating evidence.
- [x] AC10: Persistence failure is surfaced as a stable tool/runtime failure and
      the unpersisted output is not presented to the model as citable evidence.
- [x] AC11: `go test ./internal/modules/remediation/...`, remediation race tests,
      backend-wide tests, `go vet`, command builds, and `git diff --check` pass.

## Out of Scope

- Arbitrary shell access, general-purpose interpreters, or a denylist-based
  command sandbox.
- Write access, repair execution, deployment changes, container lifecycle
  changes, or privilege escalation.
- Human approval UI for individual SSH commands.
- Changes to SSH credential storage or transport authentication.

## Notes

- Parent task: `08-14-agentic-remediation-harness`.
- Prior inspect work was absorbed into
  `08-21-remediation-tool-driven-context`; this task extends that implemented
  boundary rather than implementing the obsolete standalone
  `08-21-ssh-inspect-tool` task.
