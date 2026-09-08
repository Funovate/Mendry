# Hero Flow Integration Design

## Boundary

The change is limited to the right-hand media region of the shared introduction hero. `Introduction.astro` remains the owner of the markup and both locale pages continue to use the same component. Existing left-side content and all later sections are not refactored.

## Runtime structure

Copy the reviewed prototype resources into the docs package:

```text
docs/public/media/hero-flow/
  scene.mjs
  scene-fallback.png
  vendor/
    three.module.min.js
    LICENSE
```

The public module is loaded once by the right-hand figure. It imports the adjacent vendored module with a relative URL, queries the figure through a dedicated `data-hero-flow` marker, and does not assume a page-specific source path. A static `<img>` is rendered before JavaScript and remains the fallback until WebGL initialization succeeds. The canvas becomes visible only after the renderer is ready.

## Markup and localization contract

Extend `IntroContent` with a `heroFlow` object containing:

- localized top line, accessible description, pause/play/static labels, status line, and caption strings;
- four `{ step, title }` stage labels; and
- four phase status messages.

`Introduction.astro` renders those values as ordinary HTML/data attributes. The stage list is the accessible semantic summary; the canvas is `aria-hidden`. The fallback image carries the localized media alt text from the existing content contract. The figure keeps the existing `.intro-product-frame` class so current shell/test selectors remain stable and adds a dedicated flow class for new styling.

The script reads the serialized phase/status data from the figure, with a safe default only for malformed runtime data. It updates stage `data-active`, status text, and the pause control without embedding a second locale table in JavaScript. The control is hidden until WebGL is ready and hidden permanently on fallback/context loss.

## Styling and layout

Keep existing `.intro-hero`, copy, action, and post-hero selectors unchanged. Add scoped `.intro-flow-visual` rules after the existing product-media rules:

- a stable `aspect-ratio` matching the reviewed fallback (1240x815), transparent local image/canvas layers, and no screenshot border treatment;
- restrained top line, semantic labels, status, and caption using existing docs tokens;
- explicit stage positions and responsive breakpoints from the reviewed prototype;
- fixed control dimensions and visible focus styles;
- no viewport-scaled type, no decorative gradients, and no nested UI cards.

The fallback and canvas occupy the same bounded diagram box. The label list is overlay-only for pointer input but remains accessible as semantic HTML. CSS switches the visible layer based on `data-renderer`.

## Media and metadata

Add a `hero-flow` record to `docs/src/assets/media.yml` with source prototype, capture date, dimensions, synthetic/generated status, sensitive-data review, staleness review, and current availability. Change both introduction route Open Graph image paths to `/media/hero-flow/scene-fallback.png`; no old media record is deleted because it may be used elsewhere or retained as provenance.

## Lifecycle contract

Carry forward the reviewed scene behavior: 30fps cap, DPR cap at 2, preserved drawing buffer, logical 16-second phases, reduced-motion completed review state, manual pause, visibility/intersection suspension, context-loss fallback, and pagehide disposal. Only adapt selectors and localized text; do not introduce product API calls or new network paths.

## Validation and rollback

Update introduction E2E coverage to assert both localized stage sets, local fallback dimensions, WebGL/fallback behavior where the browser permits, controls, no overflow, axe, and unchanged next-section visibility. Run the existing docs lint/check/build/contract suite and the focused hero-flow lifecycle checks with a static server.

Rollback is limited to removing the new flow markup/script/styles/assets and restoring the existing `/media/remediation-review.webp` references. No React/backend or docs route changes are required.
