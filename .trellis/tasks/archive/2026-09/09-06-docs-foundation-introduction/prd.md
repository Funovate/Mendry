# Site Foundation and Introduction

## Goal

Create the independently buildable Astro Starlight documentation package and
its bilingual introduction experience so FixThe can be evaluated through a
truthful, accessible, non-indexable preview without coupling docs releases to
the React application or Go API.

## Background

- The repository currently has independently buildable `frontend/` and
  `backend/` packages, with no root JavaScript workspace or static-site package.
- The parent task fixes the URL topology: English introduction at `/`, English
  docs under `/docs/*`, Simplified Chinese introduction at `/zh-cn/`, and
  Chinese docs under `/zh-cn/docs/*`.
- The initial dependency baseline is Astro 7.3.1 and Starlight 0.42.0. Astro 7
  requires Node `>=22.12.0`.
- Product screenshots exist under `frontend/screenshots/`, but several represent
  different application generations. No screenshot is approved for public use
  until it is checked against current production components and reviewed for
  sensitive or obsolete content.
- Authorized external brand assets, a custom production domain, and a tested
  customer installation artifact are not yet available. They block public
  production promotion, not local or non-indexable preview work.

## Requirements

### Package Foundation

- Add `docs/` as a third independently buildable package with its own pinned
  dependencies, lockfile, Node engine declaration, TypeScript configuration,
  and development, check, test, and production-build scripts.
- Produce static output only. Do not add an Astro Cloudflare adapter, Pages
  Function, hosted search, analytics, remote font, or visitor-tracking script.
- Add concise root README pointers for installing, developing, checking, and
  building the docs package without changing backend or frontend instructions.

### Routing And Shell

- Configure English as the default locale and Simplified Chinese as `zh-CN`
  under `/zh-cn/`, without browser-language redirects.
- Preserve Starlight navigation, theme behavior, accessibility, and locale
  switching on the custom introduction routes.
- Establish `/docs/*` and `/zh-cn/docs/*` content roots with paired placeholder
  overview/get-started/architecture destinations sufficient to keep all
  introduction actions valid. Full operator documentation belongs to the
  bilingual-content child task.
- Use Starlight configuration, public APIs, and isolated custom components
  before considering framework-internal overrides.

### Introduction Experience

- Implement purpose-built English and Chinese introduction routes with
  reciprocal locale navigation and complete localized metadata and alt text.
- Make `FixThe` the first-viewport H1 and primary brand signal. Supporting copy
  must identify it as a customer-managed production incident investigation and
  controlled remediation system.
- State the authority boundary explicitly: deterministic operation does not
  require a remote LLM; optional remote analysis uses redacted context; merge
  and production deployment remain human-controlled.
- Provide a primary get-started action and a secondary architecture action.
  Until the installation release gate passes, the get-started destination must
  be visibly labeled as preview/readiness information and must not claim a
  supported production install exists.
- Keep a visible hint of the section following the first viewport at desktop
  and mobile sizes. Text, actions, navigation, and media must not overlap at
  target viewports, 200% zoom, or with longer Chinese labels.
- Use restrained operational styling derived from a small docs-owned copy of
  approved product token values. Do not import the React runtime stylesheet.

### Media And Brand

- Use text branding for preview builds until authorized logo and favicon source
  files and their usage rights are supplied. Do not invent replacement marks.
- Prefer a current incident/remediation review as primary media and current
  project configuration or event stream as supporting media.
- Copy or recapture only assets validated against current production
  components and stable synthetic data. Exclude credentials, private endpoints,
  customer/personal identifiers, and obsolete prototype-only behavior.
- Keep required media local, optimized, accessible, and intrinsically sized to
  avoid layout shift. Record its source and review status in the docs package.

### Preview, Metadata, And Validation

- Keep preview origins configurable and non-canonical. Preview output must be
  non-indexable and must not silently emit a `pages.dev` URL as the permanent
  canonical origin.
- Establish sitemap, canonical/alternate, Open Graph, robots, `_headers`, and
  `_redirects` foundations while leaving production promotion gated on the
  parent task's external release dependencies.
- Add automated checks for types/content, locale route parity, internal links,
  production build, generated metadata, and representative desktop/mobile
  browser behavior.
- Validate both locales, navigation, locale switching, theme, accessible
  landmarks and focus, image loading, 404 behavior, reduced motion, and text
  reflow. Search-result proof and the complete page tree belong to the content
  child task, but the foundation must not prevent built-in Pagefind indexing.

## Acceptance Criteria

- [x] A clean supported Node environment can install and build `docs/`
      independently, producing a static `dist/` without a server adapter.
- [x] `/` and `/zh-cn/` render localized custom introduction pages inside a
      consistent Starlight shell with working theme, navigation, and reciprocal
      locale switching.
- [x] `FixThe`, the customer-managed deployment model, the authority boundary,
      current product media, and valid get-started/architecture actions are
      visible and understandable at desktop and mobile sizes.
- [x] The introduction never presents planned installation or remediation
      behavior as available, and preview output is non-indexable and
      non-canonical by default.
- [x] Docs-owned styles use the product's restrained visual values without a
      runtime import from `frontend/`, and authorized assets are not fabricated.
- [x] Every committed product image has recorded provenance, sensitive-data and
      staleness review, localized alt text, intrinsic dimensions, and responsive
      behavior.
- [x] Locale-parity, internal-link, type/content, production-build, generated
      metadata, accessibility, and Playwright desktop/mobile checks pass.
- [x] The root README identifies `docs/` and its supported local commands while
      preserving existing package instructions.

## Out Of Scope

- Writing the complete bilingual operator documentation set or proving final
  Pagefind search quality; that belongs to the content child task.
- Publishing a production Cloudflare Pages deployment, connecting the custom
  domain, or claiming a supported installation path; that belongs to the
  publication-readiness child task.
- Creating product deployment artifacts, changing frontend/backend behavior,
  adding analytics, hosted search, exhaustive API reference, or documentation
  versioning.

## Release Dependencies

The following remain explicit blockers for public production promotion but do
not block this child task's local and non-indexable preview acceptance:

- Authorized brand files and repository/public-site usage confirmation.
- A custom canonical domain and final Cloudflare Pages domain settings.
- A tested customer installation artifact and approved release/support and
  security-boundary statements.
