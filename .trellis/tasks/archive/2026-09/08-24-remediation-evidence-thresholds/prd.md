# Define remediation evidence thresholds

## Goal

Prevent the remediation harness from turning weak, noisy, or temporally
misaligned observations into a high-confidence root cause. The harness must
degrade predictably when an alert contains only a timestamp, clocks use
different time zones, alert text is test-like or vague, or the monitored
system has incomplete logs.

## Background

- Incident ingest currently persists a normalized title, fingerprint, message,
  source identity, and occurrence timestamps, but it does not expose a typed
  incident-time zone, environment classification, expected runtime/log source,
  or alert quality score to remediation.
- `POST /hooks/{token}` currently accepts any non-empty UTF-8 `text/plain`,
  `application/json`, or `application/x-www-form-urlencoded` body. There is no
  required webhook alert schema. The route token resolves project/source
  identity, while arbitrary body content supplies all alert semantics.
- Webhook ingest currently sets Observation `occurredAt` and Incident
  `firstSeen`/`lastSeen` from Fixthe's UTC receive clock, not from a timestamp
  parsed out of the alert body. The raw body is retained as the Observation
  message, but inbound service, host, request ID, and attributes are empty and
  level is always `error`.
- The remediation bootstrap currently contains project, environment, source,
  deployed commit, connector status, and tool hints. It does not load the
  triggering Observation body, normalized incident title, original event time,
  service/host/container, request/trace ID, or error signature. The diagnosis
  agent can therefore search target logs without knowing which original symptom
  it must explain.
- The current Tencent CLS alert payload is an aggregate rule notification, not
  the matched log event. A representative production payload contains `UIN`,
  generic `Alarm`, human-readable `Topic`, stable `TopicId`, query-count
  `Condition`, aggregate `TriggerParams`, and an operator `DetailUrl`. It does
  not contain the matched log text, event timestamp, service instance,
  host/container, trace/request ID, or exception signature. For example,
  `Condition=[$1.__QUERYCOUNT__] > 0` and
  `TriggerParams=$1.__QUERYCOUNT__=1;` prove only that one record matched the
  configured CLS query.
- Current webhook normalization deliberately removes UINs, IDs, timestamps,
  and detail URLs from grouping inputs. This is appropriate for fingerprint
  stability, but those fields still need a separate evidence/connector role:
  `TopicId` identifies the provider log topic without becoming user
  configuration, and `DetailUrl` remains a trusted-adapter input rather than
  model-visible arbitrary-fetch authority.
- The representative `DetailUrl` has been verified to redirect to Tencent's
  anonymous CLS alert page. That page calls
  `POST /cls_no_login?action=GetAlertDetail` with a `RecordId` and returns a
  structured alert record containing alert/fire times, region, logset/topic,
  the configured query and query interval, result count, and the analyzed
  original matched log. In this incident the matched record contains the nil
  pointer trigger message, host, source address, file path, code path, and exact
  log timestamp that were absent from the webhook body.
- `GetAlertDetail` also returns configured multidimensional analysis content
  directly through `ResultsSnapshot.AnalysisInfo`, together with
  `AnalysisResultFormat`, `RawResults`, `ColNames`, `Columns`, and `QueryParams`.
  The representative alert currently contains one `original` analysis named
  `原始日志`, configured with `Fields=*`, query index 1, and limit 1. It returns
  the exact nil-pointer trigger record but not its surrounding stack/context.
- The representative Tencent detail mixes display time zones. Its zone-less
  alert display time `2026-08-24 15:44:32` corresponds to `FireTime`
  `2026-08-24 07:44:32.007 UTC`, while the analyzed log field `time` is
  `2026-08-24 07:37:07.956` and matches its epoch `__TIMESTAMP__` in UTC. The
  provider query epoch range is `2026-08-24 07:28:30–07:43:30 UTC`. The apparent
  eight-hour difference is a display-zone difference, not an eight-hour event
  mismatch.
- The same anonymous detail response also contains credential/control material
  that is not operational log evidence. Operational evidence is intentionally
  operator-facing and may retain its complete original content; credentials and
  control secrets remain a separate authority boundary. The no-login page/API
  is an observed Tencent frontend contract, not yet an assumed stable provider
  API contract.
- SSH projects provide `projectFolder` and `logPath` as discovery hints. The
  agent chooses bounded read-only commands; the harness does not currently
  require it to inspect container stdout/stderr or prove that the selected log
  source is authoritative for the failing process.
- The diagnosis contract supports `insufficient_evidence`, `missingEvidence`,
  contradictions, citations, and confidence, but only validates field shape and
  confidence range. It does not enforce a minimum direct-evidence threshold or
  cap confidence when the original symptom remains unexplained.
- In the observed `rr / real-estate` investigation, the agent read an
  application log file, followed repeated population fallback warnings into
  repository code, and missed a nil-pointer failure present in Docker logs.
  The resulting hypothesis did not explain the actual fault signal.
- Existing tool and run budgets remain the outer bound. Evidence-quality rules
  decide whether a diagnosis is supportable within that bound; they must not
  create unbounded collection.

## Requirements

### R1. Sparse alert input

- Treat alert fields as search anchors, not as proof of root cause.
- Define a typed normalized alert context independently of the accepted external
  webhook shape. It must distinguish event time from ingest time and preserve
  available service, environment, host/container, trace/request ID, error
  signature, original summary, and source-provided attributes.
- The triggering normalized alert context and a bounded reference to its raw
  Observation must reach remediation before the first diagnosis turn.
- When only an occurrence time is available, remediation must derive a bounded
  search window and inventory the configured runtime evidence sources before
  selecting files or queries.
- Absence of a service, host/container, endpoint, trace/request ID, exception,
  or symptom must lower input quality and be reported as missing evidence.
- A sparse alert may start bounded collection, but it may not by itself support
  an actionable root-cause diagnosis.
- Classify aggregate notifications such as the current Tencent payload as
  `anchor_only`: sufficient to open/group an incident and start bounded
  evidence retrieval, but insufficient to state the matched error or enter fix
  planning.
- Treat `TopicId` as the stable CLS log-topic identity separate from the
  semantic incident fingerprint. Preserve it from the callback as provider
  evidence and compare it with the resolved detail record when available, but
  do not require the operator to enter or select it in project configuration.
  A mismatch is contradictory connector evidence; it must not be resolved by
  asking the model to infer ownership from free-form `Topic` text. Use the
  detail record's alert ID when a stable alert-rule identity is required.
- Treat query-count conditions and trigger counts as occurrence metadata, not
  direct fault evidence.

### R2. Harness time reasoning and uncertainty

- Preserve original provider and log time fields without provider-specific time
  configuration or adapter-side rewriting. The raw alert/detail/log evidence,
  including display strings, explicit offsets, epochs, and query intervals,
  must reach the harness together.
- Apply one global harness instruction for every evidence provider: the AI must
  determine time-zone semantics before comparing event order or proximity,
  prefer paired epoch values when available, and state its interpretation and
  uncertainty in the diagnosis.
- The AI must evaluate time evidence in this order: paired epoch, explicit
  timestamp offset, contextual source/system time zone visible in evidence, then
  unresolved/low-certainty. This is a reasoning policy, not a personalized UI or
  per-provider time-zone setting.
- The diagnosis must cite the original time values and the AI's normalized
  comparison, and must not claim temporal correlation when it cannot reconcile
  the source clocks or when evidence falls outside the interpreted window.
- Conflicting string and epoch representations must be listed as contradictory
  time evidence rather than silently treated as separate incidents.

### R3. Vague and test-like alert text

- Strings such as `test alert`, `test message`, `测试告警`, or `测试信息` are
  low-information indicators, not sufficient proof that an event is harmless.
- A test/noise classification requires corroborating structured context such as
  an explicit test flag, configured non-production environment, known test
  source/rule, or matching maintenance/test window.
- In production, vague or test-like text must not suppress investigation when
  runtime evidence contains a real failure signal.
- When retrieved evidence contains a real correlated fault signal, continue the
  full diagnosis and record `test_suspected` as an auditable indicator. The
  indicator alone must not downgrade or terminate diagnosis and must not be
  treated as authorization to suppress remediation.
- Automatic remediation may be suppressed as test/noise only when a structured
  test flag, configured test environment, explicit test-source/rule mapping, or
  another auditable structured policy matches.
- Generic alert text must not be used as the sole repository-search query or
  diagnosis citation.

### R4. Incomplete monitored-system logs

- Evidence discovery must distinguish configured sources, sources actually
  inspected, unavailable sources, empty sources, and sources that cannot carry
  the relevant process output.
- For SSH targets, discovery should consider process/container stdout/stderr,
  service-manager logs, application files, and host/kernel evidence when those
  capabilities are available and policy-approved.
- SSH evidence collection may use a bounded read-only Docker capability for
  container discovery and log retrieval. It is limited to `docker ps`,
  time/line-bounded `docker logs` using `--since`, `--until`, and `--tail`, and
  the minimum read-only `docker inspect` fields needed to identify runtime/log
  ownership.
- Docker evidence is available only through an enabled, reachable SSH source
  whose remote user can execute the approved read-only Docker commands. Missing
  SSH configuration, connection failure, absent Docker CLI/daemon, or denied
  Docker socket access must be recorded as an unavailable evidence source; it
  must not be bypassed with a new direct connection.
- An IP or host field in alert/detail/log evidence identifies a candidate
  machine, not a container. It may corroborate the configured SSH target or a
  trusted project/source mapping, but it must not authorize SSH to an
  unconfigured address or be treated as a unique container selector.
- This scope retains one configured SSH host per project. Alert/detail IP
  fields are correlation evidence for that host only; they do not select among
  multiple SSH targets or create a new target at incident time.
- When an alert/detail IP cannot be reconciled with the single configured SSH
  host, bounded read-only inspection of that configured host may continue but
  the source coverage must record `host_identity_unverified`. Evidence from the
  host cannot satisfy operational correlation unless time, fault signature,
  request/trace identity, or another direct bridge ties it to the triggering
  observation; otherwise the result remains `insufficient_evidence`.
- `docker logs` may run only after one container is resolved through trusted
  configuration or correlated evidence such as an explicit container ID/name,
  a recognized Docker log path containing a container ID, or a unique match of
  configured service identity against bounded `docker ps`/read-only `inspect`
  metadata. Ambiguous matches remain missing evidence and must not cause a
  sweep across every container.
- The SSH source configuration UI must explicitly distinguish host-process and
  Docker deployment. Selecting Docker must not expose a free-form container
  name field: after host, port, user, and an SSH credential reference are
  available, an authorized backend probe reads the remote Docker inventory and
  returns bounded container identity metadata for a select control.
- The container selector must show at least the exact container name, image,
  and runtime status, support an explicit refresh, and include both running and
  stopped/restarting containers so a crashed workload remains selectable.
  Running containers sort first, the response is bounded, and the selected
  exact container name is persisted as project configuration. Raw SSH
  credentials and raw command execution remain backend-only.
- Runtime collection must resolve the persisted exact container name again on
  the configured SSH host before reading logs. The discovered container ID is
  runtime evidence only and must not be the durable selector because it changes
  when a container is recreated.
- Docker execution and mutation remain prohibited: no `docker exec`, attach,
  copy, start, stop, restart, kill, remove, update, or other container/image/
  network/volume state changes are exposed to the harness.
- Missing stacks, correlation IDs, or structured fields lower evidence quality;
  they do not authorize the model to substitute the most frequent warning as
  root cause.
- If direct evidence remains unavailable, return an inspectable degraded result
  with attempted sources, missing evidence, bounded candidate hypotheses, and
  recommended instrumentation or manual collection steps.
- For Tencent CLS notifications, the preferred next evidence step is a bounded
  read through a trusted CLS adapter for the detail record and records that
  caused the rule to fire. The adapter may resolve an allowlisted Tencent CLS
  `DetailUrl`, but the model must not receive arbitrary URL-fetch authority.
- Preserve every configured `AnalysisInfo` item and its original result shape as
  operator evidence. Analysis errors must remain attached to their owning item
  instead of invalidating successful dimensions.
- A provider analysis result limited to one matching row is a direct event
  anchor, not proof that surrounding causal context is complete. The harness may
  use the returned query interval and matched timestamp to request bounded
  context from the configured CLS/log connector.
- All configured multidimensional analysis items are accepted as initial
  evidence. When an original-log analysis is bounded to one row, the harness
  must use the provider's query interval and exact matched epoch for bounded
  follow-up context rather than inventing a separate generic time window.
- Detail ingestion must constrain the Tencent HTTPS host and expected redirect
  host/action, reject cross-provider redirects, enforce short time/body limits,
  and parse the structured response. It must preserve complete operational
  evidence content for authorized operators, including original log records,
  stacks, paths, hosts, source addresses, account/topic/logset identity, queries,
  time ranges, and provider metadata useful for investigation. This evidence is
  not content-redacted or restricted through a field allowlist.
- Credential-bearing and authority-bearing response material, including
  plaintext secrets and capability tokens, is not classified as log evidence.
  It must remain structurally isolated from persisted evidence, ordinary logs,
  UI evidence payloads, and external LLM context. This is credential isolation,
  not content redaction of operator evidence.
- Because the anonymous no-login endpoint is a frontend implementation detail,
  failure or schema drift must degrade to connector-unavailable and permit a
  supported official CLS API/configured connector fallback; it must not fail the
  remediation runtime or silently accept an unvalidated shape.
- Candidate hypotheses in a degraded result must be explicitly marked
  non-actionable. They may help a human continue investigation, but they do not
  raise the evidence grade, satisfy causal closure, or authorize fix planning.

### R5. Evidence gate and causal closure

- Separate observations into direct fault evidence, correlated supporting
  evidence, contextual evidence, and unrelated/contradictory evidence.
- An actionable diagnosis must explain the original fault signal and cite at
  least one temporally and operationally correlated direct fault observation.
- Repository code can explain a runtime observation but cannot replace one;
  code-only hypotheses remain non-actionable unless the incident itself is a
  static/configuration finding.
- Confidence must be capped by input quality, source coverage, temporal
  certainty, and causal closure. Model self-reported confidence alone is not
  authoritative.
- The service-owned confidence bands are `0.00–0.39` low, `0.40–0.69` medium,
  and `0.70–1.00` high. Only high confidence is numerically eligible for fix
  planning, and it remains ineligible unless every structural evidence gate
  also passes.
- Missing direct fault evidence forces a confidence cap of `0.39`. An
  unresolved host identity, unresolved time semantics, uninspected/unavailable
  primary runtime source, or unresolved material contradiction forces a cap of
  `0.69` unless another direct correlation bridge explicitly closes that gap.
- Values below `0.70` may support an operator-visible diagnosis or explicitly
  non-actionable hypotheses, but they must produce `insufficient_evidence` and
  must not transition to plan-candidate generation.
- Exhausting bounded collection without passing the evidence gate must lead to
  `insufficient_evidence` / manual review rather than a guessed fix plan.
- An `insufficient_evidence` result may persist bounded non-actionable candidate
  hypotheses, but the run must remain in the evidence-insufficient terminal
  outcome and must not transition to plan-candidate generation.

### R6. Auditability

- Persist the normalized incident window, evidence-source coverage, quality
  factors, gate decision, confidence cap, contradictions, and missing evidence.
- Operators must be able to see why a source was not inspected and why a
  diagnosis was accepted, downgraded, or rejected.
- Operational evidence remains complete and unredacted for authorized users and
  the harness; credential isolation and existing byte/time budgets still apply.

### R7. Explicit Tencent CLS webhook selection

- Provider-specific behavior must be opt-in configuration for a signed webhook;
  it must not be inferred from arbitrary payload text or applied to every
  webhook.
- The project configuration UI must ask for the webhook format/provider after
  `Signed webhook` is selected. At minimum it must offer `Generic webhook` and
  `Tencent Cloud CLS alert` as distinct choices using the existing form control
  conventions.
- Selecting Tencent Cloud CLS must visibly state that the generated inbound URL
  is intended for a Tencent CLS alarm notification callback and that Fixthe will
  resolve `DetailUrl`, ingest all multidimensional analysis results, and provide
  the complete alert/detail/log time fields and provider query interval to the
  harness for reasoning and follow-up evidence.
- The Tencent option must show the expected top-level callback fields (`UIN`,
  `Alarm`, `Topic`, `TopicId`, `Condition`, `TriggerParams`, and `DetailUrl`) and
  distinguish the minimal aggregate callback from the richer detail record that
  Fixthe retrieves.
- Configuration summaries and review screens must display `Signed webhook ·
  Tencent Cloud CLS` rather than a generic signed-webhook label.
- The signed-webhook configuration contract must persist a versioned provider
  discriminator. Existing configurations migrate/default to `generic`; changing
  provider must be explicit and auditable.
- Only the Tencent provider selection enables Tencent host/redirect validation,
  `GetAlertDetail` parsing, and multidimensional evidence extraction. The global
  harness time-reasoning policy applies to Tencent and generic evidence alike.
- For a trigger configured as Tencent Cloud CLS, ingress must synchronously
  require one valid JSON object, a non-empty `TopicId`, and a valid `DetailUrl`.
  Other expected top-level fields are preserved when present but do not block
  acceptance when absent. An invalid provider callback returns a stable 400
  configuration error and does not start background processing.
- After a structurally valid Tencent callback is accepted, a `DetailUrl` fetch,
  redirect, provider availability, or schema failure remains an asynchronous
  evidence-connector observation. The HTTP response remains 202 and the run
  degrades to `insufficient_evidence` when no alternative direct evidence exists.

## Acceptance Criteria

- [ ] A timestamp-only alert triggers bounded source discovery and ends as
      evidence-insufficient when no direct fault signal is found; it does not
      produce a high-confidence code/data root cause.
- [ ] The representative Tencent payload is classified as `anchor_only`; its
      query count cannot satisfy the direct-evidence gate.
- [ ] `TopicId` is retained as provider evidence without becoming a required
      user configuration field or part of the semantic incident fingerprint;
      callback/detail disagreement is visible as a contradiction, and
      `DetailUrl` is never fetched by the model as an arbitrary URL.
- [ ] The representative `DetailUrl` resolves through a trusted adapter to
      complete operator-visible evidence containing the actual nil-pointer
      match and provider-supplied query interval, without content redaction or
      a log-field allowlist.
- [ ] All configured Tencent multidimensional analysis items, result columns,
      raw results, formatted results, configuration, and per-item errors remain
      available as complete operator evidence.
- [ ] A one-row `original` analysis can anchor the fault but cannot by itself
      satisfy stack/context completeness; bounded follow-up collection remains
      possible around its exact timestamp.
- [ ] Plaintext credentials and capability tokens present beside the evidence
      remain structurally isolated and never enter persisted evidence, ordinary
      logs, UI evidence payloads, or external LLM context.
- [ ] Unexpected redirect hosts, response types, oversized bodies, missing
      record identity, and provider schema drift are rejected as safe connector
      observations rather than passed through to the model.
- [ ] A Tencent CLS connector can retrieve the bounded matched record set, or
      report the source as unavailable and leave the result
      `insufficient_evidence`.
- [ ] Remediation's first diagnosis turn receives the normalized triggering
      alert context and can state which original symptom collected evidence must
      explain; project/source/commit metadata alone is insufficient.
- [ ] An alert at UTC+8 correlates with UTC logs using the same normalized UTC
      instant, while preserving both original timestamps for audit.
- [ ] The representative Tencent alert display time `15:44:32` normalizes from
      its epoch to `07:44:32.007 UTC`, and its `07:37:07.956` analyzed log remains
      inside the provider's `07:28:30–07:43:30 UTC` query range; the harness does
      not treat their displayed clock difference as an event mismatch.
- [ ] Valid Tencent epoch fields take precedence over zone-less display strings,
      and any material disagreement is surfaced as contradictory time evidence.
- [ ] Raw provider and log time fields reach the harness unchanged; the AI
      records its time-zone interpretation, normalized comparison, and any
      unresolved uncertainty without requiring provider-specific time settings.
- [ ] A production alert titled `测试告警` is not suppressed solely by its text;
      a real correlated panic still drives the full investigation and records
      `test_suspected` without treating that marker as a remediation blocker.
- [ ] A configured test alert can be classified as test/noise only with a
      structured corroborating signal and an auditable reason.
- [ ] When an application file lacks the failure but container stderr contains
      a correlated nil-pointer stack, the container evidence is treated as the
      direct fault observation and unrelated warnings cannot become root cause.
- [ ] Docker evidence access is read-only and bounded: container inventory and
      identity plus time/line-bounded logs are permitted, while exec, attach,
      copy, lifecycle operations, and all Docker mutations are rejected before
      remote execution.
- [ ] Alert/detail IP evidence can correlate to an administrator-configured SSH
      target but cannot create a new SSH target or select a container by itself;
      ambiguous container ownership remains `insufficient_evidence`.
- [ ] An unreconciled alert IP does not redirect SSH access. Inspection may use
      only the configured host, records `host_identity_unverified`, and cannot
      become actionable without an independent direct correlation bridge.
- [ ] Selecting Docker deployment in the SSH source editor loads the remote
      container inventory through the stored SSH credential and requires the
      operator to choose a returned container; container names are not typed
      free-form, running and stopped/restarting states are distinguishable, and
      the selected exact name is shown in review/summary UI.
- [ ] A recreated container can be re-resolved by its persisted exact name
      without relying on the stale discovery-time container ID; a missing name
      is reported as unavailable evidence rather than replaced by a guessed
      container.
- [ ] When all accessible sources are incomplete, the persisted result lists
      attempted/unavailable sources, explicitly non-actionable candidate
      hypotheses, and instrumentation recommendations; its final classification
      remains `insufficient_evidence` and it does not enter fix planning.
- [ ] An actionable diagnosis cannot pass without causal closure to the
      original symptom and at least one direct evidence citation.
- [ ] A model response reporting `code_fixable` cannot enter planning below
      service-capped confidence `0.70`, and confidence `0.70` or above cannot
      bypass direct-evidence, temporal, operational-correlation, source-
      coverage, contradiction, or causal-closure gates.
- [ ] Missing direct fault evidence caps confidence at `0.39`; unresolved host,
      time, primary-source coverage, or material contradictions cap it at
      `0.69` unless an auditable direct bridge resolves the corresponding gap.
- [ ] Existing tool-call, elapsed-time, repository-byte, evidence-byte, and
      credential-isolation limits remain enforced without content-redacting
      operator evidence.
- [ ] The Trigger configuration presents Generic and Tencent Cloud CLS webhook
      choices, explains the Tencent-specific callback/detail behavior, and
      persists the selected provider.
- [ ] Existing signed webhooks remain `generic` after migration and do not
      begin fetching Tencent detail links without an administrator selecting the
      Tencent Cloud CLS option.
- [ ] Configuration summary/review surfaces identify the selected provider, and
      only Tencent-configured triggers activate the verified CLS detail path;
      time interpretation remains a global harness responsibility.
- [ ] A malformed Tencent callback or one missing its required `TopicId` or
      `DetailUrl` receives a stable 400 response before background processing,
      while a post-acceptance detail retrieval failure remains 202 and produces
      an inspectable connector observation / `insufficient_evidence` outcome.

## Out Of Scope

- Guaranteeing diagnosis when the monitored system emits no usable evidence.
- Automatically installing logging, tracing, agents, or collectors on the
  monitored system during an incident.
- Treating free-form alert wording alone as a trusted environment or test flag.
- Unbounded searches intended to compensate for missing timestamps or logs.
- Multiple SSH hosts per project, host pools, and dynamic SSH target selection
  from alert/detail IP addresses.
