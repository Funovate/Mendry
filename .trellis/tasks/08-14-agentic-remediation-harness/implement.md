# Implementation Plan: Agentic Remediation Harness

## Parent Rule

This task is an umbrella. Do not run a broad implementation directly against
it. Create and finish the child tasks below, then use this parent only for the
cross-child integration review and controlled pilot decision.

## Delivered Baseline

- [x] Walking skeleton: durable trigger, diagnosis, plans, suggested diff, real
      Git/evidence/model adapters, review API/UI, and real-incident checkpoint.
- [x] Tool-driven diagnosis foundation: ordered conversation, model-visible
      tools and observations, SSH inspect, dynamic MCP, corrective retry, and
      bounded context.
- [x] Evidence and resilience: evidence re-read, fact/challenge gate,
      checkpoints, recovery protocol, exhaustion proof, phase budgets, and
      restart reconstruction.
- [x] Full lifecycle kernel: `ApplyPlan`, `ResumeLifecycle`, plan policy,
      workspace/validation/publication ports, durable lifecycle-effect store,
      validation revision, publication retry, and `awaiting_human_review` tests.

## Step 1: Clean Up Existing Child Boundaries

- [ ] Audit `08-21-remediation-tool-driven-context` against delivered code.
      Archive the stale original as superseded after carving any genuine generic
      HTTP JSON log gap into an independent low-priority task.
- [ ] Rewrite and reprioritize `08-14-stdio-mcp-harness-config` against the
      current dynamic MCP runtime. Keep it independent from production repair.
- [ ] Confirm neither task blocks the remaining ApplyPlan path.

## Step 2: Create Focused Remaining Children

- [ ] Create **execution policy configuration** child with project-owned,
      versioned approved validation commands, risk policy, artifact retention,
      publication target/namespace, and scoped write credential metadata.
- [ ] Create **lifecycle runtime adapters** child with production workspace,
      isolated validator, content-addressed artifact store, Git/SCM publisher,
      PostgreSQL lifecycle-store injection, and restart resumer.
- [ ] Create **apply-plan API and UI** child with authorized durable admission,
      HTTP mapping, lifecycle review projection, plan selection, confirmation,
      progress/recovery, and publication handoff.
- [ ] Create **controlled production pilot** child that depends on the first
      three and owns the final execution go/no-go.
- [ ] Add the children to this parent and verify their requirements do not
      overlap.

## Step 3: Execution Policy Configuration Child

- [ ] Persist immutable validation command versions using argv, cwd policy,
      timeout, output/resource/network bounds, and no shell text.
- [ ] Persist ordinary/high-risk/denied change policy, required command IDs,
      diff/file/iteration bounds, artifact policy, publication target/prefix,
      and scoped publication credential reference.
- [ ] Add admin-only API/UI with optimistic updates, same-project credential
      checks, secret-free responses, and configuration readiness projection.
- [ ] Implement production `PlanPolicyEvaluator` and a loader that snapshots all
      required command/policy versions at plan admission.
- [ ] Test stale versions, incomplete policy, cross-project/wrong-kind secrets,
      denied control-plane changes, high-risk opt-in, and immutable run
      snapshots.

## Step 4: Lifecycle Runtime Adapters Child

- [ ] Implement idempotent disposable `WorkspacePort` at the exact deployed
      commit with tree-hash CAS, bounded patching, path policy, and cleanup/TTL.
- [ ] Implement the isolated `ValidationPort` with approved argv only, no shell,
      deny-by-default network, no sensitive credentials, resource/output bounds,
      and no host/container-engine authority.
- [ ] Implement project/run-scoped content-addressed patch and validation
      artifacts with hashes, retention, and bounded authorized reads.
- [ ] Implement `PublicationPort` with a fresh exact-baseline checkout,
      artifact/hash/tree verification, restricted branch namespace, idempotent
      commit/push, and draft/compare metadata when supported.
- [ ] Inject `RunStore` as `LifecycleStore` plus all runtime ports/policies into
      bootstrap. Add bounded startup resume for active lifecycle phases.
- [ ] Run adapter contract, isolation, idempotency, restart, cleanup, artifact
      mismatch, remote identity, and protected-branch tests.

## Step 5: Apply-Plan API And UI Child

- [ ] Add an application use case requiring project write permission,
      generation, run ID, expected version, and plan ID.
- [ ] Validate latest series/run/baseline/mode/state/plan/policy before durable
      admission and before any external effect.
- [ ] Make duplicate admission idempotent and conflicting/stale admission fail
      before adapter execution.
- [ ] Add the protected `/remediation/apply` endpoint with stable response/error
      contracts and request-size/unknown-field handling.
- [ ] Ensure admission survives HTTP cancellation and active lifecycle work can
      resume after process restart or an authorized repeated request.
- [ ] Extend review API types with selected plan, lifecycle effects, validation,
      publication metadata, and final next action without exposing raw output or
      secrets.
- [ ] Add frontend API schemas and plan selection, apply confirmation,
      authorization, pending/recovery/failure, validation, publication, and
      human-review states.
- [ ] Add backend, frontend unit, and Playwright coverage for operator/admin,
      viewer denial, stale state, duplicate action, active recovery, terminal
      handoff, responsive layout, and no horizontal overflow.

## Step 6: Controlled Pilot Child

- [ ] Configure a designated non-production project with approved commands,
      isolated runtime, artifact store, and scoped publication credential.
- [ ] Run a real code-fixable incident from `diagnosis_ready_for_review` through
      one validated branch/commit and `awaiting_human_review`.
- [ ] Verify no merge, deployment, production mutation, or recovery claim
      occurs.
- [ ] Exercise at least validation failure/revision, publication retry/restart,
      stale duplicate admission, and policy-denied change scenarios.
- [ ] Produce a written security and quality read-out with a go/no-go for
      per-project execution enablement.

## Step 7: Parent Integration Gate

- [ ] Verify every parent PRD acceptance criterion against child artifacts,
      commits, tests, and the controlled-pilot read-out.
- [ ] Run complete backend unit/race/vet/build and PostgreSQL integration checks.
- [ ] Run frontend lint/typecheck/unit/build/Playwright checks.
- [ ] Run isolation and network-denial tests in the actual deployment runtime,
      not only with in-process fakes.
- [ ] Run GitNexus change detection and inspect every affected execution flow.
- [ ] Confirm specs describe the final API, policy snapshots, adapter security,
      artifact boundary, rollout, and rollback.
- [ ] Archive this parent only after all remaining children and the final pilot
      are complete.

## Validation Commands

Each child owns focused commands. The final parent gate includes:

```bash
cd backend && go test ./...
cd backend && go test -race ./internal/modules/remediation/...
cd backend && go vet ./...
cd backend && go build ./cmd/...
cd frontend && npm run lint
cd frontend && npm run typecheck
cd frontend && npm test -- --run
cd frontend && npm run build
cd frontend && npm run test:e2e
git diff --check
```

PostgreSQL tests must use an explicitly isolated test database. Publication tests
must use local fakes or a designated non-production repository and must never
push to an arbitrary user or production branch.

## Review Gates

1. Parent and child ownership review before creating implementation tasks.
2. Execution policy and threat-model review before enabling runtime adapters.
3. Isolation/artifact/publisher contract review before exposing ApplyPlan.
4. API/UI authorization and idempotency review before the controlled pilot.
5. Pilot read-out and explicit per-project enablement decision before parent
   archive.

## Rollback

- Disable plan admission globally or for the affected project.
- Stop new lifecycle execution while preserving runs, effects, artifacts, and
  already-pushed branches for audit.
- Resume or hand off active states explicitly; do not rewrite them as successful
  or recovered.
- Never delete a remote branch, merge, deploy, or execute rollback automatically.
