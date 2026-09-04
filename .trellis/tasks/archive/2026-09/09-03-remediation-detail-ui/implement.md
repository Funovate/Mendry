# Implementation Plan

1. [x] Add `features/incidents/remediationDiff.ts` with typed diff file/row projections and a parser for standard unified diff hunks, including a raw-text fallback signal.
2. [x] Add focused Vitest coverage for context rows, paired removal/addition rows, unmatched changes, multiple files, line-number tracking, and malformed/fallback input.
3. [x] Refactor `RemediationPanel.tsx` markup into semantic review blocks while preserving API data, state predicates, action mutations, accessible labels, and existing text contracts.
4. [x] Update `features/incidents/incidents.css` for the compact overview, main/secondary diagnosis and attempt layout, full-width plan list, readable prose widths, stable attempt rows, and split original/changed diff panes.
5. [x] Run frontend lint, type-check, unit tests, build, route-mocked E2E, screenshot checks at desktop and 390px, and `git diff --check`.
6. [x] Inspect the rendered desktop/mobile screenshots for text clipping, overlapping controls, unexpected horizontal document overflow, and code comparison alignment; adjust only feature-owned styles if needed.

## Validation commands

```bash
cd frontend
npm run lint
npm run typecheck
npm run test
npm run build
npm run test:e2e
cd ..
git diff --check
```

## Risk points

- Parser semantics are the main logic risk; keep it pure and covered independently.
- The CSS grid must set `min-width: 0` at each nested region so generated content cannot widen the document.
- The diff viewer must preserve code whitespace and provide intentional pane scrolling on mobile.
- Do not change API schemas, query/mutation wiring, authorization checks, or remediation state behavior.
