# Incident Console And REST API

## Goal

Give authorized operators a secure internal interface for reviewing,
configuring, and recording the outcome of production incidents.

## Parent References

`08-10-production-incident-mvp/prd.md`, `design.md`, and `implement.md`.

## Dependencies

Requires `08-10-incident-console-prototype`,
`08-10-incident-service-foundation`,
`08-10-incident-domain-lifecycle`, `08-10-incident-ingestion-cls`, and
`08-10-incident-evidence-notifications`, and
`08-10-incident-llm-providers`. It consumes their APIs/contracts and does not
duplicate their authorization or mutation rules.

## Requirements

- Provide protected REST endpoints and an internal web console for login,
  incident list/detail/event stream, evidence inspection, outcomes, source and
  notification policy configuration, and audit viewing.
- Display facts, hypotheses, source provenance, error samples, trend, alert
  history when available, and notification status distinctly.
- Allow operators to record root cause, known issue, false positive, or manual
  action. Only authorized roles may configure connectors, secrets, policies, or
  LLM opt-in.
- Let administrators select configured provider/model metadata for a project
  without exposing the underlying provider API key.
- Never display secret values or introduce production write controls.

## Disposition

Archived 2026-08-20. The backend-aligned console shell shipped through child `08-13-frontend-backend-aligned-scaffold`. Remaining review surfaces (outcomes, evidence/report display, notification-policy UI) are out of this expired parent contract.

## Acceptance Criteria

- [x] Protected console shell, login, project-scoped incident/event/audit/configuration APIs, and secret non-display shipped via `08-13`.
- [ ] Browser tests cover administrator, operator, and viewer workflows and
      verify forbidden controls/data cannot be accessed through the UI or API.
- [ ] An operator can inspect an incident and its evidence, record an outcome,
      and see the corresponding audit event.
- [ ] An administrator can manage connector/policy metadata without retrieving
      secret plaintext.
- [ ] Incident/event lists preserve all successfully ingested occurrences and
      accurately expose lifecycle and notification state.
