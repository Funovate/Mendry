# Technical Design: Remediation Evidence Thresholds

## Design Basis

This design adds a service-owned evidence gate around the existing remediation
agent loop. The model may classify, correlate, and explain evidence, but it does
not decide whether weak evidence is sufficient to enter fix planning.

The design preserves these project boundaries:

- project configuration and credentials remain owned by `projects`;
- public callbacks enter through `hooks` and are scoped by the inbound token;
- provider and SSH/Docker I/O stays in trusted adapters;
- the remediation coordinator remains the only run-state owner;
- the Tool Gateway remains the only model-requested execution boundary;
- operational evidence is complete for authorized operators and the harness,
  while credentials and authority-bearing tokens are structurally excluded;
- existing run, tool, elapsed-time, repository-byte, and evidence-byte budgets
  remain the outer collection bound.

This is a cross-layer change: webhook configuration and validation, Tencent CLS
detail retrieval, incident evidence persistence, remediation bootstrap and
gating, SSH/Docker evidence, and operator UI must agree on the same contracts.

## Decisions

### D1. Version Provider And Deployment Configuration

Signed-webhook configuration gains a provider discriminator:

```json
{
  "schemaVersion": 2,
  "provider": "generic|tencent_cls"
}
```

Existing version-1 signed webhooks decode as `generic`; the next explicit save
writes version 2. Provider behavior is never inferred from callback text. A
Tencent configuration does not ask the operator for `TopicId`: that value is
provider evidence, not project configuration.

SSH source configuration gains deployment identity while retaining one host:

```json
{
  "schemaVersion": 2,
  "host": "...",
  "port": 22,
  "user": "...",
  "projectFolder": "...",
  "logPath": "...",
  "mode": "tail|snapshot",
  "deployment": {
    "kind": "host|docker",
    "containerName": "required only for docker"
  }
}
```

Version-1 SSH sources decode as `deployment.kind=host`. The durable Docker
selector is the exact container name, not the discovery-time container ID.
Multiple SSH hosts, host pools, and callback-directed SSH targets remain out of
scope.

Configuration writes continue using strict connector-specific decoding.
Provider/deployment changes use the existing project audit path and include
only safe summary metadata.

### D2. Make Tencent Ingress A Typed Opt-In Path

`WebhookIngress` includes the saved webhook provider. The HTTP handler resolves
the token and provider before accepting the body.

- `generic` keeps the existing bounded UTF-8 behavior.
- `tencent_cls` requires `application/json`, one JSON object, a bounded non-empty
  `TopicId`, and an HTTPS `DetailUrl` on the expected Tencent alarm host.
  `UIN`, `Alarm`, `Topic`, `Condition`, and `TriggerParams` are preserved when
  present but are not synchronous acceptance requirements.
- malformed JSON, missing TopicId/DetailUrl, or invalid DetailUrl returns a
  stable `400 invalid_tencent_cls_callback` before background processing.
- a structurally valid callback returns the existing `202 {accepted:true}`.

The accepted aggregate callback becomes a normalized alert with quality
`anchor_only`. `Condition` and `TriggerParams` are occurrence metadata and
cannot become direct fault evidence. `TopicId` is retained unchanged as
provider identity but does not enter the semantic incident fingerprint.

### D3. Resolve DetailUrl Through A Trusted Tencent Adapter

The model never receives URL-fetch authority. A dedicated adapter owns the
verified Tencent frontend flow:

1. accept only the configured Tencent callback provider and expected HTTPS
   DetailUrl host;
2. follow only the bounded known Tencent redirect shape;
3. extract the record identity;
4. call the fixed `POST /cls_no_login?action=GetAlertDetail` action with a
   typed `RecordId` request;
5. enforce redirect count, timeout, content type, response size, and strict
   structural validation;
6. return typed operational evidence plus a separately classified connector
   observation.

The adapter projects response sections by authority class, not by log-content
allowlisting:

- operational evidence includes alert/fire times, epochs, region, account,
  logset/topic identity, query/query interval, result counts, complete
  `AnalysisInfo` items, configuration, columns, raw/formatted results, and
  per-item errors;
- credential/control material, including plaintext secrets, webhook tokens,
  and capability-bearing URLs, is never returned by the adapter port;
- log records inside accepted evidence sections retain every original field and
  complete content.

Callback `TopicId` and detail topic identity are compared when both are
present. Disagreement is persisted as contradictory connector evidence, not a
request for the model to guess from `Topic` text.

The anonymous endpoint is treated as a best-effort frontend contract. Redirect
drift, provider unavailability, invalid schema, or size/time failure occurs
after the 202 and becomes a safe connector observation. It does not fail the
ingress request or the remediation runtime. A future official CLS connector
can implement the same evidence port.

### D4. Persist Evidence Separately From The Observation Envelope

The raw webhook body remains the triggering Observation message. Complete
provider analysis and runtime evidence require a separate project/incident-
scoped evidence record because Observation attributes are intentionally small
and scalar.

An additive evidence record contains:

- project, environment, source, incident, triggering Observation, and run IDs
  as applicable;
- provider/kind, content version, provider occurrence/query times, ingest time,
  content hash, byte count, and collection outcome;
- complete typed operational payload or a safe connector error;
- evidence classification: `direct_fault`, `correlated_supporting`,
  `contextual`, `unrelated`, or `contradictory`;
- provenance and source-coverage identity.

Content is bounded at the record and aggregate run levels, but it is not
content-redacted. Project authorization protects operator reads. Ordinary logs,
audits, and notifications contain only IDs, hashes, byte counts, outcomes, and
safe error classes.

The remediation bootstrap loads the normalized triggering alert and bounded
evidence references before the first diagnosis turn. It includes the original
summary/symptom, provider fields, service/host/container/request identifiers,
event and ingest times, query interval, source-quality flags, and complete
available operational evidence within the existing model-context budget.

### D5. Preserve Raw Time Evidence And Apply One Global Reasoning Policy

Adapters do not rewrite provider time strings into provider-specific configured
zones. Evidence carries the original display strings, explicit offsets, epoch
values, query range, and log time fields together.

The global harness prompt requires this comparison order:

1. paired epoch;
2. explicit timestamp offset;
3. time-zone context visible in the source/system evidence;
4. unresolved, low-certainty time.

The diagnosis returns a structured time assessment containing original values,
the interpreted normalized instants/ranges, basis, certainty, and conflicts.
String/epoch disagreement is contradictory evidence. The representative
Tencent case must interpret the alert epoch as `07:44:32.007 UTC` and the log
epoch as `07:37:07.956 UTC` inside the `07:28:30–07:43:30 UTC` provider query
range without inventing an eight-hour mismatch.

### D6. Track Source Coverage Before Judging Causality

Each expected source records one of:

```text
configured -> inspected_success|inspected_empty
           -> unavailable(reason)
           -> not_applicable(reason)
           -> not_inspected(reason)
```

For SSH sources the configured deployment determines the primary runtime
source:

- `host`: application files and applicable service-manager/process evidence;
- `docker`: the selected container's stdout/stderr, with application files and
  host/service/kernel evidence as supporting sources when relevant.

A one-row CLS original-log analysis is a direct event anchor, but its source
coverage records that surrounding context is incomplete. Follow-up uses the
provider query interval and exact matched epoch. Missing stacks, request IDs,
or structured fields lower quality; frequent warnings cannot substitute for
the triggering fault.

An alert/detail IP may corroborate the sole configured SSH host but never
creates a target. If it cannot be reconciled, inspection may continue only on
the configured host and coverage records `host_identity_unverified`. Evidence
from that host needs an independent time/signature/request correlation bridge
before it is operationally correlated.

### D7. Discover And Read Docker Through Narrow Typed Operations

Docker is not added as a generic `ssh.inspect` binary. Two narrow trusted paths
prevent command-shape expansion.

Configuration probe:

```text
POST /api/v1/projects/{projectKey}/configuration/source/ssh/containers
```

The admin-only probe accepts the current draft host/port/user and a same-
project SSH credential reference. The backend executes a fixed, read-only
container inventory operation through SSH. It returns at most 100 entries with
exact name, ephemeral ID, image, and state/status. Running entries sort first;
stopped and restarting entries remain visible. Credentials, arbitrary command
text, and raw Docker output never reach the browser.

Runtime operations use the saved SSH source and selected container name:

- resolve the exact name to the current container ID and bounded identity
  metadata;
- read only stdout/stderr with mandatory `--since`, `--until`, and `--tail`;
- cap the requested interval relative to the incident/provider window, line
  count, bytes, and execution time;
- return empty/unavailable/ambiguous outcomes as source coverage.

The model-visible Docker log tool does not accept a container parameter. The
gateway injects the configured name, validates the requested time window and
tail bound, and calls a credential-free Docker evidence port. `docker exec`,
attach, copy, lifecycle actions, and every mutation have no registered route
and are rejected before SSH.

If the configured name disappears after recreation, the adapter reports the
primary source unavailable; it never selects a similarly named container or
sweeps all containers.

### D8. Make The Evidence Gate Service-Owned

The diagnosis envelope gains structured quality input/output rather than
relying on free-form `missingEvidence` strings:

- normalized alert quality and incident window;
- evidence citations with classification;
- source coverage;
- time assessment;
- host/service/container/request correlation;
- causal-closure assertion and explanation;
- material contradictions;
- `test_suspected` and any structured test-policy match;
- non-actionable hypotheses.

After strict envelope decoding, an application-level gate independently
validates cited persisted evidence and computes the effective confidence cap.

```text
missing direct fault evidence                         -> cap 0.39
unresolved host/time/primary source/material conflict -> cap 0.69
otherwise                                             -> cap 1.00
effective confidence = min(model confidence, cap)
```

Planning eligibility requires all of:

- effective confidence at least `0.70`;
- at least one cited direct fault record;
- temporal and operational correlation to the original symptom;
- primary source inspected or an auditable direct bridge that closes its gap;
- no unresolved material contradiction;
- causal closure explaining the original fault signal;
- final fixability `code_fixable`.

Failure of any gate overrides model `code_fixable` to the business outcome
`insufficient_evidence`. The coordinator does not enter planning. The existing
manual-review terminal lifecycle may carry that outcome, but the review and
notification surface must display `insufficient_evidence`, never a plan-ready
state.

An insufficient result may retain bounded hypotheses only when each is marked
`non_actionable`. They cannot supply evidence refs to a repair plan.

Test-like text only records `test_suspected`. Full diagnosis continues when
real fault evidence exists. Automatic remediation suppression requires an
explicit structured test flag, configured non-production/test environment,
known test source/rule, or another auditable policy match.

### D9. Expose Configuration And Evidence Decisions To Operators

The Trigger step uses an explicit provider choice after `Signed webhook`:

- `Generic webhook`;
- `Tencent Cloud CLS alert`.

The Tencent panel explains the callback fields and detail/multidimensional
retrieval behavior. It does not ask for TopicId or time-zone settings. Review
and overview surfaces render `Signed webhook · Tencent Cloud CLS`.

The SSH Source step uses a deployment-mode control (`Host process` / `Docker`).
Docker mode enables a refresh action and a native select populated by the
backend probe. The option label includes container name, image, and status;
there is no free-form fallback. Saving is blocked until a returned container is
selected. Review and overview surfaces show deployment mode and container name.

The remediation review shows original alert, time interpretation, source
coverage, evidence classifications/citations, contradictions, applied
confidence cap, gate outcome, `test_suspected`, non-actionable hypotheses, and
instrumentation/manual collection recommendations. Complete operational
evidence is project-authorized; credential/control material is absent by
construction.

## Runtime Flow

```text
Tencent callback
  -> token + provider lookup
  -> synchronous Tencent envelope validation
  -> 202
  -> persist anchor Observation / incident occurrence
  -> trusted DetailUrl adapter
       -> success: persist provider evidence + per-item errors
       -> failure: persist connector-unavailable observation
  -> remediation bootstrap
       normalized alert + raw Observation ref + provider evidence + source config
  -> diagnosing tool loop
       CLS/context tools when configured
       SSH host tools when relevant
       typed Docker logs for saved Docker deployment
  -> model diagnosis
  -> service evidence gate + confidence cap
       pass -> planning -> review
       fail -> insufficient_evidence/manual review, no planning
```

## Compatibility And Rollout

- Decode signed-webhook v1 as generic and SSH-source v1 as host deployment.
- Do not fetch Tencent URLs for existing triggers after migration.
- Additive persistence migrations are applied before enabling provider/detail
  processing or the evidence gate.
- Gate enforcement can be feature-enabled after historical/current-run readers
  tolerate absent assessments. New runs always write the assessment once the
  gate is enabled.
- Detail connector failure is reversible by disabling Tencent provider
  processing; raw callbacks and generic webhook behavior remain available.
- Docker capability is enabled only for saved Docker deployments and has no
  generic command fallback.

## Failure Matrix

| Condition | Behavior |
|---|---|
| Malformed Tencent callback/missing required field | Stable synchronous 400; no background work |
| Valid callback, DetailUrl redirect/schema/provider failure | 202 remains; connector observation; bounded alternatives; usually insufficient evidence |
| TopicId differs between callback and detail | Persist contradiction; no model inference from Topic text |
| Only timestamp/query count is available | Anchor-only collection; no actionable diagnosis |
| Time values cannot be reconciled | Cap 0.69; surface uncertainty/contradiction |
| No direct fault evidence | Cap 0.39; insufficient evidence |
| SSH/Docker unavailable or permission denied | Source unavailable with reason; no bypass |
| Alert IP cannot be tied to configured host | `host_identity_unverified`; direct bridge required |
| Configured container name missing | Primary source unavailable; no guessed replacement |
| Model returns high-confidence code_fixable without gate evidence | Override to insufficient evidence; no plan candidates |
