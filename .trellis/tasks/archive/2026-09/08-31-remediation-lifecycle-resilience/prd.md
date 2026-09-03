# Remediation Lifecycle Resilience

## Goal

Make remediation a persistent agent workflow across diagnosis, planning,
patching, validation, and publication. A bounded tool failure, malformed model
turn, evidence citation correction, context compaction, or unavailable evidence
source must be recoverable. Automation may end only for a verified hard
blocker or after the agent proves that all viable paths are exhausted and the
service validates that proof.

The harness owns authority, evidence authenticity, permissions, budgets,
persistence, recovery, and auditability. The agent owns investigation and
repair strategy. In particular, the harness must not prescribe a fixed order
such as Docker logs before repository inspection when a trusted alert already
contains a stack, source path, function, or line number.

## Background

`INC-2270` exposed two independent harness failures even though the model/tool
loop itself worked:

- The first attempt retried Docker, fell back to SSH, inspected logs and code,
  and returned `code_fixable` with high confidence and causal closure. The
  service-owned EvidenceGate silently rewrote it to `insufficient_evidence`
  because a citation classification differed from the stored classification;
  the mismatch became a material contradiction and capped confidence just
  below the planning threshold.
- The second attempt loaded only prior runtime evidence. It omitted the
  persisted trusted provider detail, reducing the usable evidence from roughly
  46 KiB to 1.8 KiB and causing the model to report that the raw error/stack and
  time evidence were unavailable.

The current coordinator also terminalizes a model `stop`, an
`insufficient_evidence` result without explicit tool calls, repeated Docker
refinement failure, or collection-loop exhaustion as manual review. These are
control-loop states, not necessarily business conclusions.

The target behavior follows the long-running patterns documented for Codex and
Claude Code: persistent objectives and constraints, tool-mediated execution,
automatic compaction with durable context, resumable sessions, structured
feedback after invalid actions, and verification before declaring completion
or abandonment.

## Requirements

### Agent autonomy

- R1. The first diagnosis turn MUST receive bounded original trusted
  alert/provider detail, including available error, stack, source path, line,
  raw time values, provider identity, and evidence IDs.
- R2. The agent MUST be free to choose repository, evidence, SSH, Docker, or
  other allowed read tools in any useful order. A source path/line or stack is
  a strong code-localization hint, not a deterministic log-skipping rule.
- R3. If code inspection does not establish a causal explanation, the agent
  MUST be able to search runtime evidence, refine failed queries, switch tools,
  and return to code without creating a new attempt.
- R4. Prompts MUST express the outcome, constraints, available capabilities,
  and completion criteria. They MUST NOT encode provider-specific diagnostic
  decision trees such as mandatory Docker-before-repository ordering.

### Evidence authority and rehydration

- R5. The service MUST remain authoritative for evidence identity, ownership,
  stored classification, provenance, content hash, permissions, and safety.
- R6. EvidenceGate MUST NOT silently change causal or fixability conclusions.
  A failed fact check MUST return a structured challenge to the same agent loop
  so it can correct citations, re-read evidence, or explain why the disputed
  fact is not material.
- R7. A citation classification mismatch MUST be treated as correctable
  metadata. It MUST NOT by itself become a material evidence contradiction.
- R8. Request ID, host bridge, and time-zone reconciliation MUST NOT be global
  prerequisites for every code fix. The agent must state whether a missing
  correlation is material to its particular causal chain, and the service must
  validate only factual claims and policy constraints.
- R9. An `evidence.read` tool MUST let the agent re-read bounded pages of trusted
  evidence by evidence ID. It MUST reject arbitrary URLs, paths, raw offsets,
  cross-project access, and evidence outside the current remediation series.
  Continuation cursors MUST be server-authoritative capabilities whose offset,
  expiry, and evidence binding cannot be forged from model-visible response
  fields or repository source.
- R10. A continuation MUST retain a structured evidence index and permit
  re-reading provider, runtime, repository, and artifact evidence from earlier
  attempts in the same series. Evidence MUST NOT cross lifecycle generation or
  deployed commit boundaries. Pre-run evidence MUST carry a verifiable
  observation baseline; legacy rows without one fail closed for continuation
  indexing and `evidence.read`.

### Durable working memory and compaction

- R11. The harness MUST persist versioned working-memory checkpoints containing
  objectives, verified facts with evidence IDs, active and rejected hypotheses,
  unresolved material questions, phase progress, recovery attempts, evidence
  index, next intended actions, and a complete, versioned budget recovery
  snapshot including the immutable plan, consumption, closed phases, current
  phase, and allocation frontier.
- R12. Checkpoints MUST use append-only events plus a latest snapshot. Updating
  the snapshot and appending its event MUST be transactional, versioned, hashed,
  and scoped to the run/series/context version. Durable run state/version is the
  recovery authority: an older checkpoint is rebuilt, a future checkpoint is
  rejected, and both phase-transition crash windows have deterministic rules.
- R13. Checkpointing MUST be triggered both automatically near context/tool
  output limits and forcibly at diagnosis, planning, patching, validation, and
  publication boundaries.
- R14. Provider-native compaction or session continuation MAY accelerate an
  in-process run. Opaque native session/compaction blobs MUST NOT be persisted
  in v1; process restart MUST reconstruct from durable checkpoints, evidence
  indexes, artifacts, and run records.
- R15. Summaries and checkpoints MUST never become evidence. Every verified
  factual claim in working memory must retain evidence IDs, and original
  evidence must remain re-readable within retention and authorization policy.

### Recovery and terminal policy

- R16. Tool failures, protocol/envelope errors, evidence challenges, context
  truncation, validation failures, and transient publication failures MUST use
  one structured recovery protocol with safe error classes, attempts, remaining
  budget, available capability classes, and suggested recovery classes.
- R17. Recovery MUST be progress-aware. The harness may stop repeating an
  unchanged failing action, but failure of one action MUST close only that
  path, not the run, while another permitted path remains viable.
- R18. A model `stop`, `insufficient_evidence` without tool calls, collection
  loop limit, or repeated connector failure MUST be normalized to an exhaustion
  proposal or a specific policy blocker; none may directly enter manual review.
- R19. To claim all viable paths are exhausted, the agent MUST provide the
  unresolved goal, attempted paths and observation/evidence references,
  untried capabilities, and why each untried capability is unavailable,
  irrelevant, unsafe, or budget-prohibited.
- R20. The service MUST validate exhaustion against the snapshotted capability
  catalog, policy, budgets, evidence ownership, and recovery history. It MUST
  challenge incomplete proposals, but MUST NOT require a fixed investigation
  order or force every tool to be called.
- R21. Only safety/permission denial, persistence or consistency failure,
  unrecoverable configuration error, global hard budget exhaustion, or an
  accepted exhaustion proof may terminate automation before the normal human
  merge/review gate.

### Budgeting, product state, and rollout

- R22. Each run MUST have one global hard ceiling, soft phase budgets, and
  minimum reserves for later phases and recovery. Early phases may borrow only
  unreserved capacity; unused capacity may flow forward. The allocator MUST
  serialize and restore without changing any phase-boundary admission result.
- R23. Recoverable conditions MUST keep the run active in its current phase.
  Review API/UI MUST show a safe recovery summary, attempted path classes,
  next action, checkpoint age, and remaining budget without exposing raw model
  traces, credentials, or unbounded tool output.
- R24. “Manual fix” MUST appear only for a verified terminal blocker. Missing
  evidence while recovery remains possible must appear as active recovery.
- R25. Delivery MUST be staged behind a project policy snapshotted by the run:
  `agentLoopMode=legacy|resilient_v1`. The common recovery/checkpoint kernel is
  implemented first, diagnosis is enabled first, and later phases are enabled
  sequentially without changing the architecture. A root run atomically
  snapshots the current versioned project policy; every continuation inherits
  its predecessor's mode and policy version rather than rereading the project.

## Acceptance Criteria

- [ ] AC1. An `INC-2270` regression proves that trusted stack/path/line evidence
      can lead the agent to inspect the exact deployed code first, correct an
      evidence-classification challenge, preserve `code_fixable`, and enter
      planning without manual intervention.
- [ ] AC2. A continuation regression proves that a later attempt can use
      `evidence.read` to retrieve the earlier trusted provider detail and does
      not degrade to runtime-only context. It also proves that knowledge of an
      evidence ID/content hash cannot forge a positive offset or later expiry,
      and that pre-run evidence from another generation or deployed commit is
      rejected.
- [ ] AC3. A code-first scenario that remains ambiguous proves the agent can
      autonomously switch to SSH/Docker/log tools, refine its query, and return
      to repository analysis.
- [ ] AC4. Docker, SSH, MCP, or log-API failure produces a recoverable challenge;
      an alternative available tool remains usable and the run stays active.
- [ ] AC5. Citation classification mismatch is corrected through the agent loop
      and cannot independently reduce confidence or create a material
      contradiction.
- [ ] AC6. Malformed envelopes, oversized tool observations, context compaction,
      and provider-native continuation loss recover without changing the
      diagnosis into `insufficient_evidence`.
- [ ] AC7. Restart tests at every phase boundary reconstruct the run from the
      latest durable checkpoint without an opaque provider session, including
      crashes immediately before and immediately after the durable transition.
- [ ] AC8. The service rejects an early or incomplete exhaustion proposal and
      returns a challenge listing the uncovered capability/recovery class.
- [ ] AC9. A complete, evidence-backed exhaustion proposal is accepted only
      when every viable capability is covered and no permitted recovery remains.
- [ ] AC10. Diagnosis cannot consume minimum planning, repair, validation, or
      recovery reserves; only the global hard ceiling yields
      `budget_exhausted`. Serialize/restart/continue tests cover every phase
      boundary and preserve the same allocator projection and decisions.
- [ ] AC11. Planning correction, patch revision, validation repair, and
      idempotent publication retry use the same recovery/checkpoint contracts.
- [ ] AC12. Review API/UI distinguishes active recovery from terminal manual
      review and exposes no raw model trace or unrestricted evidence content.
- [ ] AC13. `legacy` and `resilient_v1` can run concurrently for different
      projects, and rollback requires only switching new root runs to `legacy`;
      an existing series and its continuations retain the original snapshot.
- [ ] AC14. Focused remediation tests, race tests, backend-wide tests, frontend
      tests/type checks, `go vet`, builds, `git diff --check`, Trellis check, and
      GitNexus change detection pass.

## Out Of Scope

- Giving the model connector credentials, arbitrary shell/network access, raw
  provider URLs, or direct state-transition authority.
- Persisting complete raw model conversations or provider-native opaque session
  state.
- Replacing existing adapter-local transport retries or connector contracts.
- Removing the human merge/review gate after a validated publication handoff.
- Correlating evidence across remediation series, lifecycle generations, or
  deployed commits.

## Notes

- Parent: `08-14-agentic-remediation-harness`.
- Coordinates existing work in `08-21-remediation-tool-driven-context`,
  `08-24-remediation-evidence-thresholds`,
  `08-25-remediation-reasoning-turns`,
  `08-28-model-tool-corrective-retry`, and archived continuation work. Where
  those tasks conflict with this terminal/context contract, this task owns the
  corrected behavior.
