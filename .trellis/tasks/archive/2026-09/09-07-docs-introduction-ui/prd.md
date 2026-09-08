# Introduction UI refinement

## Goal

Refine the bilingual FixThe introduction UI so visitors can understand the
product, inspect its current interface, and find preview and architecture
documentation with a clear visual hierarchy on desktop and mobile.

## Confirmed Context

- This task refines `09-06-docs-foundation-introduction`, whose original
  acceptance checklist and verification record are complete.
- `docs/src/components/Introduction.astro` shares presentation across locales;
  `docs/src/content/intro.ts` provides localized content and
  `docs/src/styles/starlight.css` owns site tokens and introduction styles.
- The current hero uses a two-column text/product-frame layout. Frame labels,
  the screenshot caption, and numbered section kickers contain hardcoded English.
- The existing package provides desktop/mobile browser checks in
  `docs/tests/introduction.spec.ts`.

## Requirements

- Refine first-viewport composition, typography, spacing, section hierarchy,
  product-media presentation, and action prominence as one consistent design.
- Follow the user's selected refinement direction: retain the restrained
  documentation aesthetic, existing palette, and section order. Improve the
  existing experience without introducing a new visual identity.
- Preserve FixThe as the primary H1, reviewed local product media, intrinsic
  image dimensions, and a visible transition into subsequent content.
- Keep English and Simplified Chinese presentation complete, including captions
  and supporting labels; retain localized metadata and route parity.
- Preserve customer-managed positioning, optional redacted remote analysis,
  human-controlled merge/deployment, and preview-safe readiness language.
- Preserve Starlight navigation, theme switching, locale switching, keyboard
  access, and reduced-motion behavior.
- Constrain implementation to the docs introduction and necessary shared styles;
  verify any shared-style effects on conventional documentation pages.

## Acceptance Criteria

- [x] The agreed visual direction is implemented consistently in both locales.
- [x] Desktop and mobile screenshots show readable product media, coherent
      heading/action hierarchy, and no overlapping or horizontally overflowing UI.
- [x] Long Chinese labels, 320px reflow, and 200% zoom remain usable.
- [x] Light and dark themes maintain readable text and action contrast.
- [x] Introduction labels and captions are localized and all existing primary
      and secondary navigation destinations remain valid.
- [x] Relevant package validation and browser checks pass, with representative
      documentation pages checked for shared-style regressions.

## Out Of Scope

- Frontend/backend behavior changes, full operator documentation, production
  deployment, new branding assets, and changes to release readiness claims.
