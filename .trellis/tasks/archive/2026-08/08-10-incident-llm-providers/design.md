# Technical Design: Incident LLM Provider Integrations

## Provider Boundary

The provider child implements the existing Go `LLMProvider` port. The core
passes a redacted, bounded inference turn and a local result-envelope schema;
an adapter maps that contract to one provider transport and maps its response
back before the core validates it. An envelope may contain a diagnosis or a
typed tool request, but adapters never execute tools or own the agent loop.

```text
Evidence/harness turn -> redaction -> LLMProvider -> provider adapter
                                      |             |
                                      v             v
                           local result schema   HTTPS provider API
                                      |
                                      v
                    policy validation -> report/audit/collection proposal
```

Initial adapters are Claude, OpenAI, and OpenAI-compatible. The compatibility
adapter is not assumed to implement every OpenAI feature: it supports only the
configured, tested subset needed to return the local structured result.

## Configuration And Secrets

Each instance-scoped provider configuration contains a kind, metadata-only
model name, HTTPS endpoint where applicable, timeout, and encrypted API-key
reference. A project may select one configured provider/model only after an
administrator enables LLM opt-in. The execution path checks both project
opt-in and provider status for every request. Project records store only their
selection and never duplicate the API key.

## Egress Policy

Only the worker process has an LLM egress client. It uses direct HTTPS to an
administrator-allowlisted provider FQDN or an administrator-configured HTTPS
proxy. The API and console do not construct LLM clients. TLS certificate
verification remains enabled; endpoints, proxy settings, response sizes,
timeouts, and retry limits are validated before use. Provider IP addresses are
not pinned because CDN-backed provider infrastructure may change addresses.

## Failure, Audit, And Safety

Adapters perform bounded retries only for classified transient failures. They
never retry validation failures. Request/response sizes are bounded, results
are decoded into the local schema, and invalid output is treated as a non-fatal
diagnostic limitation. Audit records include provider/model metadata, evidence
item IDs, timestamps, outcome, available usage metadata, prompt template
version/content hash, and the validated structured conclusion included in the
report. Raw provider request/response bodies, API keys, and unredacted evidence
are never persisted.

No adapter executes a requested tool. The core separately validates any
follow-up descriptor against source capabilities and collection policy.
