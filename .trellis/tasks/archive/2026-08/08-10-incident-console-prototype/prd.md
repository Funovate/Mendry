# Incident Console Prototype

## Goal

Give stakeholders a realistic, reviewable view of the internal operations
console before implementation of the production service begins.

## Parent References

`08-10-production-incident-mvp/prd.md` and `design.md`.

## Dependencies

None. User approval of this prototype is required before starting
`08-10-incident-service-foundation` or `08-10-incident-console-api`.

## Confirmed Context

- The product is a self-hosted, single-tenant production-incident console for
  the `real-estate` project and production environment.
- Operators need to review every error occurrence, grouped incidents, evidence
  provenance, reports, notifications, outcomes, source configuration, and
  audit records. The final product also needs a visible path from diagnosis to
  AI remediation package, hotfix branch, draft PR, and verification.
- The console uses local representative data only. It must not call customer
  sources, display credentials, invoke an LLM, or create a production action.

## Requirements

- Represent the primary workflow: sign in, scan an incident list, inspect an
  incident and evidence chain, record an outcome, then review project/source
  and notification settings.
- Include screens or states for an incident list, incident detail with event
  stream and report, evidence provenance, source/policy configuration, audit
  history, remediation package/hotfix draft state, and role-restricted views.
- Show an automated remediation state with the resolved production baseline,
  restricted `hotfix/*` branch, patch/test status, draft PR, and a distinct
  human-only production-merge approval gate.
- Use representative `real-estate / production / cls-backend` data, including
  the known language-validator error, so the visible states reflect the MVP
  contract rather than generic dashboard filler.
- Make priority, lifecycle status, notifications, facts versus hypotheses, and
      evidence source/time/access scope easy to scan.
- Show how an administrator configures an incident project with low cognitive
  load: SCM-provider and repository selection, write-only Git HTTP(S) or SSH
  transport credential setup,
  production-branch selection, deployed baseline verification, and read-only
  evidence access scope. The flow must distinguish safe defaults from fields
  that need a deliberate choice.
- Let an administrator start a new project from the project switcher, provide
  project identity and service context, complete the same repository,
  credential, baseline, evidence-scope, and final-review gates, then see a
  local creation confirmation without exposing any secret value.
- Treat Git as the required project foundation, while allowing one or more
  independently selected log sources (SSH logs, cloud logs, or MCP) and one or
  more independently selected trigger modes (signed webhook or custom rule).
- Let a reviewer open representative raw evidence from the evidence chain,
  inspect the redacted log excerpt, exact query and time window, integrity
  metadata, and links back to the diagnosis that cites it.
- Let a reviewer inspect why an AI remediation is proposed: cited facts,
  rejected alternatives, the immutable baseline and patch diff, test command
  and result, and the remaining human production-merge gate.
- Let an administrator combine evidence sources and trigger paths: cloud logs
  over API or MCP, local SSH log collection, and independently selected signed
  webhooks or custom rules; all selected paths must deduplicate into the same
  incident flow.
- Support desktop and mobile review without overflow, overlap, or hidden core
  actions.
- Deliver a high-fidelity, clickable static prototype rather than wireframes.

## Out Of Scope

- Authentication, backend APIs, database persistence, connector calls, secret
  management, real notifications, and real LLM/provider integration.
- Marketing pages, production-write controls, automatic remediation, and
  frontend error discovery.

## Acceptance Criteria

- [ ] A reviewer can navigate the representative primary workflow using only
      local data and understand the incident state, evidence, and next action.
- [ ] The prototype visibly distinguishes facts, hypotheses, missing evidence,
      and proposed remediation.
- [ ] Administrative settings reveal metadata and role restrictions without
      exposing secret values or any production-action control.
- [ ] A reviewer can complete the local configuration flow and see why the
      production baseline is pinned to an immutable commit rather than a
      moving branch name.
- [ ] A reviewer can complete the full `Create project` wizard from project
      identity through final review, with required validation gates and a
      visible creation confirmation.
- [ ] An administrator can choose a representative SCM provider and save a
      write-only Git HTTP(S) or SSH credential reference without its value being displayed after
      entry, then see the least-privilege permissions attached to it.
- [ ] A reviewer can open raw representative evidence and trace the proposed
      change back to its logs, source query, and verification outcome.
- [ ] Desktop and mobile screenshots show a coherent, legible operational
      interface with no clipped or overlapping text.
- [ ] No runtime request reaches a customer system or external LLM provider.

## Disposition

Archived 2026-08-20. The static prototype and implement checklist are complete. The stakeholder-approval gate no longer blocks implementation: production work already shipped through `08-12` and `08-13`. Keep `App.tsx` / `data.ts` as reference only; remaining review surfaces belong to production tasks.

## Review Status

The primary workflow, expanded configuration, raw-evidence review,
remediation-rationale interactions, and full create-project wizard are complete.
The create-project composition model keeps Git mandatory while allowing log
sources and trigger modes to be selected independently.
