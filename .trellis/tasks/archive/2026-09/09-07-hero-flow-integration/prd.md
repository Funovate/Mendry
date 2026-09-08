# Integrate hero flow illustration into docs

## Goal

Replace only the right-hand first-viewport product screenshot on the bilingual FixThe introduction with the reviewed concrete workflow illustration from the existing research prototype.

## Confirmed facts

- `docs/src/components/Introduction.astro` owns the shared English/Chinese introduction hero and currently renders `/media/remediation-review.webp` on the right.
- `docs/src/pages/index.astro` and `docs/src/pages/zh-cn/index.astro` point Open Graph metadata at the same old screenshot.
- The reviewed prototype is `.trellis/tasks/09-06-project-docs-starlight/research/hero-flow-concept.html` with local resources in `research/hero-flow-assets/`.
- The prototype already covers a substantial four-stage WebGL scene, a local PNG fallback, localized semantic stage labels in the Chinese review, reduced-motion behavior, keyboard pause/resume, offscreen suspension, context-loss fallback, DPR capping, and disposal.
- Docs is a static Astro/Starlight package with no runtime dependency on `frontend/` or `backend/`, no remote runtime assets, and existing introduction/browser contracts.

## Requirements

1. Replace only the right-side `.intro-product-frame` media content with the approved four-stage flow visual. Keep the left copy, actions, hero columns, and every section after the hero unchanged.
2. Keep all runtime assets local to `docs/public/media/hero-flow/`, including the module script, vendored Three.js module/license, and PNG fallback. Preserve the prototype's relative vendor import and do not add a CDN or app-package import.
3. Adapt the visual to the shared English/Chinese `IntroContent` contract. The four semantic stage labels, status messages, controls, description, caption, and fallback alt text must be localized; product names and authority wording remain truthful and unchanged outside the right media.
4. Preserve the visual's accessibility and lifecycle contract: a real canvas when WebGL is available, a visible image fallback with no JavaScript/WebGL/context loss, semantic stage list and screen-reader description, a labeled pause/play control, reduced-motion static review state, offscreen/document-hidden suspension, and no automatic merge/deploy output.
5. Preserve responsive composition at desktop, 390px, 320px, dark theme, 200% CSS zoom, and long Chinese labels without horizontal overflow or overlap. The existing lower hero section must remain in the same document flow.
6. Update the media provenance manifest and introduction Open Graph image reference to the local flow fallback. Do not remove unrelated existing media records.
7. Add focused browser assertions for localized flow labels, fallback/media loading, animation controls, and the unchanged left/next-section contract while retaining the existing accessibility and build checks.

## Acceptance criteria

- [ ] `/` and `/zh-cn/` show the four-stage flow visual in the right hero column; the old remediation screenshot is no longer rendered there.
- [ ] Left-side hero copy/actions and the following workflow section retain their existing structure and localized content.
- [ ] Both locales expose exactly four localized semantic stages, localized caption/status text, a localized accessible description, and a localized media alt/provenance entry.
- [ ] Preview build remains static, non-indexable, and free of external runtime requests; public Open Graph output references the local flow fallback.
- [ ] WebGL renders a substantial scene when available, while no-JavaScript/no-WebGL/context-loss paths show the local fallback and hide the dead motion control.
- [ ] Reduced motion freezes the completed human-review state; manual pause freezes clock/frames/pixels and resumes; offscreen suspension works.
- [ ] Desktop, 390px, and 320px pages have no horizontal overflow, no accessibility violations, no visual overlap, and the next section remains discoverable.
- [ ] `npm run lint`, `npm run check`, `npm run test`, `npm run build`, focused/full `npm run test:e2e`, `npm audit --omit=dev`, and `git diff --check` pass.

## Out of scope

- Changing the left hero copy, actions, navigation, hero column layout, or any post-hero section.
- Reworking the docs information architecture, product behavior, React app, Go API, or deployment configuration.
- Adding new product claims, external artwork, a hosted asset/CDN, analytics, or a server adapter.
- Publishing or enabling the production release gate.

## Risk notes

The shared introduction component is consumed by both locales, so a broken media contract can affect both entry routes. The prototype is not indexed as production code; package-local build and browser checks must provide the detailed verification. GitNexus impact analysis for the existing docs symbols returned `UNKNOWN`/target-not-found because the docs package is outside the current symbol index; no HIGH or CRITICAL risk was reported.
