# Remediation Lifecycle Resilience Design

## Design Basis

The remediation model loop is already capable of multi-turn tool use. The
failure is in the surrounding control plane: service code currently prescribes
parts of the investigation order, loses trusted provider detail during
continuation, treats citation metadata mismatch as causal contradiction, and
turns bounded recovery failures into terminal business conclusions.

The redesigned boundary is:

```text
goal + trusted bootstrap + working-memory checkpoint + allowed tools
                              |
                              v
                     agent chooses next action
                              |
               +--------------+---------------+
               |                              |
          tool request                   phase proposal
               |                              |
      ToolGateway executes          fact/policy gates validate
               |                              |
        safe observation          accept or structured challenge
               +--------------+---------------+
                              |
                   checkpoint and continue
```

The coordinator remains the sole run-state owner. The model never gains
credentials, direct adapters, unrestricted execution, or transition authority.
The difference is that recoverable gate/tool/protocol outcomes return to the
same loop instead of becoming terminal states.

## D1. Goal-Oriented Prompt And Capability Catalog

Replace provider-specific ordering rules with a stable phase contract:

- original symptom and trusted provider-detail locators;
- exact deployed commit and repository identity;
- current objective and phase completion criteria;
- available bounded tools with safety/effect metadata;
- current working-memory checkpoint and remaining/reserved budget;
- the rule that the agent must verify its conclusion and may revise its plan.

A stack, path, function, or line is described as a high-value repository
locator. The agent may inspect it before logs, after logs, or between refined
queries. The harness does not infer that code evidence always eliminates the
need for runtime correlation, nor that runtime correlation is always mandatory.

Tools remain phase/policy filtered and execute only through ToolGateway. The
catalog records stable capability classes (`repository`, `provider_evidence`,
`runtime_logs`, `ssh_inspect`, `workspace`, `validation`, `publication`) so
recovery and exhaustion validation can reason about available paths without
encoding a diagnostic decision tree.

## D2. Durable Working Memory

Add a provider-neutral `WorkingMemoryCheckpointV1`:

```json
{
  "schemaVersion": "v1",
  "sequence": 7,
  "runId": "...",
  "seriesId": "...",
  "contextVersion": 3,
  "observedRunVersion": 12,
  "phase": "diagnosing",
  "objective": {"goal": "...", "completionCriteria": ["..."]},
  "verifiedFacts": [{"statement": "...", "evidenceIds": ["..."]}],
  "activeHypotheses": [{"id": "h1", "summary": "...", "evidenceIds": ["..."]}],
  "rejectedHypotheses": [{"id": "h0", "reason": "...", "evidenceIds": ["..."]}],
  "evidenceIndex": [{"evidenceId": "...", "kind": "...", "locator": "...", "contentHash": "..."}],
  "unresolvedQuestions": [{"question": "...", "material": true}],
  "phaseProgress": {"completed": ["..."], "remaining": ["..."]},
  "recoveries": [{"kind": "tool_failure", "action": "...", "outcomeRef": "..."}],
  "nextActions": ["..."],
  "budget": {
    "schemaVersion": "v1",
    "plan": {},
    "consumed": {},
    "closedPhases": [],
    "currentPhase": "diagnosing",
    "frontier": 0
  },
  "reason": "threshold|phase_boundary|recovery|process_shutdown"
}
```

Persist each checkpoint as an immutable event and update one latest snapshot in
the same transaction. Both carry a canonical content hash and optimistic
version. Loading rejects wrong run/series/context identities, unknown schema,
hash mismatch, a snapshot sequence not backed by an event, or a checkpoint
whose observed run version is ahead of durable run state. If durable run state
is ahead, the checkpoint is a stale projection and recovery rebuilds it from
the durable run, budget snapshot, evidence index, and artifacts before another
model turn. This makes the durable run transition authoritative in both crash
windows: a crash before transition resumes the matching checkpoint; a crash
after transition but before the after-checkpoint rebuilds the stale projection.

Checkpoint triggers are hybrid:

- automatic before the provider/context byte threshold is crossed;
- automatic when one bounded tool result creates configured output pressure;
- forced before and after every phase transition;
- forced before an orderly process shutdown when possible;
- recovery-triggered when the loop changes strategy after a challenge.

Provider-native conversation state is an optional in-memory accelerator behind
an additive capability interface. If it is absent or lost, the next turn is
rebuilt from the latest checkpoint, critical bootstrap locators, bounded recent
complete message groups, and evidence rehydration. No opaque provider state is
persisted in v1.

## D3. Evidence Index And Rehydration

Advertise `evidence.read` in phases that may need to verify a persisted fact.
Its model-visible input is:

```json
{"evidenceId": "uuid", "cursor": "optional-service-token"}
```

The service resolves the run to its series and authorizes the evidence against
project, lifecycle generation, deployed commit, and retention policy. The
model cannot select a URL, filesystem path, raw offset, or unbounded size.

The output is one server-sized page:

```json
{
  "evidenceId": "...",
  "kind": "provider_detail",
  "storedClassification": "direct_fault",
  "provenance": {"sourceId": "...", "sourceAttempt": 1},
  "contentHash": "...",
  "content": {},
  "nextCursor": "...",
  "truncated": true
}
```

The cursor is an opaque random capability backed by server-side state. The
server stores only its digest together with run, evidence, content-hash, offset,
and expiry bindings; model-visible evidence IDs, hashes, source code, and cursor
payloads are insufficient to mint or modify one. The page size uses the existing
evidence/model-visible output bound. Evidence is
the operational payload already admitted at ingestion; credentials and
authority-bearing URLs remain structurally excluded. Reads are normal audited
tool invocations and update the checkpoint evidence index.

Continuation loads the latest valid series checkpoint plus a bounded index of
all authorized evidence kinds through the predecessor attempt. It does not
inline all raw content and does not use the current runtime-only query as the
definition of reusable evidence. Evidence admitted before a run records the
incident lifecycle generation and deployed commit observed at ingestion. Both
the continuation index and `evidence.read` require that baseline to equal the
series baseline; legacy unbound pre-run rows are not continuation-readable.

## D4. EvidenceGate Becomes Fact Gate And Challenge Producer

Split gate work into two outputs:

1. `FactCheckDecision` resolves evidence IDs, replaces optional model-declared
   classifications with stored classifications, verifies provenance and cited
   fields, and identifies genuine content contradictions.
2. `RecoveryChallengeV1` reports correctable citation/factual problems to the
   same conversation.

The gate never mutates `DiagnosisOutput.Fixability` or confidence. A citation
classification mismatch produces an `evidence_correction` challenge with the
stored classification. Material contradictions require conflicting evidence
content or mutually incompatible factual claims; metadata disagreement alone
is not causal contradiction.

There is no universal matrix requiring time, request ID, operational bridge,
host identity, and source coverage for every `code_fixable` conclusion. The
diagnosis declares which unresolved facts are material. The service verifies
the cited facts and challenges an unsupported materiality claim; it does not
substitute its own causal conclusion. A corrected, fact-valid `code_fixable`
diagnosis proceeds to planning.

Persist both the submitted diagnosis and gate decision/challenge. Public review
uses the accepted diagnosis; audit can show that a correction occurred without
storing raw model turns.

## D5. Unified Recovery Protocol

Use one safe envelope across phases:

```json
{
  "schemaVersion": "v1",
  "kind": "evidence_correction|protocol_correction|tool_failure|context_rehydration|validation_revision|publication_retry",
  "severity": "recoverable|policy_blocked|hard_terminal",
  "reasonCode": "...",
  "failedActionRef": "...",
  "availableCapabilities": ["repository", "runtime_logs"],
  "suggestedRecoveryClasses": ["correct_request", "use_alternative", "rehydrate_evidence"],
  "attempt": 2,
  "remainingBudget": {},
  "message": "bounded safe guidance"
}
```

The harness counts a failure fingerprint and detects unchanged no-progress
cycles, but it does not impose a universal one-retry rule. Parameter changes,
new evidence, a different tool/capability, or a changed hypothesis are progress.
When an unchanged action is no longer useful, the challenge closes that action
and advertises remaining capability classes.

Existing adapter-local retry schedules remain independent. Persistence and
consistency failures bypass the model loop because continuing from an
unreliable durable state is unsafe.

## D6. Exhaustion Proof And Terminal Authority

Replace direct terminalization of model `stop`, empty collection requests,
Docker refinement exhaustion, and collect-loop counters with
`ExhaustionProposalV1`:

```json
{
  "unresolvedGoal": "...",
  "attemptedPaths": [{"capability": "repository", "outcomeRefs": ["..."]}],
  "untriedCapabilities": [{"capability": "runtime_logs", "reasonCode": "unavailable", "evidenceRefs": ["..."]}],
  "bestConclusion": "insufficient_evidence",
  "handoff": "safe actionable suggestion"
}
```

The service validates that:

- every referenced action/evidence belongs to the run/series;
- every currently advertised capability is attempted or has a factual,
  policy-backed reason not to be used;
- no correctable protocol/context/tool recovery remains;
- no unaccounted permitted capability exists;
- remaining/reserved budget cannot support a declared viable action.

The validator checks coverage, not investigative order and not whether every
tool was called. An irrelevant capability may be excluded when the agent gives
a fact-supported materiality reason. An incomplete proof returns a recovery
challenge. An accepted proof may transition to `blocked_manual_review` with a
specific terminal reason and handoff.

Other allowed early terminal classes are safety/permission denial, persistence
or consistency failure, unrecoverable snapshotted configuration, and hard
global budget exhaustion. Normal non-code conclusions and the final human merge
gate retain their existing semantics.

## D7. Reserved Budget Model

Extend the run budget snapshot with:

- one existing global hard ceiling for elapsed time, model/tool calls, cost, and
  evidence/repository bytes;
- configurable soft allocations for diagnosis, planning, patching, validation,
  and publication;
- minimum later-phase reserves and a recovery reserve;
- an unreserved pool containing unused/borrowable capacity.

Phase admission consumes its soft allocation, then only the unreserved pool.
Crossing a soft allocation produces a recoverable budget challenge or forces a
checkpoint; it is not `budget_exhausted`. Unused allocation flows forward after
the phase checkpoint. Only the global hard ceiling produces the existing hard
terminal state.

`BudgetPlanV1` is immutable after run creation. A stable
`BudgetPlanRecoveryV1` records per-phase consumption, closed phases, current
phase, and frontier; it is embedded in checkpoints and restored through a pure
validated constructor. Private allocator fields are not serialized directly.
Restoration must reproduce the same projection and next admission decision at
every phase boundary.

## D8. Run/API/UI Projection

Do not add a new RunState for every recovery type. The run remains in its true
active phase and stores a current bounded recovery projection:

```json
{
  "recovery": {
    "active": true,
    "kind": "tool_failure",
    "reason": "connector timeout",
    "attempt": 1,
    "attemptedPaths": ["runtime_logs"],
    "nextAction": "inspect repository locator",
    "remainingBudget": {}
  },
  "checkpoint": {
    "sequence": 7,
    "phase": "diagnosing",
    "reason": "recovery",
    "updatedAt": "..."
  }
}
```

The review API returns these optional objects additively. The incident panel
shows “recovering” with phase, safe reason, attempted capability classes, next
action, checkpoint age, and budget summary. It displays the existing manual-fix
panel only after a verified terminal blocker. It never renders raw checkpoints,
raw model turns, connector errors, or evidence payloads.

## D9. Staged Delivery And Compatibility

Add versioned project remediation policy
`agentLoopMode=legacy|resilient_v1`. Project configuration is the authorized
write/read source. Root run creation reads the policy and version in the same
transaction that inserts the run snapshot, so a mid-run configuration change
cannot alter semantics. Continuation creation copies mode and policy version
from its predecessor. Default existing and migrated projects to `legacy`.

Delivery order:

1. persistence schema, working-memory store, recovery contracts, evidence read,
   reserved budget, and additive API types;
2. diagnosis loop and `INC-2270` regression;
3. planning protocol/gate recovery;
4. patch and validation revision recovery;
5. idempotent publication recovery and full-lifecycle restart tests;
6. project-by-project enablement, followed by an explicit later decision on the
   default.

Legacy mode preserves current behavior during rollout. New tables/columns are
additive and retained on rollback; disabling the policy routes new runs through
legacy behavior. In-progress runs keep their snapshotted mode.

## Risks

- Working memory could promote summaries into facts. Schema validation and
  mandatory evidence IDs for verified facts prevent that authority escalation.
- Exhaustion validation could become a new hard-coded decision tree. It may
  validate only catalog coverage, provenance, policy, progress, and budget; it
  must not encode tool order or provider-specific causal requirements.
- Recovery could loop indefinitely. Global ceilings, no-progress fingerprints,
  phase reserves, and exhaustion challenges bound the loop without converting
  the first failure into a terminal result.
- Cross-attempt evidence could be stale. Series, lifecycle generation, deployed
  commit, context version, content hash, and source attempt remain explicit on
  every index/read result.
