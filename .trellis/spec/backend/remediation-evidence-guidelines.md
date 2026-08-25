# Remediation Evidence Guidelines

## Scenario: Trusted Evidence Gate And Docker Runtime Sources

### 1. Scope / Trigger

Use this contract when changing normalized webhook evidence, remediation
bootstrap context, source coverage, the service-owned diagnosis gate, or the
read-only Docker path behind an SSH source. The application gate decides
whether a model diagnosis may enter planning; model confidence alone is never
the authority.

### 2. Signatures

```go
type EvidenceResolver interface {
    ResolveEvidence(context.Context, string, []domain.EvidenceCitation) (domain.EvidenceResolution, error)
}

func (*application.EvidenceGate).Evaluate(context.Context, string, *application.DiagnosisOutput) (domain.EvidenceGateDecision, error)
func (*application.EvidenceGate).Apply(context.Context, string, *application.DiagnosisOutput) (*application.DiagnosisOutput, domain.EvidenceGateDecision, error)
func domain.EvaluateEvidenceGate(domain.EvidenceGateInput) domain.EvidenceGateDecision

func (*postgres.RunStore).ResolveEvidence(context.Context, string, []domain.EvidenceCitation) (domain.EvidenceResolution, error)
func (*postgres.RunStore).PersistEvidenceAssessment(context.Context, domain.EvidenceAssessment) error
```

The Docker runtime port accepts only the saved exact container name and a
bounded time/line request. It resolves the current container ID internally;
the ID is runtime evidence, never durable configuration or model input.

### 3. Contracts

- Direct evidence citations are resolved against the current run and its
  triggering observation under project/incident ownership. Unknown, unavailable,
  cross-run, or cross-project IDs remain missing evidence.
- Persisted resolver records, source coverage, correlation, and contradictions
  are authoritative. If the resolver has no persisted time assessment, the gate
  preserves the diagnosis `TimeAssessment` because the diagnosis was produced
  from the raw bootstrap time fields; an empty resolver field must not erase it.
- `0.00-0.39` is low, `0.40-0.69` is medium, and `0.70-1.00` is high. Missing
  direct fault evidence caps at `0.39`; unresolved time, host/source coverage,
  or material contradictions cap at `0.69`.
- Planning requires effective confidence of at least `0.70`, a trusted direct
  citation, temporal and operational correlation, inspected primary coverage or
  a direct bridge, no material contradiction, and causal closure to the original
  symptom. Unsupported `code_fixable` becomes `insufficient_evidence`.
- SSH Docker discovery is bounded inventory only. Runtime reads use fixed
  `docker logs --since --until --tail` after exact-name resolution. `exec`,
  attach, copy, lifecycle, mutation, arbitrary flags, and all-container sweeps
  are unavailable.
- The two subprocess output writers share one synchronized capture. Combined
  stdout/stderr byte limits and the truncation marker remain correct when the
  streams write concurrently.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Unknown or cross-run evidence ID | Mark missing; do not trust model classification |
| Missing direct fault record | Cap `0.39`; persist `insufficient_evidence` |
| Resolver lacks time assessment | Retain structured diagnosis time assessment; resolver contradictions still win |
| Unresolved time/host/primary source/material contradiction | Cap `0.69` unless a trusted direct bridge closes the gap |
| Code-only or unsupported fixability claim | Keep hypothesis non-actionable; never enter planning |
| Missing exact Docker name after recreation | Source unavailable; do not choose a similarly named container |
| Docker inventory/log command exceeds bound or requests mutation | Reject before SSH/remote execution |
| Concurrent stdout/stderr capture | Serialize state, buffers, and truncation accounting; no race or byte overrun |

### 5. Good/Base/Bad Cases

- Good: the resolver verifies a direct Tencent/Docker record and source
  coverage, while the diagnosis supplies a high-certainty epoch comparison;
  the gate merges both and permits planning only after causal closure.
- Base: a sparse or anchor-only callback starts bounded collection and is shown
  to operators, but remains non-actionable without a direct correlated fault.
- Bad: accept `evidenceId` or `classification` because the model supplied it,
  overwrite a valid time assessment with a nil resolver field, or use a stale
  discovery container ID after recreation.

### 6. Tests Required

- Domain gate tests cover missing direct evidence, unresolved time/correlation,
  direct bridges, contradiction caps, causal closure, and high-confidence
  eligibility.
- Application gate tests prove citations are resolver-owned and prove a
  resolver without persisted time does not erase the diagnosis time assessment.
- SSH/Docker tests cover exact-name re-resolution, stopped/restarting inventory,
  bounded logs, missing/ambiguous names, prohibited operations, and concurrent
  stdout/stderr capture under `go test -race`.
- Cross-layer checks run `make check`, sqlc generation/hash validation, frontend
  lint/typecheck/unit/build/E2E, and `git diff --check`.

### 7. Wrong vs Correct

#### Wrong

```go
resolution = resolverResult
// A nil resolver time silently discards the raw time interpretation from the
// triggering evidence and blocks every otherwise valid diagnosis.
```

#### Correct

```go
resolution = resolverResult
if resolution.Time == nil {
    resolution.Time = diagnosis.TimeAssessment
}
// Resolver-owned citations and coverage stay authoritative; only absent
// structured fields are filled from the bounded first-turn assessment.
```
