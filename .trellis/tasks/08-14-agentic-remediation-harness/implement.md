# Implementation Plan: Agentic Remediation Harness

## Dependency Gate

- [ ] Confirm the service-foundation worker, PostgreSQL outbox, RabbitMQ job
      envelope, retry/dead-letter policy, authorization, and encrypted-secret
      contracts are implemented or assigned to prerequisite child tasks.
- [ ] Confirm evidence/redaction, LLM-provider, remediation-package, SCM-change,
      and notification contracts have stable owning tasks and no duplicated
      responsibility remains in this task.
- [ ] Confirm the converged PRD has no open product questions and parent/sibling
      task contracts remain consistent before starting implementation.

## Ordered Checklist

- [ ] Define the typed remediation-series/run aggregates, linked attempt model,
      state transitions, fixability classes, lifecycle generation,
      evidence-context version,
      candidate/selected plan schemas, tool envelopes, budgets, artifacts, and
      idempotency keys with exhaustive domain tests.
- [ ] Add additive PostgreSQL migrations, sqlc queries, repository adapters, and
      transition/outbox transactions for runs, decisions, plans, invocations,
      artifacts, and external effects.
- [ ] Add project remediation policy and metadata-only API configuration for
      enablement, the default-`P2` minimum automatic priority, allowed
      paths/change classes, validation command IDs, branch/commit fallbacks,
      sandbox/network limits, budgets, and notification selection. The run
      baseline remains the exact configured deployed commit rather than a
      selectable latest-HEAD policy.
- [ ] Implement setup-time validation-command discovery with source evidence,
      administrator review, immutable command versions, and run-time snapshot
      references. Discovery must never execute a candidate command.
- [ ] Implement the coordinator and versioned RabbitMQ handlers using local
      fakes first; prove trigger/coalescing, context-version advancement,
      restart, redelivery, cancellation, retry/dead-letter, and
      configuration-snapshot behavior before real external adapters.
- [ ] Implement the context assembler over evidence and repository read ports,
      including incremental retrieval, redaction, size limits, repository
      guidance discovery, and prompt-injection isolation.
- [ ] Implement the provider-neutral agent protocol, strict structured-output
      validation, phase-specific tool registry, compact turn context, usage
      accounting, and bounded stop conditions against a scripted fake model.
- [ ] Implement the policy gateway for phase, capability, path, ref, command,
      network, size, time, budget, and idempotency validation with deny-first
      tests for every mutation path. Include ordinary, explicitly opted-in
      high-risk, and always-denied publication classifications.
- [ ] Implement ephemeral repository workspace creation and read-only Git/code
      tools from the exact deployed commit without exposing credentials to the
      model or sandbox.
- [ ] Implement the dedicated rootless-OCI `SandboxRunner` boundary with stable
      per-attempt IDs, explicit command IDs, read-only base images, resource
      limits, output redaction/truncation, blocked engine socket/host/production
      access, argv execution without a shell, terminal destroy, and TTL cleanup.
- [ ] Implement the content-addressed artifact boundary for bounded patches,
      expected Git tree hashes, validation artifacts, authorization, retention,
      and publisher-side replay verification without persisting a workspace or
      repository archive.
- [ ] Implement branch/commit convention inference, confidence thresholds,
      the `hotfix/INC-<incident-number>-<short-problem-slug>` branch fallback,
      sanitization, stable run-ID collision handling, and evidence reporting
      using remote-ref and SCM fake fixtures.
- [ ] Integrate candidate-plan filtering/selection and the bounded patch,
      validation, feedback, and reclassification loop.
- [ ] Integrate the remediation-package and SCM publisher ports for idempotent
      provider-neutral branch creation, commit, and push plus Yunxiao draft-PR
      creation and other-provider branch/compare results; keep merge and
      deployment absent from all interfaces.
- [ ] Integrate durable review/non-code/blocked/failure notifications and expose
      metadata-only run, diagnosis, plan, artifact, validation, and publication
      state through protected REST APIs and the console. Publish the stable
      remediation events through the signed-webhook outbox contract without
      adding native email or vendor-specific chat adapters.
- [ ] Add end-to-end fake-adapter scenarios for successful repair, non-code
      cause, additional evidence, ambiguous plan, unsafe change, failed tests,
      opted-in high-risk change, denied control-plane change, retry,
      cancellation, provider outage, SCM race, and worker restart.
- [ ] Run a full security review for credential leakage, prompt injection,
      sandbox escape, arbitrary command/network execution, ref injection,
      symlink/submodule/hook behavior, artifact retention, and audit contents.

## Validation

- `cd backend && go test ./...`
- `cd backend && go vet ./...`
- `cd frontend && npm test -- --run`
- `cd frontend && npm run lint`
- `cd frontend && npm run build`
- Run focused PostgreSQL/RabbitMQ integration suites with isolated test
  resources once those prerequisites exist.
- Run sandbox escape and network-denial tests in the approved deployment
  runtime, including engine-socket denial and terminal/TTL cleanup, rather than
  relying only on in-process fakes.
- Run SCM contract tests against local HTTP/Git fakes; no test may push to a
  user or production repository.

## Review Gates

- Domain/state-machine review before persistence or transports.
- Threat-model and tool-policy review before enabling a real model.
- Sandbox isolation review before running repository commands.
- Idempotency/redelivery review before enabling push or notifications.
- Final cross-task contract review with evidence, provider, remediation, SCM,
  and notification owners before project opt-in is exposed.

## Rollback

- Disable global or project remediation enablement and stop the harness
  consumer; incident ingestion and deterministic evidence/reporting continue.
- Preserve durable runs, plans, artifacts, audit records, and already-pushed
  branches. Do not attempt automated remote branch deletion during rollback.
- Keep additive schema data readable by the prior application version where
  possible; review database rollback separately from binary rollback.
