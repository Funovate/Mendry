# Site Foundation and Introduction Design

## Package Boundary

Create `docs/` as an independent Astro 7/Starlight package. It owns its package
manifest, lockfile, TypeScript and Astro configuration, source content, static
assets, tests, and generated `dist/`. The root repository remains a collection
of independently operated packages rather than becoming a JavaScript workspace.

The package consumes only deliberately copied visual values and approved raster
assets from the product. It does not import `frontend/src`, depend on the React
build, or call the Go API at build or runtime.

## Route Architecture

Use Starlight's localized content configuration for conventional docs pages and
custom Astro routes wrapped with the public Starlight page shell for the two
introductions:

```text
/                              English introduction
/docs/                         English docs preview overview
/docs/get-started/             English readiness-safe get-started destination
/docs/concepts/architecture/   English architecture destination
/zh-cn/                        Simplified Chinese introduction
/zh-cn/docs/                   Chinese docs preview overview
/zh-cn/docs/get-started/       Chinese readiness-safe get-started destination
/zh-cn/docs/concepts/architecture/ Chinese architecture destination
```

The custom pages share presentation components and typed locale content while
keeping localized prose explicit. Browser-language detection never redirects.
The later bilingual-content task expands the docs tree without changing these
route contracts.

## Introduction Composition

The introduction is a compact product surface rather than a generic marketing
landing page:

1. Starlight-compatible header with text brand, docs navigation, theme control,
   and locale switcher.
2. First viewport with the `FixThe` H1, literal category/deployment statement,
   authority summary, actions, and a current product image.
3. A visibly entering workflow band that explains signal-to-review progression.
4. Evidence and deterministic/optional-LLM boundaries.
5. Controlled remediation and human approval boundary.
6. Readiness-safe final action.

Layout constraints use bounded content widths, explicit image aspect ratios,
stable button dimensions, and breakpoint-specific grid tracks. Typography uses
fixed responsive steps rather than viewport-scaled font sizes. Motion is minor
and disabled under `prefers-reduced-motion`.

## Content Contract

Introduction copy is stored as typed per-locale data consumed by shared Astro
components. Product names, API/config literals, status names, commands, and
paths remain literal. English and Chinese routes each declare localized title,
description, actions, section copy, and image alt text.

Preview get-started pages explain readiness and available repository-based
development paths without inventing a customer installation command. The
architecture preview can describe only boundaries verified by code and the
parent product PRD. Availability language remains explicit.

## Visual And Asset Contract

Define a small `docs/` token stylesheet based on the current product values:
neutral canvas and surfaces, dark ink, teal accent, semantic status colors,
compact 5-6px radii, system sans, and system monospace. Keep token names local
to the docs package and document their source date; do not synchronize through a
runtime stylesheet dependency.

Use text-only `FixThe` branding until authorized source assets arrive. Product
media lives under docs-owned assets with a manifest recording source component
or fixture, capture date, synthetic-data assertion, sensitive-data review,
availability status, and dimensions. Existing screenshots are candidates, not
automatically trusted inputs; recapture from the route-mocked production app
when the current incident/remediation view is not represented accurately.

## Preview And SEO Contract

The build accepts an explicit site-origin environment value. Production
canonical generation is enabled only for an approved custom origin. Preview
builds use a controlled placeholder origin where required by Astro, emit
`noindex, nofollow`, and never treat a transient Pages host as canonical.

Static `_headers` begins with conservative security headers compatible with
Starlight theme and search scripts. `_redirects` contains only intentional route
normalization. Robots, sitemap, alternate links, canonical links, and Open Graph
metadata are inspected from built HTML. No Pages Function or adapter is needed.

## Validation Architecture

Package scripts compose these checks:

- Astro/Starlight type and content validation.
- A structured locale-route parity check rather than frontmatter regex parsing.
- Static build and internal-link/metadata inspection against `dist/`.
- Playwright tests served from built output at desktop and mobile widths.
- Accessibility assertions for landmarks, headings, focus, controls, images,
  reflow, and reduced motion.
- Visual screenshots for human review and image nonblank/load assertions.

The implementation should use the smallest dependency set that covers these
contracts. Prefer Starlight/Astro APIs and a focused Node validation script over
a custom site framework.

## Compatibility And Rollback

Pin Astro 7.3.1 and Starlight 0.42.0 with Node `>=22.12.0`. Verify their public
custom-page API through compilation before relying on it. Keep all new runtime
surface inside `docs/`; rollback is removal of the package plus its concise root
README pointer, leaving frontend and backend builds unchanged.

Media can be removed or replaced with an approved neutral product placeholder if
review fails, but unreviewed operational screenshots must never be shipped.
Public promotion remains independently reversible through the later Pages
deployment task.
