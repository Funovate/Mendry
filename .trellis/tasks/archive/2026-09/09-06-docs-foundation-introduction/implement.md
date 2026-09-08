# Site Foundation and Introduction Implementation Plan

## Preconditions

- [x] User reviews and approves this child PRD, design, and implementation plan.
- [x] Run `task.py start 09-06-docs-foundation-introduction` after approval and
      before changing production or site code.
- [x] Load `trellis-before-dev` and all relevant frontend guidelines before
      implementation.
- [x] Load the Cloudflare platform guidance before authoring Pages-specific
      files or validating deployment behavior.
- [x] Confirm local Node satisfies `>=22.12.0` and record exact package versions
      in the lockfile.

## 1. Scaffold The Independent Package

- [x] Create `docs/package.json`, lockfile, Astro/TypeScript configuration, and
      scripts for development, checks, tests, and static production build.
- [x] Pin Astro 7.3.1 and Starlight 0.42.0 and declare the Node engine floor.
- [x] Configure Starlight, sitemap, English default locale, `zh-CN` locale, and
      the fixed route topology without a server adapter.
- [x] Add minimal paired docs destinations for overview, get started, and
      architecture so introduction links are never broken.
- [x] Update the root README with the third package and its commands.

Validation:

```bash
cd docs
npm ci
npm run check
npm run build
```

Rollback point: remove `docs/` and only the docs lines added to the root README.

## 2. Build The Localized Introduction Shell

- [x] Implement shared introduction components and explicit English/Chinese
      content contracts using Starlight's supported custom-page shell.
- [x] Add `/` and `/zh-cn/` with localized metadata, text branding, navigation,
      theme control, locale switching, and valid paired actions.
- [x] Implement the first viewport and workflow, evidence, authority, controlled
      remediation, and readiness sections.
- [x] Keep the get-started action preview-safe until the external installation
      gate passes.

Validation: compile both routes and exercise navigation, theme, locale, heading
hierarchy, keyboard focus, and 404 behavior in a production preview server.

## 3. Establish Visual Tokens And Media

- [x] Add a small docs-owned token layer derived from the current product
      palette, typography, spacing, and compact radii.
- [x] Identify or recapture current incident/remediation and supporting product
      views using stable synthetic fixtures.
- [x] Add a media provenance/review manifest and reject images with sensitive,
      personal, internal, or obsolete prototype content.
- [x] Optimize approved local images and specify intrinsic and responsive sizes
      with localized alt text.
- [x] Verify first-viewport composition and visibility of following content at
      target desktop/mobile sizes, zoom, and long localized text.

Rollback point: use text branding and an approved neutral product placeholder;
do not retain failed or unreviewed captures.

## 4. Add Preview And Metadata Foundations

- [x] Implement explicit production versus preview origin/indexing behavior.
- [x] Add robots, sitemap, canonical/alternate, Open Graph, `_headers`, and
      `_redirects` foundations using static output only.
- [x] Verify the CSP against theme switching, built-in search assets, code
      rendering, and local media without introducing external runtime hosts.
- [x] Document Cloudflare Pages build command, output directory, Node version,
      and preview environment inputs without deploying production.

Validation: inspect generated HTML and public assets for both locales in
production-like and preview-mode builds.

## 5. Automate Quality Gates

- [x] Add structured locale-route parity and internal-link checks.
- [x] Add Playwright coverage for both introductions and representative docs
      destinations at desktop and mobile viewports.
- [x] Cover locale switching, theme, keyboard focus, landmarks, images, 404,
      reduced motion, text reflow, and non-indexable preview metadata.
- [x] Capture and inspect desktop/mobile screenshots; verify media renders and
      no content overlaps.
- [x] Run package checks, production build, browser tests, accessibility checks,
      and `git diff --check`.
- [x] Run GitNexus `detect_changes({scope: "compare", base_ref: "main"})` before
      commit and verify frontend/backend execution flows are unchanged.

Suggested final commands:

```bash
cd docs
npm run lint
npm run check
npm run test
npm run build
npm run test:e2e
cd ..
git diff --check
```

## Verification Record

Completed 2026-09-07 with Node 24.18.0:

- `npm ci --include=optional` completed from the independent lockfile.
- `npm run lint`, `npm run check`, `npm run test`, and the preview
  `npm run build` passed; Astro reported zero diagnostics and Pagefind indexed
  all 9 generated HTML pages.
- `npm run test:e2e` passed 12 desktop/mobile checks, including axe with zero
  violations, image loading, locale switching, 404 status, 320px Chinese text
  reflow, reduced motion, and horizontal-overflow assertions.
- The public-release build passed with
  `PUBLIC_SITE_ORIGIN=https://docs.fixthe.example`, producing canonical and
  alternate metadata, an absolute Open Graph image, and sitemap files. A
  `.pages.dev` public origin is rejected by configuration.
- `npm audit --omit=dev` reported zero vulnerabilities and `git diff --check`
  passed.
- GitNexus compare analysis reported LOW risk and zero affected execution
  flows. The new untracked `docs/` package is not present in the current index,
  so package-local checks provide the detailed coverage for its new symbols.

## Completion Boundary

Archive this child when the independent package, bilingual introduction,
preview-safe linked destinations, reviewed media path, metadata foundation, and
quality gates pass. Leave the full documentation corpus and public production
promotion to their owning child tasks.
