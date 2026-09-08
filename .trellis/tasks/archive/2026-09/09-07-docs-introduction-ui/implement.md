# Introduction UI Refinement Implementation Plan

## Preconditions

- [x] User selects refinement of the existing visual style.
- [x] User reviews planning artifacts and approves implementation.
- [x] Activate this task and load `trellis-before-dev` and docs guidelines.

## Execution

1. Capture the current English/Chinese introduction at desktop and mobile sizes;
   inspect light/dark styles and existing browser configuration.
2. Run GitNexus impact on affected symbols and report callers, processes, risk,
   and any index limitations before editing them.
3. Localize retained captions and section labels through the shared content
   contract and component.
4. Refine hero/media composition, spacing, action hierarchy, section alignment,
   responsive behavior, and theme color pairings within introduction styles.
5. Inspect screenshots and address overlap, unreadable media, wrapping, focus,
   and contrast problems. Check representative docs navigation for regressions.
6. Add focused browser assertions for uncovered acceptance requirements and
   run the package quality gate.
7. Record verification and present screenshots plus the local development URL.

## Validation

From `docs/`, run `npm run lint`, `npm run check`, `npm run test`,
`npm run test:e2e` (includes production build), and `npm audit --omit=dev`.
Run `git diff --check` from the repository root.

Browser review covers English/Chinese, desktop/mobile, light/dark, 320px reflow,
200% zoom, next-section visibility, images, navigation, and keyboard focus.
Run GitNexus `detect_changes` before any authorized commit.

## Change Boundaries

Expected files: `docs/src/components/Introduction.astro`,
`docs/src/content/intro.ts`, `docs/src/styles/starlight.css`, and
`docs/tests/refinement.spec.ts`. Avoid unrelated page, backend, frontend,
dependency, and asset changes. Preserve all existing user changes on rollback.

## Verification Record

Completed implementation and verification on 2026-09-07:

- GitNexus impact for `Introduction`, `IntroContent`, and `introContent`
  returned UNKNOWN / target not found. Source references identify the English
  and Chinese introduction routes as consumers; the untracked docs package is
  absent from the index. No indexed process impact can be claimed.
- Added Starlight's `not-content` opt-out and unlayered scoped presentation
  rules; refined spacing, headings, screenshot framing, and action labels.
- Localized section labels and the media caption. Added semantic foreground
  pairings for dark-theme actions and the readiness band.
- Fixed responsive intrinsic image height and 200% CSS zoom overflow in native
  header selectors and the long English readiness action.
- `npm run lint`, `npm run check` (zero diagnostics; eight locale routes),
  `npm run test`, `npm run build`, and `npx playwright test` passed.
  All 20 browser cases passed, including axe in both themes, desktop/mobile,
  320px reflow, 200% CSS zoom, and architecture navigation.
- `npm audit --omit=dev` reported zero vulnerabilities. Preview build's sitemap
  warning remains expected without a production origin.
- Final screenshots: `/tmp/fixthe-final-{desktop,mobile}-{en,zh-cn}-{light,dark}.png`.
  Inspected representative screenshots across both locales, themes and sizes.
- Development server: `http://127.0.0.1:4323/` (requested 4322 was occupied).
- Updated docs quality guidelines with cascade, image sizing, theme pairing,
  and viewport assertion contracts.

Code remains uncommitted alongside the pre-existing untracked docs foundation;
task archival is pending a coordinated commit of that work.
