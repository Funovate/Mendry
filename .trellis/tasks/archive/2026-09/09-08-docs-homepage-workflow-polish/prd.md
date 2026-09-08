# Polish documentation homepage workflow

## Goal

Improve the documentation homepage workflow section so the four incident-response stages read as one clear, polished progression while preserving the site's restrained operational character.

## Background

- The current `Receive / Investigate / Review / Control` presentation is a large two-by-two bordered grid with weak progression and sparse content.
- The homepage already uses localized semantic content from `docs/src/content/intro.ts` and custom introduction styles in `docs/src/styles/starlight.css`.
- The affected homepage files contain pre-existing uncommitted work. This task must preserve it and limit changes to the workflow section and its focused tests.

## Requirements

- Present the four stages as a connected horizontal process on desktop, with a clearly distinguishable numbered node, title, and description for each stage.
- Emphasize the final human-control stage with the existing authority color without suggesting an automated merge or deployment action.
- Keep the section quiet, compact, and appropriate for an operational documentation product; avoid decorative illustrations, oversized type, nested cards, gradients, and new runtime dependencies.
- Preserve the existing English and Simplified Chinese content and semantic ordered-list structure.
- Reflow to two columns at intermediate widths and a readable connected vertical sequence on mobile.
- Support both light and dark themes, keyboard/assistive technology semantics, 200% zoom, and reduced-motion environments without horizontal document overflow.

## Acceptance Criteria

- [ ] At desktop width, all four workflow stages form a visually continuous single-row progression and remain readable without overlap.
- [ ] At tablet width, the stages use a stable two-column layout; at 390px and 320px widths, they become a single-column vertical progression.
- [ ] Stage 04 is visibly differentiated using `--fx-authority`; stages 01-03 continue to use the standard accent treatment.
- [ ] Existing localized stage titles and descriptions remain unchanged and are exposed through the semantic `<ol>`.
- [ ] Light and dark theme screenshots show no clipped text, incoherent overlap, or horizontal page overflow.
- [ ] Axe reports zero accessibility violations for both locales and themes.
- [ ] Focused docs checks and Playwright homepage tests pass.

## Out Of Scope

- Hero canvas, hero copy, homepage content wording, navigation, evidence/boundary/readiness sections, and documentation routes.
- New images, icons, animation logic, dependencies, or changes to deployment behavior.
