# Remediation Adapter Guidelines

> Established contracts for the walking-skeleton Git, SSH-log, and OpenAI
> adapters that implement the frozen remediation ports.

---

## Scenario: Trusted Read-Only Adapters Behind Frozen Ports

### 1. Scope / Trigger

Use this contract when changing repository read, SSH inspect / leftover SSH
log evidence, or the OpenAI provider adapter, or when wiring those adapters in
the API composition root.

- The coordinator and AgentEngine consume only frozen ports in
  `internal/modules/remediation/domain/ports.go`. They never receive
  credentials, raw Git/SSH clients, or provider SDK types.
- Adapters own credential injection, process execution, and HTTP. Application
  and domain packages import no HTTP, env, pgx, or redis.
- This slice is read-only: no workspace mutation, no Git write, no sandbox, no
  outbox, and no new secret kind.
- Pilot decisions: log source is an SSH log path. The wrapping key is
  `FIXTHE_ENCRYPTION_KEY`. The OpenAI-compatible API key, base URL, and model
  come from `project_llm_providers` plus a same-project `http_bearer` secret.

### 2. Signatures

Frozen ports (do not rename or add credential/SDK types):

```go
func (RepositoryReadPort).ListTree(context.Context, RepoRef, string, TreeOptions) (TreeListing, error)
func (RepositoryReadPort).ReadFile(context.Context, RepoRef, string, ReadOptions) (FileContent, error)
func (RepositoryReadPort).Search(context.Context, RepoRef, SearchQuery) (SearchResult, error)
func (RepositoryReadPort).History(context.Context, RepoRef, string, HistoryOptions) (History, error)

func (EvidenceLogPort).Search(context.Context, EvidenceScope, LogQuery) (EvidencePage, error)
func (EvidenceLogPort).GetContext(context.Context, EvidenceScope, EvidenceAnchor) (EvidencePage, error)

func (SSHInspectPort).Inspect(context.Context, EvidenceScope, SSHInspectRequest) (SSHInspectResult, error)

func (LLMProviderPort).Complete(context.Context, ModelTurn) (ModelResult, error)
```

Reasoning-output accounting remains provider-neutral:

```go
var ErrModelOutputExhausted error

type ModelResult struct {
    ModelCalls                          int
    UsageTokensIn, UsageTokensOut       int64
    UsageTokens, UsageCostCents         int64
    CacheTokensReported                 bool
    CacheHitTokens, CacheMissTokens     int64
    FinishReason                        string
    // Existing content, tool, provider, model, and request-shape fields remain.
}
```

`ModelCalls == 0` is the compatibility form for one provider call. Consumers
that charge budgets or emit logical-turn logs must normalize zero to one.

Landed value types:

```go
type RepoRef struct {
    ProjectID string // project UUID, never incident UUID
    RemoteURL string // credential-free; no userinfo
    Commit    string // incident/run baseline metadata; Git reads use configured branch
}

type RepositoryConfig struct {
    RemoteURL          string
    Transport          string
    CredentialSecretID string
    ProductionBranch   string // current branch fetched before each read
}

type EvidenceScope struct {
    ProjectID, EnvironmentID, SourceID string
    TimeRange                          TimeRange
}

type ModelTurn struct {
    ProjectID    string // project UUID so the adapter can resolve the named secret
    SystemPrompt string
    UserMessage  string
    Messages     []ModelMessage
    Continuation string // provider-native incremental input; UserMessage remains the full fallback
    Tools        []ToolDefinition
    MaxTokens    int
    Temperature  float64
}
```

Composition and loaders:

```go
func bootstrap.newProjectRuntimeLoaders(*projectpostgres.Repository) (*projectRuntimeLoaders, error)
func (*projectRuntimeLoaders).LoadRepository(context.Context, string) (git.RepositoryConfig, error)
func (*projectRuntimeLoaders).LoadSSHSource(context.Context, string, string) (sshlog.SourceConfig, error)
func (*projectRuntimeLoaders).GetEncryptedSecret(context.Context, string, string) (projectdomain.EncryptedSecret, error)
func (*projectRuntimeLoaders).FindNamedSecret(context.Context, string, string, projectdomain.SecretKind) (projectdomain.EncryptedSecret, error)

type git.Options struct { Logger *slog.Logger /* plus existing dependencies/bounds */ }
func git.NewReader(git.Options) (*git.Reader, error)
type sshlog.Options struct { Logger *slog.Logger /* plus existing dependencies/bounds */ }
func sshlog.NewReader(sshlog.Options) (*sshlog.Reader, error)

func (*application.RemediationCoordinator).Start(context.Context, domain.NewRun) (domain.Run, error)
func (*application.RemediationCoordinator).resolveRefs(context.Context, domain.Run) (domain.RepoRef, domain.EvidenceScope, error)
```

`EvidenceLogPort.GetContext` is the landed name. Do not rename it to `Context`.

### 3. Contracts

#### Wiring

- `Start` looks up the incident by internal UUID and fills
  `RepoRef{ProjectID, RemoteURL, Commit}` plus
  `EvidenceScope{ProjectID, EnvironmentID, SourceID}`.
- `ProjectID` is the project UUID. Never copy `run.IncidentID` into
  `RepoRef.ProjectID` or `EvidenceScope.ProjectID`.
- `RemoteURL` comes from `RepositoryRemoteResolver.CredentialFreeRemoteURL`.
  An empty or missing remote fails the run. The coordinator must not invent a
  URL.
- Bootstrap constructs the three real adapters from `projectRuntimeLoaders` and
  the existing AES-GCM cipher. It must not add a public plaintext-secret method
  on `projects.Service`.

#### Git read

- Load the project's `ProductionBranch`, fetch remote heads before each read
  session, and operate on `refs/heads/<ProductionBranch>` so list/read/search/
  history see the current production branch tip. The run's `RepoRef.Commit`
  remains historical metadata and must not select the Git object for these reads.
- Validate the branch as a Git ref before cloning or fetching. Pass the fully
  qualified ref as an exec argument; never interpolate branch input into a shell
  command. Use `git fetch --prune` so a deleted production branch cannot leave a
  stale cached ref available.
- Prefer `ref.RemoteURL` when non-empty; otherwise load config by
  `ref.ProjectID`. Transport and `CredentialSecretID` always come from project
  config.
- HTTPS injects `projects/application.AuthenticatedHTTPSRemote` plus
  `GIT_TERMINAL_PROMPT=0`. SSH writes a `0o600` temp key and sets
  `GIT_SSH_COMMAND=ssh -i <key> -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o BatchMode=yes`.
- After `git clone --bare` of an authenticated HTTPS URL, rewrite `origin` back
  to the public URL so userinfo never remains on disk.
- Binary files return `FileContent{Truncated: true, Reason: "binary"}`. Oversized
  files use `Reason: "oversized"`. Path traversal (`..`, absolute paths) is
  rejected before exec.
- Unreachable remotes collapse to `application.ErrGitUnreachable`. Do not echo
  authenticated URLs, tokens, or PEM material.
- `file://` exists only for local unit-test repos. Production loaders emit
  `https` or `ssh`.
- `Options.Logger` is optional. Network clone/fetch emit
  `git.request.completed` using only the public remote identity, duration,
  outcome, and low-cardinality failure class. Never derive a log field from the
  authenticated target, exec environment, `GIT_SSH_COMMAND`, or temp key path.

#### SSH inspect

- SSH sources advertise only `ssh.inspect`. Do not advertise `evidence.search`
  or `evidence.context` for kind `ssh`. Do not extend `EvidenceLogPort` with
  inspect methods; leftover `Search`/`GetContext` on the SSH reader are
  compatibility only.
- Source JSON is the validated version-1 SSH config. `ParseSSHSourceConfig`
  keeps `projectFolder` as the inspect cwd. `logPath` is a bootstrap hint for
  where logs often live, not a file to auto-tail.
- The model supplies a command string. The gateway tokenizes it without a
  shell and validates each pipeline segment against a per-command read-only
  policy registry, then reconstructs a quoted argv. Simple file/display/system
  families share a no-mutation validator (`ls`, `cat`, `head`, `tail`, `grep`,
  `egrep`, `fgrep`, `stat`, `wc`, `file`, `readlink`, `realpath`, `pwd`,
  `uname`, `df`, `du`, `ps`, `id`, `free`, `uptime`, `lscpu`, `lsblk`, `lsof`,
  `netstat`, `getent`, `who`, `w`, `last`); mixed-purpose families carry
  explicit read-only subcommand/option validation: `hostname` (read flags only,
  no positional), `date` (no `-s`/`--set`), `find` (no delete/exec/ok/fprint/
  fls), `tail` (no follow), `journalctl` (no follow/vacuum/rotate/flush/sync/
  key-catalog/relinquish), `dmesg` (no clear/read-clear/console mutations),
  `ss` (no `-K`/`--kill`/`-D`), `ip` (show/list/get and `netns list` only),
  `systemctl` (status/show/cat/list-*/is-* only), `docker` (version/info/ps/
  inspect/top/non-streaming stats/bounded non-following logs plus image/network/
  volume/container list/inspect). `env`/`printenv` are not registered: their
  output discloses credentials and unrelated process configuration. SSH executes
  that reconstructed string after `cd -- <quoted projectFolder> &&`. Never
  `bash -c` the raw model string.
- At most three `|` segments. Adjacent unquoted `|` operators become `||` and
  are rejected before SSH; a quoted `'|'` remains a literal argument.
- Glob metacharacters `*?[` are rejected only when unquoted and unescaped.
  After quotes are stripped, `grep '[0-9]+'` and `grep 'foo*'` are literals.
  Unquoted `ls *.log` is `invalid_arguments`. Do not scan decoded argv for glob
  characters after quote removal.
- Reject before SSH: `;`, `&&`, `||`, `$()`, backticks, redirections, env
  assignments, `sudo`, newlines, relative `..`, `find -exec/-delete/-ok`,
  `journalctl` follow/vacuum flags, `ss --kill`, `ip` write actions, Docker
  lifecycle/exec/pull/push, interpreters, package managers, and nested command
  execution. Short options are checked per character so combined flags such as
  `-fb` cannot bypass the exact-match checks.
- Timeout 15s. Combined stdout+stderr cap 64KiB; overflow truncates,
  `truncated=true`, aborts the process, and still returns the captured prefix.
- Inspect success payload is `{command, exitCode, stdout, stderr, truncated,
  bytesRetrieved}`. A successful `ssh.inspect` result is projected into a
  canonical runtime-evidence payload (secret-redacted, bounded to 64KiB,
  provider `ssh`, kind `runtime`, classification `correlated_supporting`, primary
  `true`, operational correlation `true`) and persisted as run-owned
  `remediation_evidence` before the success observation is emitted. The
  model-visible payload is byte-identical to the persisted payload and carries
  the persisted evidence ID for citation. PEM private-key blocks, `fixthe-ssh*`
  temp key paths, tokens, passwords, and authorization values are redacted in
  both places.
- `ssh_private_key` is the supported path. `ssh_password` is rejected.
- Exec injectable `ssh` with the same `IdentitiesOnly` / `BatchMode` /
  `accept-new` options as Git. Operator `ssh.evidence.completed` includes the
  reconstructed command, host/port, duration, bytes, outcome, and secret
  ID/kind. It omits stdout, plaintext private-key bytes, and temp key paths.
- Bootstrap SSH snapshots carry host, user, `projectFolder`, and `logPath`.
  The first diagnosing turn tells the model to `ls` the hinted directory and
  discover file names before reading. `preparing_context` must not SSH.

#### OpenAI provider

- Model is `gpt-5.6`. `ModelResult.Provider` is `openai`.
- Production loads `project_llm_providers` for the project UUID, then decrypts
  `CredentialSecretID` (`http_bearer`). Do not add an `openai_api_key` kind and
  do not look up a secret by the name `openai`.
- `Complete` POSTs `/v1/chat/completions` with `response_format=json_object`.
  `Content` is the raw assistant text for `DecodeAgentEnvelope`.
- `ToolDefinition.Name` is the logical gateway name and may use dotted
  namespaces such as `repository.read_file`. The adapter must send a separate
  provider-facing `function.name` that matches `^[A-Za-z0-9_-]+$` and is at
  most 64 bytes. The per-turn mapping must cover tool definitions, historical
  assistant tool calls, and provider responses; responses are mapped back to
  the logical name before returning `domain.ToolCall`.
- Transient outbound failures use at most three HTTP attempts. Network/timeouts
  and `408`/`429`/`500`/`502`/`503`/`504` responses may retry with bounded
  exponential backoff and jitter; a valid `Retry-After` is honored up to the
  adapter cap. `400`,
  authentication, configuration, and response-protocol errors do not retry.
  Each attempt emits its own bounded `llm.request.completed` observation.
- `FIXTHE_REMEDIATION_MODEL_TIMEOUT` configures one complete logical model turn
  (default 5m, deployment range 30s..20m). Configuration/secret lookup, every
  HTTP attempt, response reads, and retry backoff share that context. Never put
  the logical timeout on `http.Client.Timeout`: a per-attempt timeout would
  multiply the allowed duration by retry count. An earlier caller deadline,
  including the run work deadline or webhook normalization timeout, wins.
- Remediation turns start with `max_tokens=8192`. A decoded response with
  `finish_reason=length`, blank content, and no valid native tool calls returns
  `ErrModelOutputExhausted` and retries exactly once with `max_tokens=16384`.
  Both output attempts remain inside the same `Complete` call and logical-turn
  context. Authentication, HTTP/status, transport, malformed JSON, ordinary
  blank responses, and envelope validation do not use this retry.
- Decode usage, cache counters, and `finish_reason` before checking for blank
  content/tool calls. Aggregate both output attempts' model-call, token, cost,
  and cache counters; the final attempt owns content, tool calls, finish reason,
  and request-shape metadata. If the second attempt also exhausts output,
  return its typed error together with the aggregate `ModelResult`.
- Provider-native history is bounded to 256 KiB of encoded provider-neutral
  messages as well as the item limit. Retain newest complete user-led groups;
  never emit an assistant native tool call without all of its tool results or
  an orphaned tool result. Semantic compaction belongs to the tool-driven
  context workflow, not the provider adapter.
- A static API key is test-only. Production bootstrap must use the encrypted
  store.
- Missing project ID, missing named secret, non-200, oversized body, or invalid
  JSON fail closed with a wrapped error that does not interpolate the API key
  or response body.

Gateway bounds the adapters must not exceed:

| Option | Default |
|---|---|
| `TreeOptions.MaxDepth` | 2 |
| `TreeOptions.MaxEntries` | 500 |
| `ReadOptions.MaxBytes` | 1 MiB |
| `SearchQuery.MaxResults` | 100 |
| `HistoryOptions.MaxCommits` | 50 |
| `LogQuery.MaxLines` | 500 |
| `LogQuery.MaxBytes` | 1 MiB |

#### Tool-driven context and dynamic MCP

The remediation coordinator starts from durable metadata and performs connector
reads only through the Tool Gateway. Preparation must not call Git, SSH, or log
data ports. A best-effort MCP discovery probe may run during preparation, but a
discovery failure is a capability observation and must not prevent the first
diagnosis model turn.

The additive provider-neutral contracts are:

```go
type DynamicToolRuntimePort interface {
    Discover(context.Context, DynamicToolScope) (DynamicToolCatalog, error)
    Call(context.Context, DynamicToolScope, DynamicToolCall) (DynamicToolResult, error)
    CloseRun(context.Context, string) error
}

type ModelMessage struct {
    Role       string
    Content    string
    ToolCallID string
    ToolCalls  []ToolCall
}

type ToolCall struct {
    ID        string
    Name      string
    Arguments map[string]interface{}
}
```

- The coordinator remains the only run-state owner. The Tool Gateway remains
  the only model-requested execution entry point.
- `ModelTurn.Tools` must be serialized into the actual provider request. A
  populated application field that the adapter ignores is not a valid tool
  contract. No-argument tools still use a valid object schema with
  `properties: {}` and `additionalProperties: false`; never serialize
  `properties: null`, which provider-compatible APIs may reject before model
  inference.
- Tool results are appended as ordered, bounded observations to the next model
  turn. Success, empty result, policy rejection, unavailable capability, and
  adapter failure all have a model-visible status and stable safe error code.
- Provider-native calls and the strict JSON `requestTool` envelope resolve
  through the same catalog, schema validation, phase check, policy check, and
  budget accounting.
- Dynamic MCP identifiers are namespaced by source/server identity. The
  runtime retains the original server tool name internally; the model receives
  only the bounded description and input schema.
- A usable MCP catalog advertises `source.search_tools(query, limit)` instead
  of every approved dynamic schema. Search considers only policy-approved
  routes allowed in the current phase, returns at most 10 deterministically
  ordered compact matches, and activates those routes for the following model
  turn without calling the MCP runtime. There is no catalog-wide schema byte
  cap; existing per-tool validation remains authoritative.
- Activation lasts for the run, but `DefinitionsForPhase` reapplies policy on
  every turn. Planning cannot see a diagnosis-only route. Refresh atomically
  replaces discovery state and clears activation so stale public identities
  cannot survive a catalog version change.
- MCP policy is the authority. Missing policy exposes no dynamic MCP tools;
  server annotations, including `readOnlyHint`, are descriptive only.
- `*mcp.ToolAnnotations` is optional. A nil annotation pointer is valid and is
  stored as absent metadata; it is not an invalid discovery response.
- Dynamic discovery is published atomically. A failed refresh clears stale
  routes and exposes an unavailable capability state instead of a partial
  catalog.
- MCP runtime sessions are keyed to run, project, source, source version, and
  policy identity. Calls missing the trusted source/server mapping are rejected
  before transport execution.
- Stdio MCP inherits only explicitly configured environment entries. Secret
  references are resolved inside the adapter and are never copied into model
  context, audit metadata, or normal logs.
- SSH sources advertise `ssh.inspect`. A Docker deployment additionally
  advertises the typed `docker.logs` tool: generic inspect covers host/network/
  process evidence, typed logs cover incident-window collection. The gateway
  tokenizes the model command without a shell, validates each segment against
  the per-command read-only policy registry, and reconstructs a quoted argv.
  Adjacent unquoted `|` operators become `||` and are rejected before SSH; a
  quoted `'|'` remains a literal argument.
- A Tencent CLS webhook with a normalized alert is a mandatory evidence gate.
  Until a trusted `provider_detail` record is available, the run catalog
  advertises only the no-argument `evidence.tencent_cls_detail` tool; diagnosis
  and stop envelopes are rejected with `required_direct_evidence`. The tool
  resolves the callback by incident identity and never accepts a model URL.
  After successful persistence, the normal repository/runtime tools are
  restored. A detail connector failure remains a model-visible safe error and
  cannot silently open the gate.
- Glob metacharacters `*?[` are rejected only when they appear unquoted and
  unescaped. After quotes are stripped, `grep '[0-9]+'` and `grep 'foo*'` are
  literal arguments. Unquoted `ls *.log` remains a policy rejection. Do not
  scan the decoded argv text for glob characters after quote removal.
- Successful `ssh.inspect`/`docker.logs` results share one canonical
  runtime-evidence projection for persistence and model context: UTF-8
  normalized, credential-shaped fields/text recursively redacted, PEM blocks and
  `fixthe-ssh*` temp key paths removed, and payload bounded to 64KiB. The
  persisted payload and the model-visible payload are byte-identical and the
  model-visible observation exposes the persisted evidence ID. Operator logs
  omit private-key bytes, temp key paths, and `-i` argv.

Tool observations cross two independent boundaries. Adapter values are first
bounded and redacted by the adapter/gateway before they reach either the
`RunObserver` or model context, then typed repository and evidence values are
normalized before entering model context. In particular,
`domain.FileContent.Content` must be treated as text rather than allowing
`encoding/json` to turn `[]byte` into reversible base64. Struct payloads such as
`EvidencePage` are JSON-normalized recursively so nested secret-bearing keys
and text assignments are covered by the same redaction rules.

Provider-native conversation is append-only between ordinary turns.
`UserMessage` remains the complete bounded fallback for legacy providers;
OpenAI uses `Continuation` when present. The first continuation carries the
phase instruction and bootstrap, later continuations carry only newly pending
strict-tool observations or protocol corrections, and native tool results stay
in paired assistant/tool messages. Pending content is acknowledged only after
a successful provider result, so a failed call can retry the same increment.
The assistant response itself is committed only after strict envelope and
current-phase validation succeeds; rejected content, mixed tool/content output,
and invalid native calls never enter accepted history or acknowledge pending
continuation. A provider failure therefore retains the same pending input for
the existing retry path.

Diagnosis envelopes use a numeric confidence contract: `diagnosis.confidence`
must be a JSON number in the inclusive range `0.0..1.0`; semantic labels such as
`"low"`, `"medium"`, and `"high"` are invalid. When the decoder returns a
structured `json.UnmarshalTypeError` for that field, the application maps it to
the bounded `invalid_confidence` protocol correction with path
`diagnosis.confidence` and expected type `number`. The correction must not copy
provider decoder internals into the next model turn.

Every diagnosis turn and allowlisted diagnosis protocol correction reuses one
canonical wire-contract instruction. It includes the `schemaVersion=v1` /
`kind=diagnosis` envelope, string `fixability` enum, numeric `confidence`, array
shapes for evidence/source fields, and concrete nested object shapes. In
particular, `timeAssessment.basis` is exactly one of `paired_epoch`,
`explicit_offset`, `contextual_zone`, or `unresolved`; explanation text belongs
in `causalReasoning`, contradictions, or other narrative fields, never in
`basis`. Typed fixability, confidence, source-coverage, citation, and time
assessment corrections must include the full canonical contract while omitting
raw model values and decoder internals.

Planning remains tool-driven after the evidence gate admits a `code_fixable`
diagnosis. The planning model receives the phase-filtered catalog and may return
one or more native or envelope `requestTool` calls before `planCandidates`.
Coordinator execution must use the same `runTool` gateway, phase policy, budget,
invocation audit, and conversation observation path as diagnosis; a successful
planning tool request resets the planning protocol-failure counter. The initial
planning prompt and every allowlisted planning correction reuse one canonical
wire contract containing the complete `schemaVersion=v1` / `kind=planCandidates`
shape, candidate fields, required `recommendedId`/`rationale`/`suggestedDiff`,
and `ordinary|high_risk|denied_control_plane` risk enum. Envelope validation
additionally requires every candidate to carry non-empty `evidenceRefs` and
`affectedFiles` arrays plus non-empty `intendedBehavior` and
`rollbackStrategy`, so incomplete candidates are rejected before persistence.
Known validation
failures return a stable code, path, expected field, and fixed reason; correction
normalization must reject any message not equal to an internally allowlisted
combination so model-provided values and decoder internals cannot be replayed.

The automatic run work budget defaults to 20m. Admission still rejects a new
model or tool operation after exhaustion, and each admitted external operation
receives a child context ending at the run deadline. When that child expires,
account the attempted model/tool effect and transition to `budget_exhausted`
with reason `elapsed`. Use the uncanceled lifecycle context for terminal state
persistence, observations, and cleanup so the durable run cannot remain active.
A logical 5m model timeout while run time remains is a provider failure, not run
budget exhaustion. Model calls, model cost, tool calls, evidence bytes, and
repository bytes remain independent exhaustion reasons.

### Tool-driven validation and error matrix

| Condition | Required behavior |
|---|---|
| Preparation connector read fails | Continue to `diagnosing`; advertise safe unavailable capability state |
| Tool success or empty result | Append ordered bounded observation to the next model turn |
| Policy/phase/schema/path rejection | Record `rejected`; do not invoke an adapter or fail the run |
| Connector timeout/rate-limit/transport | Record stable retryable error; count the call against the shared run budget |
| Authentication/authorization/invalid configuration | Record stable non-retryable error; omit raw response, stderr, headers, and secrets |
| MCP annotations absent | Accept discovery and retain empty descriptive annotations |
| MCP tool absent from policy | Omit it from the model catalog and reject direct calls before `tools/call` |
| MCP discovery refresh fails | Replace routes with unavailable status; never retain a partial or stale catalog |
| MCP catalog has many approved routes | Expose `source.search_tools`; do not eagerly serialize all dynamic schemas or add a total schema-byte cap |
| Search matches a diagnosis-only route | Advertise it on the following diagnosis turn; omit it again in planning |
| Dynamic result exceeds bound | Return a deterministic truncation marker and byte count |
| Quoted inspect glob / character class (`grep '[0-9]+'`, `grep 'foo*'`) | Reconstruct as a literal argv argument; do not reject after quote stripping |
| Unquoted inspect glob (`ls *.log`) or adjacent unquoted `\|\|` | Reject as `invalid_arguments` before SSH |
| Inspect stdout/stderr with PEM or `fixthe-ssh*` temp key path | Keep raw secrets/tokens; strip only PEM blocks and temp key paths |
| Invalid diagnosis envelope or mixed native tool+content | Record bounded `invalid_envelope` observation; stay in the phase loop and count the turn against the model budget |
| Planning requests a phase-advertised repository tool | Execute through `runTool` with phase `planning`, persist invocation/budget effects, append the bounded result, reset consecutive protocol failures, and continue planning |
| Planning tool is rejected by policy/schema/path validation | Persist the stable rejection without adapter execution, append it to the planning conversation, and continue planning |
| Invalid planning JSON/schema/kind/candidates/recommendedId/suggestedDiff/planId/risk | Return the matching allowlisted planning code/path/expected field plus the complete canonical planning contract; never echo the rejected value or raw decoder error |
| Diagnosis `confidence` is not a JSON number | Record bounded `invalid_confidence` with path `diagnosis.confidence` and expected type `number`; include numeric `0..1` guidance without decoder internals; stay in the phase loop |
| Diagnosis `fixability` is an object or `sourceCoverage` is an object | Record the typed field correction and replay the complete canonical diagnosis contract; never normalize model-controlled objects into accepted values |
| `timeAssessment.basis` is narrative text or an unknown value | Record `invalid_time_assessment` with the four allowed enum values; retain strict decoding and keep raw model text out of the correction |
| Tencent CLS normalized alert has no successful provider detail | Advertise only `evidence.tencent_cls_detail`; reject diagnosis/stop with `required_direct_evidence` until the trusted tool persists detail evidence |
| Tencent detail tool receives a URL or extra argument | Reject before adapter execution; the tool accepts an empty object and resolves the URL from the incident-bound callback |
| Tencent detail resolution fails | Return stable `provider_detail_*` code/retryability to the model and emit `tencent_cls.detail.completed`; keep the mandatory gate closed |
| Known `evidenceRef` citation field | Strictly reject the citation, retain the operator diagnostic, and send only bounded `evidenceCitations[].evidenceRef` → `evidenceId` guidance; never accept it as an alias |
| First or second consecutive invalid envelope in a phase | Account the model effect and append one allowlisted protocol correction for the next turn |
| Third consecutive invalid envelope in a phase | Account the model effect, append no further correction, and transition to `blocked_manual_review` unless an independent run budget exhausted first |
| Phase-valid diagnosis or `planCandidates` envelope | Reset that phase's consecutive protocol-failure count; planning owns a separate counter from diagnosing |
| Provider/infrastructure model failure | Remain a harness `failed` outcome; do not invent an envelope |
| `finish_reason=length` with blank content and no native tool call | Retry once at 16384 inside the same logical-turn deadline; return `ErrModelOutputExhausted` with aggregate usage if it happens again |
| Blank provider response with any other finish reason | Fail closed as a protocol error; do not use the output-budget retry |
| Output retry succeeds | Persist two model calls and aggregate both attempts' token/cost/cache usage; record only the final assistant content/tool calls in history |
| Native history exceeds 256 KiB or its item limit | Evict oldest complete user-led groups; never retain a partial assistant/tool group |
| Logical model-turn deadline expires while run time remains | Fail as a provider timeout; all attempts together consumed at most the configured turn duration |
| Run deadline expires during a model/tool operation | Cancel the operation, account it, and persist `budget_exhausted` with reason `elapsed` using the lifecycle context |

### Tool-driven tests required

- Coordinator: first diagnosis turn with zero eager repository/evidence reads;
  successful and failed tool observations affect the following turn; an invalid
  diagnosis envelope is retried as a protocol observation; planning receives the
  compacted diagnosis conversation; retry and hard-budget bounds remain inspectable.
- Diagnosis protocol: prompt and system instructions require the complete
  canonical wire shape and numeric `confidence`; object-shaped `fixability`,
  object-shaped `sourceCoverage`, a string confidence label, and narrative
  `timeAssessment.basis` each produce safe typed corrections. A following valid
  diagnosis uses a short allowed basis enum and completes the bounded retry path.
- Stop handoff protocol: a `stop` envelope is a terminal handoff to a human and
  must carry a non-empty `stop.recommendedNextAction` (schema `minLength: 1` and
  runtime `validateStop` both enforce it). A stop without a suggestion is fed
  back through the bounded `required_stop_suggestion` correction and must never
  terminalize directly. A legal stop persists one `unsafe_to_automate` decision
  (`stop.reason` → `causalReasoning`, `stop.recommendedNextAction` →
  `recommendedNextAction`) before transitioning to `blocked_manual_review`, so
  the review chain and continuation brief always carry a human-actionable
  suggestion even on the give-up path.
- Planning protocol: planning advertises only its phase-authorized repository
  tools, executes multiple native calls through the gateway, feeds successful or
  rejected observations into the next turn, and then accepts `planCandidates`.
  Tests assert model/tool budgets and invocation phases, prove a successful tool
  request resets the consecutive failure counter, and verify every known
  validation category reaches the model with its safe code/path and the same
  canonical planning contract used by the initial prompt.
- Tencent detail gate: a Tencent normalized alert exposes only the detail tool,
  a successful detail result persists a run-owned `provider_detail` and restores
  normal tools, while failure remains blocked and model-visible.
- Coordinator end-to-end: search activates one phase-approved MCP route, the
  dynamic call reaches the original runtime name exactly once, its result feeds
  re-diagnosis, planning removes diagnosis-only schemas, and the code-fixable
  path persists plans/diff before `diagnosis_ready_for_review`.
- Conversation: bootstrap, strict-tool observations, native tool results, and
  protocol corrections are delivered exactly once; rejected assistant output is
  absent from accepted history; provider failure does not acknowledge pending
  continuation content; three consecutive invalid envelopes stop at manual
  review and preserve the independent run-budget precedence.
- OpenAI: outbound requests contain every advertised tool definition and parse
  native tool calls; duplicate definitions/IDs, mixed content and tool calls,
  oversized schemas, and credential ownership mismatches fail closed. Slow
  success, retry backoff, and parent-deadline tests prove one shared logical
  deadline rather than a fresh timeout per HTTP attempt.
- MCP: initialization, paginated discovery, optional annotations, namespaced
  schemas, policy filtering, source/server ownership, call success/error,
  unsupported content, truncation, refresh isolation, and run cleanup.
- Redaction: typed `FileContent`, `EvidencePage`, MCP structured values, bearer
  tokens, `sk-...` values, PEM blocks, assignments, and authenticated remotes
  never appear in model context, normal logs, or persisted tool metadata.
- SSH inspect parser: quoted `grep '[0-9]+'` / `grep 'foo*'` are accepted;
  unquoted `ls *.log` and `ls &&` / `ls ||` remain rejected; expanded
  host/network/process/file/log/Docker read-only forms parse and execute, while
  mutating variants (`hostname <name>`, `date --set`, `ss --kill`, `ip` write
  actions, `journalctl --vacuum-*`, Docker lifecycle/exec) are rejected before
  SSH; canonical payloads redact credentials while preserving host identity and
  log content.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Missing project UUID on `RepoRef` / `EvidenceScope` / `ModelTurn` | Fail closed; do not invent IDs |
| Empty remote URL | Fail the run; do not invent a URL |
| Remote URL contains userinfo | Strip before returning to coordinator; never persist it as `origin` |
| Path traversal or absolute repo path | Reject before `git` exec |
| Binary blob | `FileContent{Truncated: true, Reason: "binary"}` |
| File larger than `MaxBytes` | Truncate or refuse with `Reason: "oversized"` |
| Git remote unreachable / auth failure | `ErrGitUnreachable`; no token/PEM/URL userinfo |
| Git clone/fetch with logger | Public remote identity only; authenticated target never enters the record |
| SSH source JSON missing `host`/`logPath` | Wrapped incomplete-config error |
| SSH source JSON includes `projectFolder` | Keep it as inspect cwd; do not ignore |
| `ssh_password` for inspect | Reject; private-key path only |
| SSH inspect with logger | Completion includes reconstructed command; omit stdout, private-key bytes, and temp key path |
| Unquoted inspect glob or `; && \|\| $()` | `invalid_arguments` before SSH |
| Quoted inspect glob / character class | Reconstruct as a literal argv argument |
| Inspect output over 64KiB | Truncate, `truncated=true`, return captured prefix |
| Unknown evidence ID | Empty `EvidencePage`, not an error/panic |
| Missing `openai` / `http_bearer` secret | Wrapped missing-convention error |
| OpenAI non-200 / invalid JSON / oversized body | Wrapped error; no key or body in the message |
| Decrypt failure | Fail closed; do not wrap raw crypto text into logs |

### 5. Good/Base/Bad Cases

- Good: coordinator receives `RepoRef{ProjectID: projectUUID, RemoteURL: "https://git.example/app.git", Commit: deployed}` and adapters decrypt inside their process, wipe plaintext, and return bounded redacted data.
- Good: bootstrap injects the process logger, Git logs public clone/fetch
  identity, and SSH logs safe credential metadata without command or output.
- Good: planning reads the current production branch through advertised
  repository tools, observes each bounded result, and only then returns a
  contract-valid candidate plan and unified diff.
- Base: a project with an SSH source and an `openai` bearer secret can run the diagnosis loop without exposing credentials to application or HTTP.
- Base: an MCP source sends built-ins plus `source.search_tools` on its first
  turn, then sends only explicitly activated, phase-allowed dynamic schemas.
- Base: a reasoning model exhausts 8192 output tokens without final content,
  then succeeds at 16384; the run charges two calls and all returned usage.
- Bad: stuffing `run.IncidentID` into `RepoRef.ProjectID`, leaving
  `https://user:token@host` in `origin`, hashing evidence IDs by tail index,
  rejecting `projectFolder`, adding `openai_api_key`, or decrypting through a
  public `projects.Service` method.
- Bad: logging authenticated remotes, plaintext private-key bytes, temp key
  paths, or treating a nil logger as a constructor error. Inspect operator logs
  may include the reconstructed command; they must not dump unbounded stdout.
- Bad: advertising repository tools in planning while rejecting every planning
  `requestTool`, or forwarding `cause.Error()` / model-provided invalid values in
  a protocol correction.
- Bad: replaying bootstrap/tool observations in both native history and the
  next user message, setting `http.Client.Timeout` to the logical turn limit so
  retries multiply it, or using an expired operation context to persist the
  terminal run state.
- Bad: treating a 200/`finish_reason=length` blank response as zero-token
  generic failure, retrying it under a fresh timeout, or truncating native
  history in the middle of an assistant/tool exchange.

### 6. Tests Required

- Git: exact-commit isolation against a later commit on the same repo; bounds
  on tree/file/search/history; binary refuse; path-traversal reject; cached
  origin has no userinfo; errors hide remote details; logger captures clone and
  fetch using only public identity.
- SSH inspect: parser accepts `ls /var/log | grep app` and quoted
  `grep '[0-9]+'`; rejects `ls; rm`, `cat $(pwd)`, `sudo journalctl`,
  `find -exec`, `journalctl -f`, unquoted `ls *.log`; fake `ssh` asserts
  reconstructed argv and `cd -- projectFolder`; 64KiB truncation still returns
  a prefix; canonical runtime-evidence payloads redact PEM/temp-key/token/
  password material while preserving host identity and log content; completion
  logs retain reconstructed command and secret
  ID/kind and omit private-key bytes and `-i` argv.
- OpenAI: `httptest.Server` asserts model `gpt-5.6` and bearer auth; maps usage
  and content; missing secret / non-200 / invalid JSON wrap without leaking the
  key; injected short durations prove slow success, shared retry deadlines,
  and earlier parent-deadline precedence.
- OpenAI reasoning output: assert 8192 then 16384 serialized `max_tokens`, one
  retry only for typed output exhaustion, unchanged request fields otherwise,
  aggregate usage/cache/model-call counters on success and second exhaustion,
  and no retry for an ordinary blank response.
- Conversation/accounting/logging: assert encoded native history remains at or
  below 256 KiB with complete groups, legacy zero `ModelCalls` charges one,
  retries charge two, and logical-turn logs contain `model_calls` plus the final
  `finish_reason` without response bodies or reasoning text.
- Wiring: `RepoRef.ProjectID` is the project UUID, `RemoteURL` is
  credential-free, `EvidenceScope` carries environment/source IDs.
- Unit tests inject binaries or local repos. They must not skip when a real
  remote, SSH host, or OpenAI key is absent.

### 7. Wrong vs Correct

#### Wrong

```go
ref := domain.RepoRef{ProjectID: run.IncidentID, Commit: run.DeployedCommit}
scope := domain.EvidenceScope{ProjectID: run.IncidentID}
plaintext, _ := projectService.DecryptForHTTP(ctx, secretID)
```

#### Correct

```go
identity, err := lookup.GetByID(ctx, run.IncidentID)
remoteURL, err := remotes.CredentialFreeRemoteURL(ctx, identity.ProjectID)
ref := domain.RepoRef{ProjectID: identity.ProjectID, RemoteURL: remoteURL, Commit: run.DeployedCommit}
scope := domain.EvidenceScope{
    ProjectID: identity.ProjectID, EnvironmentID: identity.EnvironmentID, SourceID: identity.SourceID,
}
// Decrypt only inside the adapter, then wipe.
plaintext, err := cipher.Decrypt(projectID, secretID, kind, ciphertext, nonce)
defer clearBytes(plaintext)
// Logger injection remains metadata-only; plaintext never enters an observation.
reader, err := sshlog.NewReader(sshlog.Options{Sources: sources, Secrets: secrets, Cipher: cipher, Logger: logger})
```

For reasoning-output retry, keep the retry at the provider boundary:

```go
// Wrong: a fresh Complete call grants a fresh logical-turn timeout and loses
// the first attempt's usage.
_, _, _ = engine.Turn(ctx, phase, projectID, prompt)

// Correct: one Complete call owns both bounded output attempts and returns
// aggregate accounting to the coordinator.
result, err := provider.Complete(ctx, turn)
effect := modelEffect(result)
```

For diagnosis confidence and time basis, keep the wire types and enum values
exact and correct errors with an allowlisted protocol observation:

```json
// Wrong
{"confidence":"low","timeAssessment":{"basis":"paired epochs prove the event time"}}

// Correct
{"confidence":0.2,"timeAssessment":{"basis":"paired_epoch"}}
```

The application should tell the model which field and type/enum to repair and
include the canonical diagnosis contract, but must not forward the raw
`json.UnmarshalTypeError`, validation value, or provider response.

For a mandatory Tencent CLS detail source, keep URL authority in the trusted
adapter:

```json
// Wrong: model supplies a capability-bearing URL
{"toolName":"evidence.tencent_cls_detail","parameters":{"url":"https://..."}}

// Correct: server resolves the callback saved for this incident
{"toolName":"evidence.tencent_cls_detail","parameters":{}}
```

## Scenario: Tencent CLS Detail Page Resolution

### 1. Scope / Trigger

Use this contract when changing the trusted Tencent CLS detail adapter or the
evidence boundary that consumes it. The provider's short URL is a browser
capability, not a record identifier: the page is an SPA shell and the alert
record is fetched by a fixed read-only API action.

### 2. Signatures

```go
func NewClient(Options) (*Client, error)
func (*Client) Resolve(context.Context, hooksapplication.TencentCLSCallback) (FetchResult, error)
func (*IncidentDetailResolver) ResolveTencentCLSDetail(context.Context, domain.TencentCLSDetailRequest) (domain.TencentCLSDetailResult, error)
func parseDetailResponse([]byte, string) (OperationalEvidence, error)

type Options struct {
    // Existing HTTP, logger, timeout, redirect, and byte-bound fields remain.
    RetryDelays []time.Duration
}
```

Webhook ingress has no provider-detail resolver dependency. It persists the
complete callback as the Observation and exactly one `normalized_alert`
evidence record. The incident-bound, no-argument
`evidence.tencent_cls_detail` tool is the only detail acquisition entry point.

### 3. Contracts

- Webhook ingress validates the Tencent envelope, stores the complete callback
  Observation, and persists only `normalized_alert`. It must not fetch detail or
  persist `provider_detail` / `connector_observation`; remediation invokes the
  incident-bound `evidence.tencent_cls_detail` tool with `{}`.
- The bounded GET follows only the existing trusted Tencent URL allowlist. The
  final regional URL may be
  `https://<region>-monitor.cls.tencentcs.com/cls_no_login?action=GetAlertDetailPage#/alert?RecordId=<uuid>&JumpDomainID=<uuid>`.
- Parse `RecordId` from the final URL fragment before considering compatible
  page/JSON forms. Never use the short-link path such as `MColyiGd` as the ID.
- Build the API request from the final authorized origin and send
  `POST /cls_no_login?action=GetAlertDetail` with
  `Content-Type: application/json` and exactly `{"RecordId":"<uuid>"}`.
- The detail response may be `application/json` or
  `text/plain; charset=utf-8`, but its body must be exactly one valid JSON
  object. The production envelope is `Response.Record.ResultsSnapshot`.
  Existing direct-record and `data` fixture shapes remain compatibility forms.
- Project `Response.Record` identity and snapshot metadata. Map each
  `AnalysisInfo[].AnalysisOriginal` to `AnalysisInfoItem.RawResult`; retain
  bounded raw result sections needed for operator/time correlation.
- Treat only `Response.Error.Code=-1001` (string or number) as the provider's
  eventual-consistency state. Retry its exact POST with the copied, validated,
  context-aware schedule; the default delays total less than two minutes.
  Exhaustion or cancellation maps to retryable `OutcomeUnavailable`.
- A successful detail is trusted only when it is `provider_detail`,
  `direct_fault`, `success`, available, primary, has a non-empty JSON object
  payload, and has complete provenance:
  `adapter=tencent_cls`, `detail_capability_validated=true`,
  `detail_resolution=validated_provider_detail_get_alert_detail`, and explicit
  `contradictions:[]`. Resolver persistence, bootstrap readiness, prompt
  preference, and runtime gate opening use this same predicate.
- Do not project or log `ActualCallback`, `H5AlarmShield`, `SecretID`,
  `SecretText`, callback/webhook URLs, or unrelated response-envelope fields.
  Request URLs in operator logs retain only provider-safe identity; capability
  paths, fragments, and query material are removed.

### 4. Validation & Error Matrix

| Condition | Required behavior |
|---|---|
| Invalid callback URL or final redirect host | `invalid` or `redirect_rejected`; no detail POST |
| Missing final-fragment `RecordId` | `invalid`; do not substitute the short-link path |
| Detail response is non-JSON `text/plain` | `invalid`; body decoder remains authoritative |
| Missing `Response.Record` or `ResultsSnapshot.AnalysisInfo` | `invalid`; no partial evidence |
| `Response.Error.Code=-1001` then valid record | Retry within the shared context; aggregate bytes across attempts |
| `-1001` schedule exhausted or canceled | Retryable `unavailable`; keep mandatory gate closed |
| Other `Response.Error`, missing/empty `AnalysisInfo`, or non-object payload | Non-retryable `invalid`; do not persist trusted detail |
| Missing/incomplete/malformed provenance, `contradictions:null`, or contradictions present | Reject detail and keep bootstrap/runtime gate closed |
| Normalized evidence persistence fails before remediation | Preserve the committed incident, report the background failure, and do not emit automatic remediation |
| Non-2xx provider response | Bounded `unavailable` observation with status |
| Page/detail body exceeds configured limit | `oversized`; retain only bounded diagnostics |
| Request deadline expires | `timeout` with existing retryability classification |
| Control material appears in the provider response | Exclude it from evidence and sanitized operator logs |

### 5. Good/Base/Bad Cases

- Good: a three-request flow extracts the UUID from the regional fragment,
  posts the exact JSON body, accepts `text/plain` JSON, and preserves
  `AnalysisOriginal` log fields in the evidence snapshot.
- Good: webhook ingress persists only the callback Observation and
  `normalized_alert`; the model is forced to request the no-argument detail
  tool, whose server-side resolver retries a temporary `-1001`, persists trusted
  direct evidence, and only then opens the normal tool catalog.
- Base: an existing direct record or `data` fixture is accepted only when it
  still satisfies the bounded snapshot/analysis contract.
- Bad: POSTing `MColyiGd`, parsing the SPA HTML as alert JSON, accepting
  arbitrary plain text, or copying the full `Response.Record` into evidence.
- Bad: logging the callback URL, response `ActualCallback`, H5 shield values,
  or secret-bearing error/body material.

### 6. Tests Required

- A production-shaped fixture must assert initial GET, regional GET, and fixed
  detail POST count; final-fragment UUID in the POST body; and the exact action.
- Assert `text/plain; charset=utf-8` parsing, `Response.Record` identity,
  snapshot metadata, `AnalysisOriginal` message/file/host/source/time/path,
  and preservation of query/result metadata.
- Assert `ActualCallback`, `H5AlarmShield`, `SecretID`, `SecretText`, callback
  URLs, and short-link capability paths are absent from evidence and logs.
- Assert webhook ingress performs no provider request, persists exactly one
  `normalized_alert`, and rejects wiring that cannot persist evidence before
  remediation. Evidence persistence failure must not emit automatic remediation.
- Assert `-1001` string/number retry success, exhausted retryable unavailable,
  cancellation during wait, copied/validated schedule, and aggregate bytes.
- Assert resolver, bootstrap, prompt preference, and runtime gate reject empty,
  null, array, contextual, contradictory, non-primary, or incomplete-provenance
  detail records, including missing/null `contradictions`.
- Assert malformed plain text, wrong envelope, missing ID, unsafe redirects,
  oversized bodies, timeouts, HTTP failures, and legacy fixture compatibility.

### 7. Wrong vs Correct

#### Wrong

```go
recordID := path.Base(callback.DetailURL) // "MColyiGd", not the alert UUID
resp, _ := http.Get(callback.DetailURL)
json.NewDecoder(resp.Body).Decode(&record)
```

#### Correct

```go
// Webhook ingress: persist callback/normalized alert only.
// AI tool call: no capability-bearing parameters.
result, err := resolver.ResolveTencentCLSDetail(ctx, incidentBoundRequest)
// Resolver owns redirect, RecordId extraction, -1001 retry, projection,
// trust validation, and provider_detail persistence.
```

The gateway opens normal tools only after `result.Evidence` satisfies the same
strict trust predicate used by bootstrap evidence.

## Scenario: Continue And Retry Flow

### 1. Scope / Trigger

A terminal remediation run is immutable history. "Continue" creates a new
linked attempt in the same `(incident_id, lifecycle_generation,
deployed_commit)` series and gives the agent a bounded structured brief of the
previous attempt. It never mutates the old run, recreates a root, replays raw
provider conversation, or treats model conclusions as facts.

### 2. Signatures

The frozen `domain.RunStore` method signatures never change. Continuation uses a
companion application port implemented by the PostgreSQL store and test fakes:

```go
type AttemptStore interface {
    GetLatestForIncident(context.Context, string, int64, string) (domain.RunAggregate, error)
    CreateNextAttempt(context.Context, domain.NextAttempt) (domain.Run, error)
}
```

`domain.NextAttempt` carries only opaque IDs, immutable series identity, current
context version, expected predecessor version, continuation origin
(`automatic_continue` / `manual_continue`), and a bounded safe reason. It never
contains credentials, URLs with userinfo, provider clients, prompts, or raw
webhook data.

Coordinator and trigger seams:

```go
func (c *RemediationCoordinator) Continue(context.Context, domain.NextAttempt) (domain.Run, error)
func (t *Trigger) Continue(context.Context, TriggerRequest, domain.NextAttempt) (domain.Run, error)
func (*Service) ContinueRemediation(context.Context, authdomain.User, string, string, int64, string, int64) (domain.Run, error)
```

`Service.ContinueRemediation` arguments are project key, public incident
identifier, current lifecycle generation, expected latest run ID, and expected
latest run version. The store repeats ownership and predecessor checks inside
its transaction.

### 3. Contracts

Schema (migration `000015_remediation_continuation`):

- `remediation_run.continuation_of_run_id uuid NULL REFERENCES remediation_run(id) ON DELETE SET NULL` — direct predecessor link.
- `trigger_reason text NOT NULL DEFAULT ''` — `automatic`, `manual`, `automatic_continue`, or `manual_continue`.
- `continuation_reason text NOT NULL DEFAULT ''` — bounded operator/system reason, never an error body or payload.
- `context_version bigint NOT NULL DEFAULT 0` — incident/evidence context snapshot seen by the attempt.
- `terminal_reason text NOT NULL DEFAULT ''` — safe stable terminal classification.
- `retryable boolean NOT NULL DEFAULT false` — service-owned eligibility metadata.

Old rows default to non-retryable and remain manually continuable from
allowlisted states. `CreateNextAttempt` is one transaction: parse and validate
the predecessor UUID and immutable identity → `SELECT ... FOR SHARE` the
current incident row and verify the requested context version → `SELECT ... FOR
UPDATE` the predecessor series row (using the incident→series lock order and
serializing all creators for one series) → confirm the predecessor is the latest
run, its version equals `ExpectedPreviousVersion`, and no active attempt exists
→ apply origin eligibility → insert `latest.AttemptNumber + 1` in `queued` →
commit.

Coordinator phase resume is derived from durable output, not free-form terminal
text. The persistence companion queries the latest qualifying attempt at or
before the expected predecessor rather than trusting bounded page history:

- latest durable decision is `code_fixable`, and the checkpoint has the exact
  same series/context version as the child → the service-owned evidence gate
  already admitted planning, so the child transitions `preparing_context ->
  planning` and the brief identifies both the direct predecessor and checkpoint;
- a newer/unknown context version or no qualifying checkpoint → the child
  transitions `preparing_context -> diagnosing` and analyzes the persisted
  evidence snapshot. A later same-context `insufficient_evidence` attempt does
  not erase an earlier valid planning checkpoint.

Manual and automatic drive modes differ after attempt creation:

- `manual_continue` builds an analysis-only catalog without dynamic discovery
  and exposes only repository list/read/search/history. It never calls Tencent
  detail, Docker logs, SSH inspect, evidence search/context, source discovery,
  or a dynamic MCP tool. Persisted evidence fields and values enter trusted
  model context unchanged; byte/count limits are bounds, not redaction.
- `automatic_continue` may use the normal source catalog because the webhook
  gate requires a newer committed context version.

This distinction prevents a page retry from changing its evidence input while
still allowing repository reads needed to map the established fault to code.

Eligibility:

- `automatic_continue`: latest state `failed`, `retryable` true, request context
  version greater than latest, fewer than 3 prior automatic continuations in the
  series (counted while holding the series lock).
- `manual_continue`: latest state in `{failed, budget_exhausted, blocked_manual_review}`.

REST contract (project-scoped, admin/operator capability for mutations):

```http
POST /api/v1/projects/{projectKey}/incidents/{id}/remediation/retry
{"generation":1,"runId":"<latest-run-id>","version":2}
```

Success returns `runId`, `seriesId`, `status`, `generation`, `attemptNumber`,
`version` — a new monotonically numbered attempt, never the previous terminal
run. `POST .../remediation/start` keeps initial-start behavior for a series
with no run; repeated root calls stay idempotent.

The review response adds current attempt metadata (attemptNumber, version,
origin, terminalReason, retryable), server-computed `continuationAvailable`
(project write capability AND latest state in the manual allowlist AND no
active attempt), bounded attempt history, and a top-level `manualSuggestion`
derived from the latest decision's `RecommendedNextAction` through
`sanitizeReviewText` (empty string when no decision or no suggestion). The
incident page renders a prominent "人工修复建议 / Manual fix suggestion" block
with the missing-evidence list only when `status == "blocked_manual_review"`
and `manualSuggestion` is non-empty; it must not render for other terminal
states or expose operational evidence payloads.

### 4. Validation & Error Matrix

| Condition | Result |
|---|---|
| Viewer / non-member invokes mutation | `403` forbidden; review still readable |
| Unknown project / incident / series | `404` not found |
| Stale generation or expected predecessor/version | Stable `409` conflict; no attempt created |
| Active attempt exists | Stable `409` conflict; no attempt created |
| Latest state not in manual allowlist | Stable `409` unsupported-continuation; no attempt created |
| Duplicate identical click | Idempotent or stable conflict; exactly one next attempt |
| Automatic gate expected race (stale/active/ceiling) | Accepted no-op after webhook `202`; auditable safe skip reason |
| Storage/control-plane failure in gate | Error to the existing safe background failure reporter |
| Latest same-context durable `code_fixable` checkpoint in the continuation chain | Resume planning with repository-only tools for manual continuation |
| Newer context or no same-context durable `code_fixable` checkpoint | Restart model diagnosis over persisted evidence; no external evidence/source tools or discovery |
| `insufficient_evidence` diagnosis has no collection tool calls | Persist one decision and immediately stop at `blocked_manual_review`; do not consume empty collection loops |
| `stop` envelope without non-empty `recommendedNextAction` | Bounded `required_stop_suggestion` correction fed back to the model; never terminalize directly |
| Legal `stop` envelope | Persist an `unsafe_to_automate` decision with the handoff suggestion, then `blocked_manual_review` |
| Review without any decision / empty suggestion | Top-level `manualSuggestion` is `""`; page omits the suggestion block |
| Persisted evidence contains `DetailUrl`, password/token-shaped values, or provider fields | Preserve stored fields and values in trusted model context; do not expose them through page/log DTOs |

### 5. Good/Base/Bad Cases

- Good: repeated open-fingerprint webhook with committed new evidence → gate
  creates one linked retryable continuation; page manual continue creates
  attempt N+1 and analyzes the current persisted snapshot without refreshing it.
- Good: a manual child receives original persisted evidence values, exposes only
  repository read tools, and performs no dynamic discovery or connector call.
- Good: the latest same-context `code_fixable` checkpoint may be several attempts
  behind a polluted direct predecessor; the child enters planning from that
  checkpoint with repository tools and no provider/log recollection.
- Base: repeated root webhook / duplicate delivery → exactly one queued root;
  subsequent deliveries no-op.
- Bad: webhook payload used as retry authorization or credentials; retry of
  `diagnosis_ready_for_review` / `completed_non_code`; continuation brief
  containing raw provider messages or unrestricted tool output; restarting
  diagnosis after a durable `code_fixable` decision and losing the established
  fault because the newest alert detail is sparse.

### 6. Tests Required

- Domain: eligibility matrix, typed error categories, bounded metadata.
- PostgreSQL (integration): concurrent creators → one child; monotonic
  numbering; automatic ceiling; stale version; rollback; old-row defaults.
- Coordinator: continuation brief reaches the next turn; fresh budget; a
  same-context chain whose earlier attempt has the latest valid `code_fixable`
  checkpoint transitions directly to planning despite later insufficient
  diagnoses; a newer context restarts diagnosis; tool-less insufficient evidence
  terminalizes after one model turn; every manual continuation advertises
  repository-only tools, performs no Tencent/Docker/SSH/evidence/dynamic
  discovery call, and preserves persisted evidence values in model context;
  automatic continuation can analyze newly committed webhook evidence;
  predecessor rows never reassigned.
- Trigger: root idempotency, active/usable/non-retryable/unchanged-context
  skips, concurrency, ceiling.
- HTTP/API/E2E: authorization, stale generation/version, duplicate clicks,
  response metadata, capability-gated UI, no-secret rendering.

### 7. Wrong vs Correct

#### Wrong

```go
// Mutating a terminal run or replaying raw provider history as a "retry".
run.State = domain.RunStateQueued
```

#### Correct

```go
// New immutable linked attempt with a bounded structured brief. The coordinator
// resolves the latest same-context durable code_fixable checkpoint at or before
// latest before deciding whether to resume planning.
next, err := store.CreateNextAttempt(ctx, domain.NextAttempt{
    SeriesID:             latest.SeriesID,
    IncidentID:           latest.IncidentID,
    LifecycleGeneration:  latest.LifecycleGeneration,
    DeployedCommit:       latest.DeployedCommit,
    ContextVersion:       currentVersion,
    ExpectedPreviousRunID: latest.RunID,
    ExpectedPreviousVersion: latest.Version,
    Origin:               domain.OriginManualContinue,
    Reason:               "operator continue after deploy",
})
```

## Common Mistakes

- Do not treat `FIXTHE_ENCRYPTION_KEY` as the OpenAI key. It only unwraps
  `project_secrets`.
- Do not add a new secret kind for the pilot model. Store the key as
  `http_bearer` and reference it from `project_llm_providers`.
- Do not import `projects/adapter/git` from remediation application. Copy the
  temp-key / `GIT_SSH_COMMAND` pattern into the remediation adapter, or reuse
  `AuthenticatedHTTPSRemote`.
- Do not persist authenticated remotes. Rewrite `origin` to the public URL
  after clone.
- Do not hash evidence IDs with the current tail index. Sliding `tail`
  windows would break `GetContext`.
- Do not reject validated SSH source JSON because `projectFolder` is present.
- Do not pass the model command string to SSH. Regex allowlists on the raw
  string, as in typical SSH MCP servers, are bypassable with pipes, `$(...)`,
  and `;`. Parse, then reconstruct argv.
- Do not auto-tail configured `logPath`. It is a hint; the current file name is
  not guaranteed.
- Git/SSH dependency events remain metadata/public-identity only; source and
  evidence payloads belong exclusively to the remediation DEBUG observer after
  complete redaction and truncation. Never log PEM material or API keys.
  Collapse Git/SSH failures to stable errors.
- Do not make remote reads a prerequisite for `preparing_context`; the model
  must be able to choose a bounded tool after the run enters `diagnosing`.
- Do not assume an interface containing a nil optional pointer is absent-free;
  check optional MCP annotations before object validation.
- Do not let the model emit semantic confidence labels when the diagnosis
  contract is numeric. Use a bounded `invalid_confidence` correction for a type
  mismatch instead of copying decoder text or silently mapping an untrusted
  label to a score.
- Tencent CLS short detail URLs may redirect to a regional
  `region-monitor.cls.tencentcs.com` host. Accept only the strict regional
  label shape in the trusted URL validator; never replace it with an arbitrary
  `*.tencentcs.com` wildcard.
- Do not let a Tencent CLS remediation run proceed on the normalized alert or a
  failed connector observation alone. Require successful trusted detail content.
  `DetailUrl` is never accepted as a model-supplied tool parameter and never
  enters operator logs, but if it is part of a persisted operational evidence
  payload it remains unchanged in the trusted model context.
- Do not JSON-marshal typed `[]byte` payloads directly into model context; this
  produces reversible base64 instead of redacted text.
- Do not scan decoded inspect argv for `*?[` after quotes are stripped. That
  rejects `grep '[0-9]+'` even though the quotes made it a literal. Keep quote
  vs unquoted glob state in the tokenizer.
- Do not infer retryability from free-form terminal error text. Only typed
  transient provider/runtime failures and model output exhaustion set
  `retryable`; budget, policy, authorization, configuration, and
  blocked-manual-review outcomes never do.
- Do not drive a transactionally created `queued` root attempt more than once:
  the root driver and continuation drivers must both claim the queued run
  optimistically, and a `queued` root is driven by the original `Start` path,
  never by `Continue`.
- Do not make page continuation an implicit evidence refresh. `manual_continue`
  analyzes the current persisted snapshot with repository-only tools; a refresh
  requires a separate explicit contract or a newer webhook context version.
- Do not put a continuation control next to `diagnosis_ready_for_review` or
  `completed_non_code` results; those states already carry a usable business
  result and are not rerun.
