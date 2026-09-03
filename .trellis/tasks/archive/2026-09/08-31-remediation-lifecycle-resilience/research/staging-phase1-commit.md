# Staging recommendation: commit ONLY Phase 1 Durable Kernel (08-31)

Reference: implement.md Phase 1 (slices 1-4, 4.5, 5a, all marked `[x]`), diff vs
HEAD `7674d39` (2026-08-31).

## Critical context found

1. **HEAD is not a Go module.** No `go.mod`/`go.sum`/`cmd/`/`bootstrap`(except
   api.go) exist at HEAD. The working tree contains a large uncommitted infra
   refactor (module scaffold, migrate runner, platform/postgres, auth, redis,
   telemetry, cmd/*, dev/*, tools/*, Makefile, sqlc.yaml, tests/integration).
   A Phase 1-only commit therefore cannot build on a clean checkout by itself;
   the module scaffold is an unavoidable compile dependency (see §Compile deps).
2. **Migration runner refactor is in-flight and uncommitted.** At HEAD the
   `migrate` package has only `source_test.go` referencing an undefined
   `embeddedMigrations`; the runner (`source.go`, `runner.go`, `migratedb/`,
   plus migrations `000001`/`000002`/`000004`) is untracked working-tree work
   from the foundation tasks, NOT Phase 1. Phase 1's `000016` rides on it via
   `//go:embed migrations/*.up.sql`.
3. **In-flight sibling-task work is mixed into the same files.** Working tree
   contains uncommitted work from: 08-28 model-tool-corrective-retry
   (recoveryAction/`toolFailureAttempts`), 08-24 evidence-thresholds / SSH-Docker
   follow-ups (causal-closure reassessment, docker refinement challenge
   `PendingDockerLogRefinement`/`CoverageLimited`, SSH inspect expansion +
   canonical runtime-evidence persistence `runtime_evidence.go`), webhook AI,
   Tencent CLS detail, MCP dynamic runtime, logging fanout, httpserver refactor,
   and frontend. None of it is Phase 1 unless noted.
4. `agent_engine.go` (corrective-retry/test-policy/docker-coverage prompt),
   `context_assembler.go`, `evidence_gate.go`, `domain/docker.go`,
   `docker_tool.go`, `ssh_inspect.go`, `sshlog/container_runtime.go`,
   `httpserver/*`, `platform/config/config.go`, `observability/logging.go`,
   `platform/postgres/querydebug.go`, frontend, `.env.example`, `README.md`,
   `.gitignore` = non-Phase 1.

## (A) Whole files safe to stage

### New (untracked) — Phase 1
```
backend/internal/commands/migrate/migrations/000016_working_memory_checkpoint.up.sql
backend/internal/modules/remediation/domain/checkpoint.go            (+ checkpoint_test.go)
backend/internal/modules/remediation/domain/evidence_read.go         (+ evidence_read_test.go)
backend/internal/modules/remediation/domain/recovery.go              (+ recovery_test.go)
backend/internal/modules/remediation/domain/budget_plan.go           (+ budget_plan_test.go)
backend/internal/modules/remediation/adapter/postgres/checkpoint_store.go   (+ checkpoint_store_test.go)
backend/internal/modules/remediation/adapter/postgres/evidence_read.go      (+ evidence_read_test.go, evidence_read_cursor_test.go)
backend/internal/modules/remediation/application/budget_plan.go      (+ budget_plan_test.go)
backend/internal/modules/remediation/application/recovery.go         (+ recovery_test.go)
backend/internal/modules/remediation/application/resilience.go       (+ resilience_test.go, resilience_budget_test.go)
backend/internal/modules/remediation/application/evidence_read_test.go
```
All of these are pure Phase 1 (slice 1/2/3/4/4.5/5a) and import only
`remediationdb` + `domain` + pgx (adapter layer) — no platform/postgres dep.

### Tracked modified — entire diff is Phase 1
```
backend/internal/modules/remediation/adapter/postgres/queries/remediation.sql
    (agent-loop-mode run/next-run inserts, evidence baselines, GetRemediationEvidencePage,
     ListContinuationRuntimeEvidence/ListContinuationEvidenceIndex, checkpoint event/working-memory,
     evidence-read cursor queries)
backend/internal/modules/remediation/adapter/postgres/remediationdb/models.go          (generated, all Phase 1)
backend/internal/modules/remediation/adapter/postgres/remediationdb/remediation.sql.go (generated, all Phase 1)
backend/internal/modules/remediation/adapter/postgres/store.go        (mapCreatedRun/mapRunRow agent-loop-mode,
     ListContinuationRuntimeEvidence, ListContinuationEvidenceIndex, mapEvidenceIndexEntry)
backend/internal/modules/remediation/adapter/postgres/store_test.go   (continuation same-series + index tests, fixtures)
backend/internal/modules/remediation/application/continuation.go      (renderContinuationRuntimeEvidence,
     renderContinuationEvidenceIndex, safeContinuationClassification; NOTE 1-line
     `runtime_evidence_persistence` reason-code hunk is non-Phase 1 support — harmless to include, or split out)
backend/internal/modules/remediation/application/continuation_test.go (3 Phase 1 tests)
backend/internal/modules/remediation/application/trigger.go           (single hunk: preparedContinuation shape)
backend/internal/modules/remediation/domain/types.go                 (Run.AgentLoopMode + AgentLoopPolicyVersion)
backend/internal/modules/remediation/domain/evidence_persistence.go   (ContinuationEvidenceQuery/Store, EvidenceIndexEntry)
backend/internal/modules/remediation/domain/ports_contract_test.go    (adds EvidenceReadPort + ContinuationEvidenceStore)
backend/internal/modules/projects/application/service_test.go         (UpsertRemediationPolicy fake + admin test only)
```

## (B) Mixed files — stage only Phase 1 hunks

| File | Phase 1 hunks to keep | Hunks to exclude |
|---|---|---|
| `bootstrap/api.go` | `SetEvidenceReadPort(remediationStore)` line; the `remediationpostgres.NewCheckpointStore(postgresPool)` block + `SetCheckpointStore(...)` | hook/tencent/mcp/webhook wiring, `containerProbe`, `NewRemediationCoordinatorWithDynamicRuntime` switch, `SetRuntimeEvidenceWriter`/`SetDockerEvidencePort`/`SetTencentCLSDetailPort`/`SetEvidenceResolver`/`SetBootstrapEvidenceLoader` wiring, `NewTriggerWithReporter(...remediationFailureReporter)`, logger/`closeLogSink` |
| `application/coordinator.go` | struct field (L46); `SetCheckpointStore`; `SetEvidenceReadPort` (split from SetRuntimeEvidenceWriter hunk); `Start`/`Continue`/`prepareContinuation` return-shape (preparedContinuation + priorEvidence + reconstruction); `runQueued` signature + resilient-setup + `admitSoftBudget`; `drive` tracker/continuation assembly + forced checkpoint after preparing_context→diagnosing (edit out `causalClosureReassessed`/`challengedDockerRefinementVersion` var lines); routeDiagnosis code-fixable forced checkpoint; `runTool` evidence.read index hook; `transitionBudgeted` `softBudgetRecovery` + `advanceSoftBudget`; `transitionWithReason` terminal checkpoint | `challengePendingDockerRefinement` + its 2 call sites + `causalClosureReassessed` param/block + `diagnosisNeedsCausalClosureReassessment` + the `exhausted, err :=`→`=` change at L758 (depends on the causal-closure declaration) + `collectLoops`-region var decls |
| `application/conversation.go` | ONLY `AppendRecoveryChallenge` (inside the hunk that also contains `AppendCausalClosureReassessment` — needs `add -p` edit) | `toolFailureKey`/`toolFailureAttempts` + `recoveryToolError`; dockerRefinement fields + `PendingDockerLogRefinement`/`AppendDockerLogRefinement` + AppendToolResult tracking; `AppendCausalClosureReassessment`; `runtime_evidence_persistence` in safeRuntimeCode/Message; normalizeConversationValue rework |
| `application/tool_gateway.go` | `ToolEvidenceRead` const; `evidenceReadPort` field + `SetEvidenceReadPort`; advertisement + schema + description + `isRegistered` + cursor validation; `execEvidenceRead`; `case ToolEvidenceRead:` fail-closed in ExecuteTool | `ToolResult.RefinementRequired/RefinementReason`; `runtimeWriter` + `SetRuntimeEvidenceWriter`; `persistRuntimeEvidence`/`applyCanonicalDockerCoverage`/`isRuntimeEvidenceTool`; ExecuteToolObserved persistence block; SSHInspect description change |
| `application/tool_gateway_dynamic.go` | `ToolEvidenceRead` branch in `ExecuteToolWithCatalog` | `persistRuntimeEvidence` block in `ExecuteToolObservedWithCatalog` |
| `application/tool_catalog.go` | evidence.read additions in cloud branch + planning exclusion; evidence.read in SSH branch (keeping the `else` ssh.inspect structure) | moving `ToolSSHInspect` out of the else (docker also gets generic inspect — INC-2267 SSH work) |
| `application/budget.go` | `runBudget.remaining()` (D5 remainingBudget) | `ToolDockerLogs` evidence-bytes accounting |
| `application/fakes_test.go` | `mode` field; `AgentLoopMode` in CreateSeriesAndRun; Version mirror + `fakeHasCounterEffect`; `Get` returns `run.Version` | `fakeRuntimeEvidenceWriter` |
| `application/tool_gateway_test.go` | `ToolEvidenceRead: true` map entry; `len(defs) != 7` | 2 SSH inspect tests |
| `projects/domain/project.go` | `AgentLoopMode`/`RemediationPolicy`/`ValidateRemediationPolicy`; `Configuration.Remediation`; `ConfigurationDraft.Remediation` | SSHDeployment/DockerContainer/SSHSourceConfig/ParseSSHSourceConfig/ValidateSSHContainerProbe; WebhookProvider/SignedWebhookConfig/ParseSignedWebhookConfig; configSchemaVersion/validSSH*/validWebhookFields; validateSourceConfig/validateTriggerConfig refactor |
| `projects/application/service.go` | `Repository.UpsertRemediationPolicy` + `PutConfigurationRemediationPolicy` | Containers/ContainerProbePort/ErrDockerUnavailable/ProbeSSHContainers/Options.Containers; WebhookIngress.Provider |
| `projects/adapter/postgres/queries.sql` | `GetProjectRemediationPolicy` + `UpsertProjectRemediationPolicy` | LookupWebhookToken `webhook_provider`; UpsertSource/UpsertTrigger audit deploymentKind/provider |
| `projects/adapter/postgres/repository.go` | `getRemediationPolicy` + `UpsertRemediationPolicy` + Configuration/Draft.Remediation wiring | `LookupWebhookToken` Provider |
| `projects/adapter/postgres/projectdb/queries.sql.go` (generated) | `GetProjectRemediationPolicy`/`UpsertProjectRemediationPolicy` (hunks at ~463, ~1522) | LookupWebhookToken const/row (hunks ~813/~825), UpsertSource (~1631), UpsertTrigger (~1784). Either hand-split or stage whole file (webhook bits are additive dead code until the webhook commit) |
| `projects/adapter/http/handler.go` | `PutConfigurationRemediationPolicy` in service interface; `remediationPolicyRequest`/`remediationPolicyResponse`/`mapRemediationPolicy`; config response fields; `case "remediation-policy"` | `probeSSHContainers` + sshContainersRequest/Response + dockerContainerResponse + `ProbeSSHContainers` interface entry |
| `projects/adapter/http/handler_test.go` | `PutConfigurationRemediationPolicy` fake + `TestConfigurationRemediationPolicyPUTIsComponentScoped` | `ProbeSSHContainers` fake |
| `commands/migrate/source_test.go` | `15→16` count + 000016 fragment assertions — **but see compile deps**: cannot compile without `source.go`/`migratedb`/`000001-000004`; recommend deferring to the migrate-refactor commit | — |

## (C) Unrelated files — do not stage

- **SSH/Docker**: `adapter/sshlog/container_runtime.go`(+test, doc.go),
  `application/ssh_inspect.go`(+test), `application/docker_tool.go`(+test),
  `application/runtime_evidence.go`(+test), `domain/docker.go`,
  `application/context_assembler.go`, `application/evidence_gate.go`(+test),
  `application/tool_catalog_dynamic_test.go` (asserts non-Phase 1 SSH advertise;
  its HEAD version still passes with Phase 1 code)
- **08-28 corrective retry / 08-24**: `application/agent_engine.go`,
  `application/agent_engine_contract_test.go`, `application/conversation_test.go`,
  `application/coordinator_test.go`
- **Logging/config/httpserver**: `platform/observability/logging.go`(+test),
  `platform/config/config.go`(+test), `platform/httpserver/{server,middleware,snapshot,server_test}.go`,
  `platform/postgres/querydebug.go`(+test), `.env.example`, `README.md`, `.gitignore`
- **Infra refactor**: `backend/go.mod`, `backend/go.sum`, `backend/Makefile`,
  `backend/api`, `backend/cmd/`, `backend/dev/`, `backend/tools/`,
  `backend/sqlc.yaml`, `backend/tests/integration/`, `backend/migrations/` (dup),
  `bootstrap/{bootstrap,bootstrap_admin,migrate,postgres,redis,seed,remediation_loaders,*.test}.go`,
  `commands/migrate/{source,runner,runner_test,queries.sql,doc,migratedb}.go`,
  `commands/migrate/migrations/000001,000002,000004`, `platform/postgres/*`,
  `platform/redis/*`, `platform/observability/{remediation_payload,telemetry}.go(+tests)`
- **Other modules**: `modules/auth/**`, `modules/observations/**`, `modules/system/**`,
  `projects/adapter/openai/**`, `projects/adapter/postgres/repository_stack_test.go`
- **Frontend**: `frontend/**`
- **Tooling/docs**: `.trellis/**` (spec/journal), `.pi/`, `.agents/`, `.claude/`, `.codex/`,
  `AGENTS.md`, `CLAUDE.md`

## Compile dependencies a staged-only tree needs

1. **Go module scaffold** — `backend/go.mod` + `go.sum` (and the bootstrap/cmd
   composition that references `NewCheckpointStore`) — unavoidable; the Phase 1
   commit must land after/with the infra commit, or CI runs on the full working tree.
2. **Migration runner** — `000016` is only applied through
   `commands/migrate/source.go` `//go:embed`. The `source_test.go` Phase 1 hunk
   additionally needs `000001`/`000002`/`000004` present (count must equal 16).
   Recommended: commit `000016` standalone, defer the `source_test.go` hunk to the
   migrate-refactor commit (which must bump the count to 16).
3. **sqlc generated code** — `remediationdb/models.go` + `remediation.sql.go`
   must be staged together with `queries/remediation.sql` and migration `000016`
   (schema source). Same for `projectdb/queries.sql.go` with the projects policy
   queries (staged whole or hand-split; regenerating requires sqlc.yaml + full
   schema = infra).
4. **Domain→application→adapter chain** — `domain/{checkpoint,evidence_read,recovery,budget_plan}.go`
   are required by `application/{resilience,budget_plan,recovery}.go`, which are
   required by `coordinator.go`; `adapter/postgres/{checkpoint_store,evidence_read}.go`
   implement the ports. All in (A).
5. **conversation.go `AppendRecoveryChallenge`** — called by `resilience.go`
   (soft-budget recovery challenge); must be staged with it.
6. **budget.go `remaining()`** — used by `resilience.go` recovery envelopes.
7. **fakes_test.go Phase 1 hunks** (`mode`, Version mirror) — required by
   `resilience_test.go`.
8. **tool_gateway_test.go Phase 1 hunks** — HEAD's
   `TestToolGateway_AdvertisedDefinitionsAreBoundedSchemas` expects 6
   definitions; Phase 1 adds evidence.read → must become 7, else the staged
   test suite fails.
9. **NOT required**: `platform/postgres/querydebug.go` (no new pointer-arg
   formatting needed by Phase 1 queries — it is logging infra).

## Suggested staging order

1. Infra/migrate commit (module scaffold + runner + 000001-000004) — prerequisite.
2. Phase 1 (this commit): (A) files, then (B) partial hunks via `git add -p`,
   then `git commit`. Defer `commands/migrate/source_test.go` hunk to commit 1
   (update count to 16 there) or include it in commit 1.
3. Verify: `go build ./...`, `go vet ./...`,
   `go test ./internal/modules/remediation/...`, focused Phase 1 tests,
   `git diff --cached --check`.
