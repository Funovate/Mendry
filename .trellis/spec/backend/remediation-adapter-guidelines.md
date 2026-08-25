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
    Commit    string // exact deployed commit; never HEAD
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

- Operate only at `ref.Commit`. Never substitute `HEAD` or the latest remote
  tip.
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
  shell, allowlists inspect binaries (`ls`, `cat`, `head`, `tail`, `grep`,
  `egrep`, `fgrep`, `find`, `stat`, `wc`, `file`, `readlink`, `realpath`,
  `pwd`, `date`, `uname`, `hostname`, `df`, `du`, `ps`, `journalctl`, `dmesg`,
  `id`, `env`, `printenv`), and reconstructs a quoted argv. SSH executes that
  reconstructed string after `cd -- <quoted projectFolder> &&`. Never `bash -c`
  the raw model string.
- At most three `|` segments. Adjacent unquoted `|` operators become `||` and
  are rejected before SSH; a quoted `'|'` remains a literal argument.
- Glob metacharacters `*?[` are rejected only when unquoted and unescaped.
  After quotes are stripped, `grep '[0-9]+'` and `grep 'foo*'` are literals.
  Unquoted `ls *.log` is `invalid_arguments`. Do not scan decoded argv for glob
  characters after quote removal.
- Reject before SSH: `;`, `&&`, `||`, `$()`, backticks, redirections, env
  assignments, `sudo`, newlines, relative `..`, `find -exec/-delete/-ok`, and
  `journalctl` follow/vacuum flags.
- Timeout 15s. Combined stdout+stderr cap 64KiB; overflow truncates,
  `truncated=true`, aborts the process, and still returns the captured prefix.
- Inspect success payload is `{command, exitCode, stdout, stderr, truncated}`.
  No evidence-line IDs. Model context keeps bounded raw stdout/stderr except
  PEM private-key blocks and `fixthe-ssh*` temp key paths. Tokens/`sk-` values
  are intentionally not redacted.
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
  contract.
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
- SSH sources advertise only `ssh.inspect`. The gateway tokenizes the model
  command without a shell, allowlists inspect binaries, and reconstructs a
  quoted argv. Adjacent unquoted `|` operators become `||` and are rejected
  before SSH; a quoted `'|'` remains a literal argument.
- Glob metacharacters `*?[` are rejected only when they appear unquoted and
  unescaped. After quotes are stripped, `grep '[0-9]+'` and `grep 'foo*'` are
  literal arguments. Unquoted `ls *.log` remains a policy rejection. Do not
  scan the decoded argv text for glob characters after quote removal.
- Inspect stdout/stderr stay bounded raw in model context. Skip secret-pattern
  redaction there, but still strip PEM private-key blocks and `fixthe-ssh*`
  temp key paths. Operator logs omit private-key bytes, temp key paths, and
  `-i` argv.

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
| Invalid diagnosis/plan envelope or mixed native tool+content | Record bounded `invalid_envelope` observation; stay in the phase loop and count the turn against the model budget |
| Diagnosis `confidence` is not a JSON number | Record bounded `invalid_confidence` with path `diagnosis.confidence` and expected type `number`; include numeric `0..1` guidance without decoder internals; stay in the phase loop |
| Known `evidenceRef` citation field | Strictly reject the citation, retain the operator diagnostic, and send only bounded `evidenceCitations[].evidenceRef` → `evidenceId` guidance; never accept it as an alias |
| First or second consecutive invalid envelope in a phase | Account the model effect and append one allowlisted protocol correction for the next turn |
| Third consecutive invalid envelope in a phase | Account the model effect, append no further correction, and transition to `blocked_manual_review` unless an independent run budget exhausted first |
| Phase-valid envelope | Reset that phase's consecutive protocol-failure count; planning owns a separate counter from diagnosing |
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
- Diagnosis protocol: prompt and system instructions require numeric
  `confidence`; a string label produces a safe `invalid_confidence` correction,
  and a following valid numeric diagnosis completes the bounded retry path.
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
  unquoted `ls *.log` and `ls &&` / `ls ||` remain rejected; inspect payloads
  keep raw token-like text while still stripping PEM and temp key paths.

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
  a prefix; inspect payloads keep raw token-like text while stripping PEM and
  temp key paths; completion logs retain reconstructed command and secret
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

For diagnosis confidence, keep the wire type numeric and correct type errors
with an allowlisted protocol observation:

```json
// Wrong
{"confidence":"low"}

// Correct
{"confidence":0.2}
```

The application should tell the model which field and type to repair, but must
not forward the raw `json.UnmarshalTypeError` or provider response.

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
- Do not JSON-marshal typed `[]byte` payloads directly into model context; this
  produces reversible base64 instead of redacted text.
- Do not scan decoded inspect argv for `*?[` after quotes are stripped. That
  rejects `grep '[0-9]+'` even though the quotes made it a literal. Keep quote
  vs unquoted glob state in the tokenizer.
