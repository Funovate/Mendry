# Implementation Plan: Incident LLM Provider Integrations

## Ordered Checklist

- [ ] Finalize the provider-neutral inference-turn/result-envelope schema and
      validation rules with the evidence/report and remediation-harness
      contracts, including typed tool requests without tool execution.
- [ ] Add instance-scoped provider configuration and encrypted-secret references
      with admin authorization, project-level provider/model selection and
      opt-in, endpoint allowlisting, direct/proxy egress configuration, and
      audit events.
- [ ] Implement the Claude adapter using the shared Go port and local result
      schema.
- [ ] Implement the OpenAI adapter using the same port and local result schema.
- [ ] Implement the explicitly configured OpenAI-compatible adapter with a
      documented supported subset; do not assume arbitrary gateway parity.
- [ ] Add bounded timeouts, classified retries, size limits, usage metadata,
      non-fatal failure handling, TLS verification, and worker-only egress.
- [ ] Persist only the approved audit/report metadata: evidence IDs, template
      version/content hash, provider/model, time, available usage metadata, and
      validated structured conclusion. Do not persist raw provider payloads.
- [ ] Add HTTP fake contract tests, redaction/secret non-disclosure tests, and
      negative tests for invalid result and follow-up descriptors.
- [ ] Expose metadata-only provider selection to the console/API contracts.

## Validation

- The same fixture produces one validated local result through every adapter.
- No outbound request is made without project opt-in, a configured provider,
  a permitted endpoint, redacted input, a valid secret reference, and direct
  or configured-proxy HTTPS egress from the worker.
- A provider outage records a report limitation and leaves deterministic facts,
  evidence, and incident lifecycle behavior intact.
- Tests prove raw provider request and response bodies never enter persistent
  audit or report storage.

## Rollback

- Disable a provider or remove it from a project's selection to stop new model
  requests immediately.
- Preserve prior reports and audit metadata; never delete evidence or incident
  history as part of a provider rollback.
