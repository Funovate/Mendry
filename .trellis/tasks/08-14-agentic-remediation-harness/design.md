# Technical Design: Agentic Remediation Harness

## Design Principle

The harness, not the model, owns authority. The model reasons over bounded,
redacted observations and requests typed actions. Deterministic application
code validates policy, executes tools, records effects, and decides whether the
state machine may continue. Credentials are injected only inside trusted
adapters and never become model context.

## Component Boundaries

```text
Incident transition
      |
      v
Transactional outbox -> RabbitMQ -> RemediationRunCoordinator
                                        |
                    +-------------------+--------------------+
                    |                   |                    |
                    v                   v                    v
             ContextAssembler      AgentEngine          PolicyGateway
              /      |      \           |                    |
             v       v       v          v          +---------+---------+
        Evidence  Repository  Rules  LLMProvider   |         |         |
        Broker      Reader             Port        v         v         v
                                                Logs     Workspace    SCM
                                                 |       Sandbox    Publisher
                                                 +---------+---------+
                                                           |
                                                           v
                                                  RemediationPackage
                                                           |
                                                           v
                                                       Notifier
```

`RemediationRunCoordinator` is the only component allowed to advance run
state. Tool adapters do not call the model or each other. `AgentEngine` does
not receive adapter clients; it receives tool schemas and returns structured
tool requests or phase results. `PolicyGateway` is the sole execution entry
point for those requests.

## Trigger And Coalescing

Automatic remediation is a per-project policy with a minimum priority whose
MVP default is `P2`. A new or reopened `P1/P2` incident creates a remediation
series; a project `admin` or `operator` may manually create one for `Info`. The
durable series key is `(incident_id, lifecycle_generation, deployed_commit)`.
Repeated observations for the same fingerprint update the owning incident and
evidence chain but cannot create another automatic series for that key.

An explicit lifecycle generation increments when a recovered incident reopens.
A changed deployed commit creates a new series because its source baseline is
different. A series contains monotonically numbered run attempts. Manual retry
creates a linked next attempt rather than mutating a terminal run or creating a
second automatic root. Evidence collected after run creation advances an
append-only context version, and each diagnosis/plan/model result records
exactly which context version and evidence IDs it used.

## Durable State Machine

```text
queued
  -> preparing_context
  -> diagnosing
       -> collecting_more_context -> diagnosing
       -> completed_non_code
       -> blocked_manual_review
       -> planning
  -> planning
       -> blocked_manual_review
       -> patching
  -> patching
       -> validating
  -> validating
       -> patching              (bounded revision)
       -> blocked_manual_review
       -> publishing
  -> publishing
       -> awaiting_human_review

Any active phase -> cancelled | failed | budget_exhausted
```

Each transition uses optimistic versioning and writes any next job intent to
the same PostgreSQL transaction. A phase stores its validated result before
acknowledging the message. External effects use stable idempotency keys derived
from the run and effect kind.

## Core Records

`RemediationSeries` stores the incident, project, lifecycle generation, exact
deployed commit, automatic-trigger uniqueness key, and current attempt number.

`RemediationRun` stores identity, series and replaced-run references, attempt
number, state, source-baseline snapshot, current evidence-context version,
configuration/policy/template/provider versions, budget counters, terminal
reason, and timestamps.

`DiagnosisDecision` stores the fixability class, confidence, evidence links,
reasoning summary, contradictions, missing evidence, and verification steps.

`RepairPlanCandidate` stores intended behavior, evidence links, affected paths,
change steps, compatibility impact, validation plan, risk, and rollback.
`SelectedRepairPlan` adds the selection rationale and policy-validation result.

`ToolInvocation` stores the requested tool and normalized arguments, phase,
policy decision, timing, bounded/redacted result reference, and usage counters.
Secret-bearing adapter parameters are never part of this record.

`RemediationArtifact` references a diff, validation result, branch, commit, or
draft-PR artifact by content hash and retention policy. Large or sensitive
content remains in the approved artifact store rather than RabbitMQ or audit
JSON.

## Agent Protocol

Each model turn receives a fixed system contract, current phase, remaining
budget, a compact run summary, selected evidence/repository excerpts, and the
typed tools allowed in that phase. It returns exactly one schema-validated
envelope:

```text
request_tool | diagnosis | plan_candidates | patch_complete |
validation_assessment | stop
```

The harness rejects unknown fields, unknown evidence IDs, invalid paths,
unavailable tools, and requests not allowed in the current phase. Model output
never directly mutates state. The coordinator translates only validated output
into a state transition or policy-gateway request.

Tool results are summarized and redacted before the next turn. Raw model
requests and responses are not persisted; the approved structured conclusion,
template version/hash, evidence IDs, provider/model metadata, usage, and result
hash are persisted through the LLM-provider audit contract.

## Tool Model

Initial read tools:

- `repository.list_tree`, `repository.search`, `repository.read_file`,
  `repository.history`, and `repository.diff`
- `evidence.get`, `logs.search`, and `logs.context`
- `workspace.status` and `workspace.read_file`

Initial mutation/execution tools:

- `workspace.apply_patch`
- `workspace.run_validation`, limited to configured or policy-approved command
  IDs rather than arbitrary shell strings

The model may request a logical action such as `run_validation("unit")`; the
trusted runner resolves that ID to an immutable, administrator-approved command
definition snapshotted by the run. The model cannot provide a command string.
Git fetch, credential injection, branch creation, commit, push, and draft-PR
operations are coordinator/adaptor effects after the validation gate, not model
tools or arbitrary shell commands.

## Repository Workspace And Sandbox Runner

The workspace manager creates an ephemeral per-run checkout from the selected
immutable deployed commit recorded by project configuration. The configured
production branch identifies the intended PR target but its latest remote HEAD
does not replace the run baseline. Repository access credentials exist only
during trusted fetch/push adapter calls. The code-edit/test sandbox receives
the checked-out tree but no Git push identity, connector secrets, model keys,
notification keys, host mounts, or production network route.

The Go worker talks to a narrow `SandboxRunner` port backed in the self-hosted
deployment by a dedicated rootless OCI runner. The general worker does not mount
`/var/run/docker.sock` or receive general container-engine authority. Each run
attempt maps to a stable sandbox ID, starts from a project-configured read-only
toolchain image (see Target Language And Toolchain Neutrality), runs as a
non-root user with dropped capabilities and no-new-privileges, and receives
explicit CPU, memory, process, filesystem, output, and time quotas. Network is
denied unless an administrator-approved command version carries a bounded
destination policy.

Repository guidance such as `AGENTS.md`, `CONTRIBUTING`, formatter configs, and
test manifests informs style and commands but is treated as untrusted project
data. It cannot widen capabilities or override system policy.

Symlinks, submodules, Git hooks, package lifecycle scripts, binary files, large
files, and networked validation commands require explicit policy. The runner
enforces CPU, memory, process, filesystem, output, and time limits and destroys
the workspace after artifacts are captured.

Terminal transitions explicitly destroy the sandbox. A TTL sweeper destroys
orphaned sandboxes after worker loss. Reuse is allowed only for the same run
attempt; no sandbox state crosses projects or attempts.

## Target Language And Toolchain Neutrality

The monitored repository may be Go, Java, C#, Python, Node, or another stack;
the harness backend's own language is irrelevant to it. Neutrality holds by
construction: repository read/search, patch, and diff operate on files and text,
diagnosis and patching are model-driven, and validation is an
administrator-approved command ID rather than a hardcoded toolchain, so
`go test`, `mvn test`, `dotnet test`, and `pytest` are interchangeable at the
runner boundary. The model requests `run_validation("unit")` and the runner
resolves the ecosystem-appropriate argv from the approved command version.

Language specificity is confined to three configured surfaces, all owned by
project setup rather than harness code:

- **Sandbox toolchain image.** There is no single base image. Each project
  selects the read-only sandbox image carrying its ecosystem toolchain (for
  example JDK plus Maven/Gradle, the .NET SDK plus NuGet, or a pinned Python
  interpreter plus its build backend and, where native extensions compile, the
  required compiler and headers). The image stays read-only, rootless,
  capability-dropped, and network-denied by default; only its contents differ
  per project.
- **Dependency acquisition.** Because network is denied by default, dependencies
  enter through an explicit per-ecosystem policy: a warmed read-only dependency
  cache mounted into the sandbox (`~/.m2`, `~/.nuget`, a pip/wheel cache) and/or
  an administrator-approved bounded egress to a single declared package registry
  or mirror attached to the validation command version. Python native-extension
  builds require the compiler and headers to be present in the image rather than
  fetched at run time. If required dependencies cannot be provided under policy,
  validation stops and the run reports a configuration limitation instead of
  opening general network access.
- **Ecosystem file classification.** The risk-policy path matcher carries a
  per-ecosystem table of manifest/lockfile, dependency, and control-plane paths
  (see Change Risk Policy).

## Artifact Boundary

After required validation succeeds, the harness writes a bounded unified patch,
expected resulting Git tree hash, redacted validation artifacts, and metadata
to a content-addressed `ArtifactStore`. PostgreSQL stores references, hashes,
ownership, and retention; the initial self-hosted store uses a dedicated
application-owned persistent volume. It never stores a full repository archive
or mutable workspace. Authorized APIs stream only approved artifacts.

The sandbox is destroyed after artifact capture. The trusted change publisher
checks out the exact deployed commit separately, loads the patch by reference,
applies it without model involvement, and verifies the expected tree hash. This
keeps Git write credentials outside both the model and the code-execution
sandbox.

## Validation Command Discovery And Approval

A setup-time discovery service reads bounded repository metadata such as
language manifests, workspace files, Make targets, and CI definitions. It emits
command candidates with source evidence and risk metadata but cannot execute
them. A project administrator accepts, edits, or rejects each candidate. An
accepted definition receives a stable command ID and immutable version.

A remediation run snapshots the approved command IDs and versions required by
its policy. `workspace.run_validation` accepts only one of those IDs; the runner
resolves the stored argv and execution policy without a shell. Later command
edits apply only to new runs. If no required command has been approved, the run
stops before patch publication and reports a configuration limitation.

## Change Risk Policy

The policy gateway classifies the normalized diff before validation and again
before publication. Ordinary application source and test changes may continue
after required checks pass. Database migrations/schema,
authentication/authorization, and dependency manifest/lockfile changes are
high risk: they require project opt-in, specialized validation, a visible
high-risk marker, and the normal human merge gate.

CI/CD workflow definitions, deployment manifests, infrastructure-as-code,
credential or secret policy, Git hooks/submodules, and binary artifacts are
denied mutation/publication surfaces in the MVP. Model confidence and passing
generic tests cannot override this classification. A denied plan transitions
to manual review with its evidence and proposed steps but without an automated
commit or push.

Path classification is ecosystem-aware. The dependency-manifest/lockfile class
matches each configured ecosystem's files (`pom.xml`, `build.gradle`,
`*.csproj`, `packages.lock.json`, `requirements.txt`, `poetry.lock`/`uv.lock`,
`go.mod`/`go.sum`, `package.json` plus its lockfiles); the denied control-plane
class matches CI/CD, deployment, and IaC paths regardless of language. The
matcher table is a versioned project-policy input, so adding a new target
ecosystem updates the table without changing gateway code.

## Branch And Commit Convention Inference

The convention detector consumes remote refs plus recent merged-change
metadata when the SCM provider supports it. It normalizes names into features:
prefix, separators, ticket/incident token, case, slug form, and length. A
deterministic scorer selects a template only above a configured confidence and
sample threshold; otherwise the project fallback template applies.

The selected template is rendered with bounded values such as incident number,
short run ID, and sanitized diagnosis slug. The policy gateway validates the
complete ref, write namespace, collision behavior, and protected-branch rule.
Commit-message convention uses the same evidence-first approach and configured
fallback.

The branch fallback is
`hotfix/INC-<incident-number>-<short-problem-slug>`. The slug uses a bounded,
Git-ref-safe representation of the selected diagnosis. If the rendered ref
already exists for a different remediation, the detector appends the stable
short run ID; RabbitMQ redelivery for the same run resolves to the same ref.

Convention inference does not choose the source baseline. That is a separate
invariant: branch creation, code inspection, patching, and validation use the
exact deployed commit captured by the run.

## Plan Selection And Repair Loop

The agent can propose multiple plans but must identify one recommendation.
Before selection, the harness filters candidates by required capabilities,
path/change policy, supported validation, project high-risk opt-in, and maximum
risk. The agent's recommendation is accepted only from the remaining
policy-compliant set and is persisted with its evidence-based rationale.

The selected plan is applied in small patch operations. Validation failures
return sanitized diagnostics to the agent for a bounded number of revisions.
The loop stops on success, unchanged repeated failure, policy rejection,
budget exhaustion, cancellation, or a newly discovered non-code/unsafe cause.

## Publication And Human Gate

Before publication, the coordinator verifies workspace cleanliness outside the
approved diff, required validation freshness, source ancestry, remote identity,
branch protection, and idempotency state. A trusted SCM adapter then creates or
reuses the change branch, commits with the recorded message, pushes, and creates
a draft PR targeting the configured production branch when supported. If the
remote target branch has advanced since the deployed commit, the harness keeps
the deployed-commit baseline and exposes conflicts for human resolution rather
than rebasing onto code that was not part of the incident diagnosis.

The adapter has no merge operation in its interface. The terminal successful
state is `awaiting_human_review`, accompanied by the next action: review the
native Yunxiao draft PR or create a change request from another provider's
pushed branch, then merge manually. A later verification workflow may observe
merge/deployment/recovery, but it cannot be asserted by this harness.

Provider-neutral Git adapters perform HTTPS/SSH checkout and restricted branch
push for configured GitHub, GitLab, Yunxiao, Gitee, and generic remotes. The
MVP Yunxiao adapter additionally creates or reuses a native draft PR. Other
providers return the pushed ref plus a compare URL when one can be constructed;
the user creates the change request manually. Adding native provider adapters
later does not change coordinator states or the publication result contract.

## Notification Delivery

Every terminal or review-gate transition writes its notification record and
signed-webhook outbox intent in the same business transaction. Stable event
kinds are `non_code_diagnosed`, `manual_review_required`, `repair_failed`, and
`change_ready_for_review`. The console reads the durable record; webhook delivery
may retry or exhaust independently without hiding the outcome from users.

Webhook payloads contain identifiers, summaries, evidence/artifact links, and
review URLs rather than credentials, raw logs, raw model payloads, or full
repository diffs. Native email and vendor-specific chat adapters remain outside
the MVP and can consume the same stable event contract later.

## Failure And Recovery

Transient provider, connector, SCM, and notification failures use one bounded
attempt budget shared with RabbitMQ delivery so nested retries cannot multiply
unboundedly. Invalid model output and policy rejection are not retried as
transport failures. A dead-lettered run remains inspectable and resumable only
through an explicit authorized action.

The provider boundary cannot guarantee exactly-once billing or inference when
a worker loses an uncommitted response. Each attempt records a stable request
hash and started/completed effect state; an uncertain call may repeat only
within the shared budget. Repetition cannot create a second durable decision or
publication effect, and the uncertainty remains visible in audit metadata.

Cancellation stops future model/tool calls and publication. If a branch or
commit was already pushed, it is retained and reported rather than deleted.
Disabling the feature prevents new runs and stops active runs at the next phase
boundary without altering existing audit/remediation records.

## Compatibility And Delivery Order

The task cannot execute end to end until the service-foundation worker/outbox,
evidence contracts, provider port, and remediation/SCM ports exist. Contracts
and local fakes can be implemented first. Database additions should be additive
and gated behind per-project enablement, allowing the harness worker consumer
to be disabled without affecting incident ingestion or deterministic reports.
