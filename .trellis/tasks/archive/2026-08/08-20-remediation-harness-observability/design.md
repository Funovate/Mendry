# Remediation Harness Observability Design

## Scope

This change adds a console-only, correlated remediation execution trace. It does
not add persistence, migrations, APIs, frontend state, or a vendor integration.
Normal progress is visible at `INFO`; redacted payload detail is visible only when
`FIXTHE_LOG_LEVEL=debug`.

## Boundaries

### Application observation port

Add an application-owned observer port with typed records for:

- run start and terminal completion;
- successful state transitions;
- initial repository/evidence context operations;
- model-turn completion and request/response payloads;
- tool-call completion and request/result payloads.

The records carry run identity, phase, sequence, duration, outcome, counters, and
credential-free domain values. They never carry adapter clients, decrypted
credentials, authorization headers, authenticated Git URLs, SSH command lines, or
temporary key paths.

Existing constructors remain source-compatible and install a no-op observer. A new
runtime constructor accepts the observer for bootstrap wiring. This keeps unit-test
fakes small and preserves the frozen domain ports.

### Logging adapter

The remediation logging adapter implements the observation port using the injected
process `*slog.Logger`. It maps typed observations to stable events and fields owned
by `internal/platform/observability`.

Progress events:

| Event | Level | Purpose |
|---|---|---|
| `remediation.run.started` | INFO | Durable run identity and trigger metadata |
| `remediation.state.transitioned` | INFO | Successful `from_state` to `to_state` change |
| `remediation.context.completed` | INFO | Repository/evidence context operation outcome and size |
| `remediation.model_turn.completed` | INFO | Phase, sequence, duration, model usage, envelope kind, outcome |
| `remediation.tool.completed` | INFO | Tool, phase, sequence, duration, bytes, outcome/rejection code |
| `remediation.run.completed` | INFO/ERROR | Terminal state, duration, aggregate counters, outcome |

Payload events:

| Event | Level | Payload kinds |
|---|---|---|
| `remediation.context.payload` | DEBUG | repository tree, initial evidence page |
| `remediation.model_turn.payload` | DEBUG | logical model request, model response |
| `remediation.tool.payload` | DEBUG | tool parameters, credential-free adapter result |

Every event includes `run_id`; `series_id`, `incident_id`, generation, and phase are
included where known. OpenTelemetry `trace_id` and `span_id` continue to come from
`observability.Log`; callers do not copy or synthesize them.

### Adapter completion records

- Inject the process logger into remediation Git and SSH readers through optional
  constructor fields.
- Git network operations emit the existing `git.request.completed` contract using
  only the public remote identity. Instrument clone and fetch; do not log the
  authenticated target, `GIT_SSH_COMMAND`, credential, or temporary key path.
- SSH evidence reads emit a new metadata-only completion event with operation,
  configured host identity, duration, outcome, and failure class. Raw stdout is
  emitted only through the redacted remediation context/tool payload event.
- Keep the existing `llm.request.completed` provider record for wire-level provider
  troubleshooting. The remediation model-turn events add run/phase/sequence and
  the 64 KiB logical payload view.

## Data Flow

```text
Coordinator/Assembler/AgentEngine/ToolGateway
    -> typed credential-free observation
    -> remediation logging adapter
    -> redact complete payload
    -> SHA-256 complete redacted payload
    -> UTF-8-safe 64 KiB prefix + size/truncation metadata
    -> observability.Log
    -> console or JSON handler
```

The payload record is emitted after the corresponding operation has produced a
bounded domain value. A failure emits completion metadata and, only when a safe
request or partial response exists, a redacted payload record.

## Payload Contract

The shared remediation payload snapshot helper returns:

```go
type PayloadSnapshot struct {
    Text        string
    Bytes       int
    LoggedBytes int
    Truncated   bool
    SHA256      string
}
```

Processing order is fixed:

1. Serialize structured values deterministically as JSON; plain text remains text.
2. Redact sensitive JSON keys and secret-shaped text from the complete payload.
3. Compute byte count and SHA-256 from the complete redacted payload.
4. Truncate the logged prefix to 64 KiB on a valid UTF-8 boundary.

The hash never fingerprints the unredacted secret-bearing input. Empty payloads may
omit the payload event. `payload_truncated=true` is explicit whenever bytes were
discarded.

Redaction covers, at minimum:

- case-insensitive JSON keys containing password, token, secret, authorization,
  API-key, credential, or plaintext value markers;
- `Bearer` and `sk-...` values;
- HTTPS userinfo;
- PEM private-key blocks;
- password/token/secret/authorization/API-key/credential assignments in text.

This is defense in depth. Plaintext credentials remain confined to adapters and
must not be deliberately passed into an observation just because the redactor
exists. Credential references, kind, transport, and resolution/authentication
outcome may be separate metadata.

## Console Projection

JSON logs retain the payload as a structured string field with complete IDs and
metadata. The human console handler removes the payload from quoted inline
attributes and writes it as a labeled following-line block while holding the
existing shared output mutex. This matches stack, SQL, and inbound-debug atomicity
and prevents concurrent remediation payloads from interleaving.

## Lifecycle Placement

- Emit run start after `CreateSeriesAndRun` returns a queued run.
- Emit transitions only after the store transition commits successfully.
- Time repository and evidence assembly separately.
- Give model turns and tool calls monotonically increasing in-process sequence
  numbers for log reconstruction; database sequencing remains unchanged.
- Emit terminal completion after the final aggregate is loaded. On failure, emit
  the terminal failure observation in addition to the existing boundary-owned
  `remediation.failed` diagnostic; the two events serve progress and root-cause
  purposes and must not duplicate error stacks.

## Compatibility

- No domain port method changes.
- Existing remediation constructors continue to work with a no-op observer.
- Existing stable `remediation.failed`, `llm.request.completed`, and
  `git.request.completed` contracts remain valid.
- No schema, generated SQL, API response, or frontend changes.
- Logger fields/events are additive. Default `INFO` does not contain payloads.

## Rollback

The change is code-only. Rolling back the binary removes the new events without
data migration or cleanup. Consumers must treat the new event names as additive
and tolerate their absence during mixed-version rollout.

## Risks

- The console handler is shared by all backend logs. Payload block projection needs
  focused concurrency and JSON parity tests.
- Source/evidence can contain unknown secret formats. Tests cover known credential
  classes, while adapter isolation remains the primary control.
- DEBUG volume can be high. The 64 KiB per-payload cap and existing bounded tool
  reads limit each record, but operators should enable DEBUG only for diagnosis.
- GitNexus reports LOW risk for the planned symbols after re-indexing; direct source
  inspection remains authoritative because the worktree contains uncommitted files.
