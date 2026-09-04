# 优化 remediation 详情页排版与 UI

## Goal

Improve the readability and scanability of the production remediation review surface shown on an incident detail page. Operators should be able to identify run state, diagnosis, repair plans, attempts, and the suggested diff without scanning long full-width paragraphs.

## Background

- The screenshot shows the lower remediation surface rendered as a long flat column.
- `frontend/src/features/incidents/RemediationPanel.tsx` currently renders status facts, attempts, diagnosis, plans, and the suggested diff with minimal visual grouping.
- `frontend/src/features/incidents/incidents.css` owns the remediation styles; shared controls and server-state contracts should remain unchanged.
- The page uses live project-scoped remediation API data. This task is visual and interaction-focused and should not change the API contract or remediation behavior.

## Requirements

- Preserve all current remediation states and data: loading, missing run, API errors, active/recovery projection, manual-review suggestion, attempts, diagnosis, plans, and suggested diff.
- Establish a clear visual hierarchy with a compact status/metadata summary, distinct diagnosis and plan sections, a readable attempts history, and a clearly labeled suggested diff region.
- Constrain prose to a comfortable reading width and ensure long evidence IDs, file paths, plan text, and model-generated content wrap or scroll without causing document overflow.
- Keep the suggested diff legible as code with bounded height and horizontal scrolling where necessary; do not shrink code to fit the viewport.
- Make the layout stable and responsive at desktop and narrow mobile widths, with no overlapping controls or horizontal document overflow.
- Preserve accessibility semantics, capability-gated actions, existing status labels, and the current project-scoped query behavior.
- Use the existing visual tokens, controls, and icon library. Keep remediation-specific styles in `frontend/src/features/incidents/incidents.css`.

## Acceptance Criteria

- [x] Desktop remediation content is grouped into scannable sections and no longer presents all prose as full-width flat rows.
- [x] Run status, risk, attempt, origin, and loop mode can be identified quickly without reading the diagnosis body.
- [x] Diagnosis and each repair plan have distinct headings/containers; recommended plan status remains obvious.
- [x] Attempts remain readable when status/origin strings are long and do not push content off-screen.
- [x] Evidence references, affected files, rollback text, and long generated copy wrap within their owning region.
- [x] Suggested diff remains readable in a bounded code viewport with intentional overflow behavior.
- [x] Loading, error, no-run, active recovery, and manual-review states preserve their existing behavior.
- [x] The layout passes lint, type-check, unit tests, build, route-mocked E2E, screenshot checks at desktop and 390px mobile, and `git diff --check`.

## Scope

In scope: `RemediationPanel` markup/semantics and remediation feature CSS, plus focused frontend tests or screenshot assertions if needed.

Out of scope: backend/API/database changes, remediation lifecycle behavior, new approval actions, new API fields, and unrelated incident list or shell redesign.

## Diff Decision

- The suggested diff stays visible in a bounded `Suggested diff` section.
- Each parsed file renders `Original` on the left and `Changed` on the right on desktop.
- The two panes stack on narrow screens and retain intentional code scrolling.
