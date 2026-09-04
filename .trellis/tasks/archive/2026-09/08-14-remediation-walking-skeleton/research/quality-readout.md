# Real-Incident Diagnosis Quality Read-Out

Date: 2026-09-04

## Decision

The walking skeleton passes its diagnosis and advisory-planning checkpoint and
can be closed. It is a **conditional go** for the next controlled integration
slice, not approval for unattended repair or broad production rollout.

- **Go:** retain the real-source -> repository -> model -> evidence-backed
  diagnosis -> plan -> suggested-diff architecture and continue integrating it
  behind human review.
- **No-go:** do not enable production patching, validation, publication, or
  wider rollout based on this checkpoint alone. The production `ApplyPlan`
  entry point and its workspace/validation/publication dependencies are not
  wired through HTTP/UI/bootstrap, and the pilot sample does not cover enough
  non-code fixability classes.

## Sample And Method

The read-out uses four real pilot incidents and five known attempts. The
original proposal of approximately ten incidents was never locked. Four was
accepted for this checkpoint because it is the complete reviewable pilot set:

| Incident | Source | Outcome at observation time | Quality result |
|---|---|---|---|
| INC-2267 | Historical persisted run/session audit | `blocked_manual_review` / `insufficient_evidence` | Root-cause direction was correct (intentional nil-pointer handler), but host/time/request closure was over-required and Docker evidence was not durably citable. Partial. |
| INC-2268 | Historical run/session audit | `insufficient_evidence` after Docker failure and fallback | The agent changed tools, but the protocol lacked explicit model-driven corrective retry and did not close the diagnosis. Fail. |
| INC-2270 | Sanitized replay plus historical two-attempt audit | Model reached `code_fixable`; persisted result was downgraded/manual | Root-cause direction was correct. A citation-classification mismatch was incorrectly made material, and continuation omitted provider detail. Model pass; harness result fail. |
| INC-2390 | Live PostgreSQL run, queried 2026-09-04 | `diagnosis_ready_for_review`, `code_fixable`, confidence `0.96` | Evidence gate, diagnosis, plans, and suggested diff all completed. Pass. |

INC-2267, INC-2268, and INC-2270 were intentionally deleted from the pilot
database during later cleanup, so their assessment uses preserved Trellis
session records and the sanitized INC-2270 replay. INC-2390 remains in the
database and was checked directly using structured rows only. No credentials,
raw prompts, raw model responses, or unrestricted logs were read into this
report.

## Results

### Diagnosis Validity

- Three of four incidents had a correct root-cause direction: INC-2267,
  INC-2270, and INC-2390 all localized the deliberate nil-pointer path. This
  meets the proposed majority bar.
- Only INC-2390 produced the complete desired business result without a
  historical harness defect. The earlier cases exposed real orchestration
  problems rather than model inability.
- Those defects now have focused regression coverage in the corrective-retry,
  evidence-gate, continuation, and INC-2270 resilience suites. This report does
  not count those scripted tests as additional real incidents.

### Evidence Citation Quality

- INC-2390 cites three persisted evidence IDs. All 3/3 resolved in PostgreSQL;
  two are direct-fault provider/runtime evidence and one is contextual alert
  evidence.
- Its reasoning explicitly distinguishes material evidence from non-blocking
  gaps and records two contradictions rather than hiding them.
- No fabricated evidence ID was observed in the reviewable historical records.
  Exact historical citation-integrity totals cannot be recomputed because those
  database rows were deleted. INC-2270's defect was a classification mismatch,
  not an invented ID.

### Suggested-Diff Usefulness

INC-2390 produced two evidence-linked ordinary-risk plans and recommended
removing the production fault-injection handler and route. The 1,379-byte
unified diff is readable, limited to the two diagnosed files, and directly
removes the panic source. A reviewer could apply it with minor edits or choose
the documented compatibility alternative. It still requires repository-side
application, compilation, tests, and an explicit decision about retaining a
test-only endpoint; none of those effects belong to this walking skeleton.

### Operational Shape

The live run completed in 186 seconds using five model calls and seven tool
calls (`evidence.tencent_cls_detail`, repository reads/searches, and
`docker.logs`). It read 20,420 evidence bytes and 4,993 repository bytes, ended
at `diagnosis_ready_for_review`, persisted one decision, two plans, and one
suggested-diff artifact, and made no workspace or Git mutation.

## Remaining Conditions

The following are follow-on gates, not reasons to keep this walking-skeleton
task open:

1. Wire the existing complete lifecycle (`ApplyPlan`) through protected API/UI
   and production workspace, validation, publication, and lifecycle stores.
2. Run a fresh pilot set spanning at least one non-code class and multiple fault
   shapes before changing rollout defaults.
3. Keep human review mandatory after publication; this checkpoint does not
   authorize automatic merge or deployment.
4. Treat the historical INC-2267/2268/2270 failures as regression scenarios,
   while reporting them honestly rather than recasting them as successful live
   runs.

## Acceptance Conclusion

The real adapter chain has been exercised against real incidents; the retained
live run has schema-valid, resolvable citations and a useful suggested diff; and
the longitudinal sample identified defects that were converted into regression
coverage. This satisfies the Step 9 learning checkpoint. The sample limitation
and production-execution no-go remain explicit follow-on constraints.
