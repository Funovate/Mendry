# Incident Extensible Connector Framework

## Goal

Add the next connector forms without changing the shared observation,
evidence, grouping, lifecycle, or reporting behavior.

## Parent References

`08-10-production-incident-mvp/prd.md`, `design.md`, and `implement.md`.

## Dependencies

Requires `08-10-incident-service-foundation`,
`08-10-incident-domain-lifecycle`, and `08-10-incident-ingestion-cls`. It
extends the shared contracts proven by the CLS connector rather than defining
parallel connector behavior.

## Requirements

- Provide a generic read-only HTTP JSON connector with explicit endpoint,
  authentication-secret reference, pagination strategy, JSON mapping, and
  capability validation.
- Define SSH connector configuration and a read-only command policy/schema;
  do not implement an SSH executor or permit an SSH write action.
- Keep the connector contract compatible with a later SSH MCP adapter that can
  separately declare `log.read` and `diagnostic.execute`. That adapter may stage
  and run a reviewed, environment-specific script artifact under the backend
  scaffold's runner/review policy; it must not accept an arbitrary shell command
  string or cause file-reading MCP source content to become executable.
- Add contract tests proving webhook, MCP, and HTTP JSON implementations obey
  the same observation/evidence interfaces.

## Disposition

Archived 2026-08-20. Source schemas shipped through `08-12`; SSH read execution belongs to the remediation harness. This extensible connector-runtime contract is expired. A future pull-collection task must define its own boundary against webhook ingest and harness evidence ports.

## Acceptance Criteria

- [ ] An HTTP JSON fixture connector maps configured paginated payloads to the
      shared contracts without modifying core grouping or report code.
- [ ] Connector validation rejects unsafe methods, unconfigured mappings, and
      capability-incompatible collection requests.
- [ ] SSH configuration can be stored and inspected as metadata while no SSH
      network call, command execution, or write capability exists.
- [ ] The stored SSH/MCP descriptor can later add `diagnostic.execute` without a
      connector-contract change, while the current task still makes no SSH
      network call and executes no remote script.
- [ ] Shared connector contract tests pass for webhook, MCP fake, and HTTP JSON
      fixture implementations.
