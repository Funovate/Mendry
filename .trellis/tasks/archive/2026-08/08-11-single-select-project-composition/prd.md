# Single-select project composition configuration

## Goal

Make project setup easier to follow by requiring one log source and one
trigger mode, then showing only the configuration fields for those choices.
The flow should present a clear branch without asking an administrator to
configure integrations they did not choose.

## Confirmed Context

- The static prototype lives in `frontend/` and uses local React state and
  fixture data only.
- Git remains a required project foundation and is configured in its existing
  repository and credential steps.
- The current composition model stores `logSources` and `triggerModes` as
  arrays in `frontend/src/App.tsx` and renders every selected source and
  trigger configuration in the evidence step.
- The current browser coverage selects all three log sources and both trigger
  modes in `frontend/tests/prototype.spec.ts`; those assertions must change
  to cover mutually exclusive choices and conditional configuration.
- The existing source-specific panels already provide the needed fields for
  SSH logs, cloud logs, MCP, signed webhook, and custom rule.

## Requirements

- Treat log source selection as a single choice among SSH logs, Cloud logs,
  and MCP.
- Treat trigger mode selection as a single choice among Signed webhook and
  Custom rule.
- Keep Git required and preserve the existing repository, transport
  credential, immutable baseline, and final review gates.
- After a log source is selected, render only that source's configuration
  panel in the downstream evidence configuration step.
- After a trigger mode is selected, render only that trigger's configuration
  panel in the downstream evidence configuration step.
- Apply the same single-choice and conditional rendering behavior to the
  existing evidence-policy edit flow.
- Keep the create wizard's existing sequence: choose the source and trigger
  in Project details, then configure only those choices in Evidence scope.
- In the edit flow, update the visible configuration panels immediately when
  a selection changes, without adding a second confirmation screen.
- Changing either choice must clear any prior verification and require the
  newly selected configuration to be verified before continuing or saving.
- Final review and creation confirmation must display the selected source and
  trigger labels, without exposing any secret value.
- Preserve local-only behavior and responsive desktop/mobile layouts.

## Acceptance Criteria

- [x] Create project offers exactly one selectable log source and exactly one
      selectable trigger mode at a time; selecting another option replaces
      the prior option.
- [x] The evidence step renders exactly one source panel and one trigger panel
      matching the choices made earlier.
- [x] The Continue action remains blocked until both choices exist and each
      required downstream verification gate is complete.
- [x] Editing evidence policy shows only the selected source and trigger
      configuration, and changing either selection disables Save until the new
      composition is verified.
- [x] Final review and the local creation confirmation show the selected
      source, trigger mode, immutable baseline, and hidden credential state.
- [x] Browser tests cover at least two alternate branch combinations, the
      replacement behavior, verification reset, secret non-disclosure, local
      network isolation, and mobile width safety.

## Out Of Scope

- Backend persistence, connector calls, authentication, secret management,
  real webhooks, and external log or MCP requests.
- Adding new source types or trigger types beyond the existing five panels.

## Notes

- This is a lightweight high-fidelity prototype correction. Keep planning
  focused on this PRD and validate the visible interaction with the existing
  build and Playwright checks.
