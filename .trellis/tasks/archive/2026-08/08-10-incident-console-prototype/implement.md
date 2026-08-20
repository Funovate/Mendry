# Implementation Plan: Incident Console Prototype

## Ordered Checklist

- [x] Create a standalone React/TypeScript static prototype with local fixture
      data and no external runtime dependency.
- [x] Implement the operational shell, project/environment context, sidebar,
      responsive mobile navigation, and role switch.
- [x] Implement the incident list with filters, priorities, lifecycle states,
      notification state, and representative trends.
- [x] Implement incident detail overview, evidence provenance, report, and
      activity views using the language-validator sample incident.
- [x] Add a remediation tab showing automated production-baseline resolution,
      hotfix branch, patch/test, draft PR, and human-only merge approval state.
- [x] Implement event stream, source metadata, notification policy, provider
      metadata, and audit views with role-restricted states.
- [x] Add accessible labels/tooltips to icon-only controls and verify keyboard
      navigation for principal controls.
- [x] Capture updated desktop and mobile screenshots, check remediation click
      paths, and verify no
      browser request is made to a customer or LLM endpoint.
- [x] Add a guided project configuration flow for repository, production
      branch, deployed baseline, and evidence scope using local validation
      states and safe default choices.
- [x] Add an inspectable raw-evidence drawer with redacted log content,
      source query, integrity metadata, and diagnosis citations.
- [x] Expand remediation review with cited reasoning, alternative treatments,
      patch diff, test command/result, and a human-only merge explanation.
- [x] Extend desktop/mobile browser checks and screenshots for the expanded
      flows, including the local-only network assertion.
- [x] Add generic SCM provider selection and a write-only credential setup
      step to the project configuration flow, with no secret value rendering.
- [x] Extend configuration browser coverage for credential saving, provider
      selection, and secret non-disclosure.
- [x] Add a local-fixture evidence-source catalog and combined trigger-policy
      configuration for cloud API/MCP, local SSH, webhooks, monitoring, and
      repository health alerts.
- [x] Replace the create-project shortcut with a complete six-step wizard for
      identity, repository, write-only credentials, immutable baseline,
      evidence scope, final review, and local creation confirmation.
- [x] Add desktop and mobile browser coverage for the create-project flow,
      including secret non-disclosure, required validation gates, responsive
      layout, and the local-only network assertion.
- [x] Replace fixed starter bundles with a Git-required composition model that
      supports any combination of SSH logs, cloud logs, MCP, signed webhooks,
      and custom trigger rules in both create and edit flows.

## Validation

- Browser checks cover navigation from incident list to detail/evidence/report,
  filters, role restrictions, settings metadata, and mobile menu behavior.
- Desktop and mobile screenshots demonstrate legible hierarchy with no clipped
  or overlapping content.
- A network check proves all visible data comes from local fixtures.

## Rollback

- The prototype is an isolated static surface. Removing its deployment or
  reverting its assets has no production data, connector, credential, or
  infrastructure impact.
