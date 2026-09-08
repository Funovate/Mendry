# Project Introduction and Documentation Site Implementation Plan

## Preconditions

- [ ] User reviews and approves `prd.md`, `design.md`, and this plan.
- [x] Curate `implement.jsonl` and `check.jsonl` and validate task context.
- [ ] After approval, create the three child tasks from `design.md`, fully plan
      the first child, and run `task.py start` on that child before changing
      production/site code. Do not activate this integration parent for broad
      implementation.
- [ ] Confirm the supported Node release used by local development and
      Cloudflare Pages is `>=22.12.0`.

## 1. Package Foundation

- [ ] Add independent `docs/` package with pinned Astro/Starlight versions,
      package lock, scripts, TypeScript config, and static output.
- [ ] Configure English root locale, `zh-CN` under `/zh-cn/`, site metadata,
      `/docs` content routes, sidebar, Pagefind, sitemap, and local assets.
- [ ] Add a typed content schema for availability/translation metadata and a
      parsed route-parity validator.
- [ ] Add root README package/build pointers without rewriting backend or
      frontend instructions.

Validation:

```bash
cd docs
npm ci
npm run check
npm run build
```

Rollback point: remove only the independent `docs/` package and root package
pointer; existing frontend/backend builds remain unchanged.

## 2. Brand And Shared Shell

- [ ] Receive authorized logo source files and variants under
      `docs/src/assets/brand/`; record public repository/site usage confirmation.
- [ ] Define a small docs-owned brand token stylesheet based on current product
      values without importing React application CSS.
- [ ] Configure logo, favicon, theme colors, typography, navigation, language
      switcher, social/source links when a public remote exists, and 404 page.
- [ ] Verify Starlight defaults before replacing any shell component; document
      each override and cover it with browser tests.

Gate: visual polish can use temporary text branding in previews, but final review
cannot pass without authorized assets.

## 3. Bilingual Introduction

- [ ] Implement `/` and `/zh-cn/` as custom Astro routes using the Starlight page
      shell and shared introduction components.
- [ ] Build the first viewport around FixThe, a literal customer-managed incident
      remediation description, real incident/remediation media, and get-started
      and architecture actions.
- [ ] Add workflow, evidence, security/authority, deployment model, and final CTA
      sections with availability-safe claims.
- [ ] Generate local per-locale Open Graph images from approved brand/product
      assets and define complete metadata/alt text.
- [ ] Ensure following content remains visible at every target first viewport and
      media/text never overlap at mobile, desktop, zoom, or long translation
      lengths.

## 4. Core Operator Documentation

- [ ] Create the required English route set from `design.md` with executable,
      source-cited content.
- [ ] Create the complete Simplified Chinese counterpart and checked-in terminology
      mapping in the same change.
- [ ] Use Starlight components for steps, asides, tabs, code, and file trees only
      where they improve task completion; avoid card-heavy prose.
- [ ] Mark evolving features as `available`, `preview`, or `planned` and exclude
      planned behavior from executable guides.
- [ ] Validate every command against a clean supported environment or mark the
      page blocked rather than publishing aspirational output.

Publication gate: installation content and production deployment CTA stay blocked
until a tested product installation artifact and support/security statement are
available from their owning task.

## 5. Product Media

- [ ] Add a deterministic screenshot fixture/state for current production UI if
      the existing E2E harness cannot reproduce approved states.
- [ ] Capture incident/remediation review as primary media and configuration/event
      stream as supporting media at approved desktop/mobile sizes.
- [ ] Review captures for credentials, private endpoints, customer/personal data,
      obsolete prototype behavior, clipping, and unreadable text.
- [ ] Optimize local raster output and set intrinsic dimensions/responsive source
      rules to prevent layout shift.

Rollback point: replace media with approved placeholders without weakening text
content; never ship unreviewed operational captures.

## 6. Search, SEO, And Pages Deployment

- [ ] Configure Pagefind and test representative English and Chinese queries from
      built output.
- [ ] Require the canonical custom domain in production while allowing explicit
      non-indexable Pages preview origins.
- [ ] Validate canonical, reciprocal alternate, sitemap, robots, Open Graph, and
      noindex behavior for production versus preview builds.
- [ ] Add Pages build settings and local `_headers`/`_redirects` assets; verify CSP
      against theme switching, search, code rendering, and local media.
- [ ] Keep deployment static and analytics-free; do not add adapter, Functions,
      hosted search, remote font, or visitor tracking dependencies.

Rollback point: promote the prior validated Cloudflare Pages deployment.

## 7. Quality Gates

- [ ] Run docs formatting/lint checks, Astro type/content checks, locale parity,
      production build, broken-link validation, and `git diff --check`.
- [ ] Run Playwright at desktop/mobile viewports for both introductions and
      representative docs routes, including keyboard navigation, locale switch,
      theme, search, 404, code overflow, and image loading.
- [ ] Run automated accessibility checks and manually verify focus order, heading
      hierarchy, contrast, zoom/reflow, reduced motion, and alt text.
- [ ] Inspect generated HTML for canonical/alternate metadata and confirm both
      Pagefind locale indexes contain expected results.
- [ ] Use GitNexus `detect_changes({scope: "compare", base_ref: "main"})` before
      commit and confirm no frontend/backend execution flow changed unexpectedly.

Suggested commands after scripts are finalized:

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

## 8. Release Review

- [ ] Confirm authorized brand files are present.
- [ ] Confirm public Git remote/source links, license, support/security policy,
      and release/version status are truthful or omitted.
- [ ] Confirm the custom canonical domain is connected.
- [ ] Confirm the tested installation artifact and deployment support statement
      passed their owning task's checks.
- [ ] Conduct bilingual feature-owner review and final Cloudflare Pages preview
      review before promoting to production.
- [ ] Record deferred API reference, versioning, analytics, hosted search, and
      contributor portal work without silently expanding this MVP.
