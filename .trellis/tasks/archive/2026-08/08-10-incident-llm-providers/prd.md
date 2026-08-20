# Incident LLM Provider Integrations

## Goal

Enable project-opted-in AI diagnosis through interchangeable Claude, OpenAI,
and OpenAI-compatible providers without weakening the evidence or security
boundaries.

## Parent References

`08-10-production-incident-mvp/prd.md`, `design.md`, and `implement.md`.

## Dependencies

Requires `08-10-incident-service-foundation` for the typed Go `LLMProvider`
port, encrypted secret storage, and authorization. Requires
`08-10-incident-evidence-notifications` for redaction, evidence provenance,
report contracts, and the fake-provider boundary. It must not alter connector,
grouping, lifecycle, or background-job behavior.

## Requirements

- Implement provider adapters for Anthropic Claude, OpenAI, and explicitly
  configured OpenAI-compatible HTTPS endpoints behind the shared Go interface.
- Normalize a redacted provider-neutral inference turn to a strict local result
  envelope. Supported results include facts or hypotheses with evidence IDs,
  missing-evidence and verification items, remediation proposals, constrained
  follow-up collection descriptors, and bounded typed tool requests used by the
  remediation harness. Provider adapters transport those envelopes but do not
  execute tools or own the agent loop.
- Store every provider credential using the encrypted secret service. Project
  configuration chooses an enabled provider/model and explicitly grants LLM
  opt-in; the API returns metadata only. Provider configurations and encrypted
  API-key references are instance-scoped and may be selected by multiple
  projects without duplicating plaintext keys.
- Enforce a provider allowlist, request timeout, bounded retry policy, response
  size limit, and error classification. No provider may receive connector
  credentials, raw unredacted evidence, arbitrary tools, or unrestricted URLs.
- Permit outbound LLM calls only from the worker, using either direct HTTPS to
  an administrator-allowlisted provider endpoint or an administrator-configured
  HTTPS proxy. The API and console must have no LLM egress path; provider IPs
  are not pinned because they may be CDN-backed. Diagnosis runs through the
  shared RabbitMQ job runtime; provider-transport retries and job retries share
  one bounded attempt budget to prevent multiplicative retry storms.
- Persist audit metadata for provider, model, request time, evidence IDs,
  outcome, and available usage metadata. Do not persist raw provider requests
  or raw responses; retain only the prompt template version/content hash and
  validated structured conclusion included in the report. Provider failure
  remains non-fatal.

## Out Of Scope

- Chat UI, arbitrary prompting, provider-specific agent tools, model training,
  and automatic production actions.
- Local model hosting, provider billing reconciliation, and bypassing project
  opt-in or redaction.

## Disposition

Archived 2026-08-20. Per-project OpenAI-compatible configuration and the frozen `LLMProviderPort` shipped with the remediation harness. The Claude/instance-scoped/worker-only/RabbitMQ diagnosis contract is expired.

## Acceptance Criteria

- [ ] Contract tests run the same redacted diagnosis request through local HTTP
      fakes for Claude, OpenAI, and an OpenAI-compatible endpoint, producing
      the same validated local result contract.
- [ ] The same provider-neutral harness turn can return a validated typed tool
      request through every adapter without granting the adapter or model a
      credential, direct tool client, or loop ownership.
- [ ] Missing configuration, timeouts, malformed responses, rate limits, and
      provider outages produce audited non-fatal report limitations.
- [ ] Attempts to send unredacted values, connector secrets, unallowlisted
      endpoints, or unsupported follow-up descriptors are rejected before an
      outbound request or connector invocation.
- [ ] Tests prove that only the worker can use direct or configured-proxy HTTPS
      egress, and that provider requests retain TLS verification, size limits,
      timeouts, bounded retries, and idempotent handling after RabbitMQ
      redelivery.
- [ ] Provider/model credentials and configuration are authorization-protected,
      encrypted at rest, and never returned in plaintext.
- [ ] Instance-scoped provider configurations can be selected independently by
      multiple projects, while each project retains its own opt-in and model
      selection without storing a duplicate API key.
- [ ] Project provider selection and model opt-in are available to the console
      through metadata-only APIs.
- [ ] Audit and report records contain only evidence IDs, prompt template
      version/content hash, provider/model, time, available usage metadata, and
      validated structured conclusions; raw provider payloads are not stored.
