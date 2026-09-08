# Hero Flow Integration Implementation Plan

## Preconditions

- [x] User approved creation of this child task and clarified that only the right screenshot is in scope.
- [x] Read the reviewed prototype, hero design notes, docs quality rules, and existing introduction tests.
- [x] Run GitNexus upstream impact analysis for the existing Introduction, introContent, and route symbols; all were target-not-found/UNKNOWN because docs symbols are not in the current index.
- [x] Activate this child with `task.py start` before changing docs production code.
- [x] Dispatch the implementation sub-agent with model `gpt-5.6-luna` at the highest available reasoning level after activation.

## Verification Record

- `npm run lint`, `npm run check`, `npm run test`, `npm run build`, `npm audit --omit=dev`, `node --check`, and `git diff --check` passed in `docs/`.
- Preview and public builds passed; public mode emitted canonical/alternate metadata, local hero-flow OG image, and sitemap.
- Final `npm run test:e2e` passed 50 tests with 2 intentional mobile Pagefind skips. Hero-flow WebGL, fallback, no-JavaScript, no-WebGL, reduced-motion, context-loss, responsive, dark-theme, zoom, and accessibility checks passed.
- Manual Playwright screenshots were reviewed at 1440px, 390px, 320px, and dark desktop. The left hero content, hero column structure, and post-hero workflow remained intact; the actual PNG is 1240x815 and the CSS contract matches it.
- The first E2E attempt reused a leftover Astro dev server because `reuseExistingServer` was enabled; after stopping that process, preview-server E2E passed. This is an environment prerequisite, not a product failure.
- GitNexus compare detection reported `low` risk, 14 changed files, 0 indexed changed symbols, and 0 affected execution flows because the untracked docs package is outside the stale index.


## 1. Local media and component contract

- [x] Copy the reviewed scene module, fallback PNG, vendored Three.js module, and license into `docs/public/media/hero-flow/`.
- [x] Extend `IntroContent` with localized hero-flow strings while leaving existing left/post-hero content unchanged.
- [x] Replace only the right media markup in `Introduction.astro`, including semantic stages, fallback/canvas layers, data contract, and control.

Validation: `npm run check` and focused route/component inspection.

Rollback point: restore the original `<figure>` image block and remove only the new local media directory.

## 2. Scoped scene integration and styles

- [x] Adapt the prototype `scene.mjs` to the production marker/data attributes and both locale strings.
- [x] Add scoped responsive flow styles to `docs/src/styles/starlight.css` without changing existing copy or section selectors.
- [x] Point both introduction Open Graph image metadata entries at the local flow fallback.
- [x] Record the generated illustration in `docs/src/assets/media.yml`.

Validation: static server plus WebGL, reduced-motion, fallback, context-loss, and 200% zoom checks.

## 3. Focused tests and contracts

- [x] Update introduction tests for the new aspect ratio, four localized stages, fallback asset, motion control, and unchanged workflow section.
- [x] Add/adjust build contract asset checks for the flow fallback/module/vendor output and absence of external runtime requests.
- [x] Keep existing preview/public metadata and locale parity contracts passing.

Validation:

```bash
cd docs
npm run lint
npm run check
npm run test
npm run build
npm run test:e2e
npm audit --omit=dev
cd ..
git diff --check
```

## 4. Final review

- [x] Open desktop, 390px, 320px, dark, reduced-motion, and fallback screenshots for visual review.
- [x] Run `gitnexus_detect_changes` with `scope=compare` and `base_ref=main` before commit; confirm only docs hero/media/test symbols changed.
- [x] Do not commit in the implementation sub-agent; report verification and remaining browser/GPU limitations.
