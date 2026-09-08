# Project Introduction and Documentation Site Design

## Design Summary

Add `docs/` as a third independently buildable package using Astro 7 and
Starlight 0.42. It produces one bilingual static site for Cloudflare Pages:

```text
/                         English introduction
/docs/*                   English documentation
/zh-cn/                   Simplified Chinese introduction
/zh-cn/docs/*             Simplified Chinese documentation
```

The introduction routes are custom Astro pages wrapped in the Starlight shell.
Documentation routes use Starlight's content collection, sidebar, table of
contents, Pagefind search, code rendering, i18n, sitemap, and metadata support.
No server adapter, Pages Function, analytics script, or external search service
is part of the initial release.

## Delivery Structure

Treat this task as the integration parent and implement through independently
verifiable children created after planning approval:

1. **Site foundation and introduction**: `docs/` package, Starlight/i18n shell,
   custom bilingual introduction routes, brand/media pipeline, validation
   harness, and non-indexable Cloudflare Pages previews.
2. **Core bilingual operator documentation**: paired operator-journey content,
   terminology, availability metadata, locale parity, links, and Pagefind proof.
3. **Production publication readiness**: consume the external installation,
   support/security, brand, and domain dependencies; validate production SEO,
   headers, redirects, and promotion/rollback.

The third child depends on the first two and on the external release dependencies
in `prd.md`. The parent owns final cross-child review and should not be activated
as a broad implementation target.

## Audience And Journey

The primary user is a production operations technical lead evaluating and then
self-hosting FixThe. The site should support this sequence:

1. Understand what FixThe does and what authority it deliberately does not have.
2. Validate deployment, data residency, connector, Git, and LLM assumptions.
3. Install a tested release and bootstrap the first administrator.
4. Create a project and configure the first supported signal path.
5. Receive and inspect the first incident.
6. Review evidence, diagnosis, and remediation state without granting automatic
   production merge or deployment authority.
7. Resolve common setup and runtime failures.

The primary introduction CTA is `Get started`, targeting `/docs/get-started/`.
Before the deployment release gate passes, previews render this action as a
non-production readiness notice or point to an explicitly labeled preview page;
they do not claim that a production installation path exists.

## Information Architecture

The required English and Chinese page set is:

```text
/                                  Introduction
/docs/                             Documentation overview
/docs/get-started/                 Operator journey overview
/docs/get-started/prerequisites/   Host, network, dependency, credential needs
/docs/get-started/install/         Tested installation artifact only
/docs/get-started/bootstrap/       Migration, first admin, health verification
/docs/get-started/first-project/   Project, repository, source, and trigger
/docs/get-started/first-incident/  Signal ingestion and review verification
/docs/concepts/architecture/       Components and trust boundaries
/docs/concepts/data-model/         Project, observation, incident, evidence
/docs/concepts/lifecycle/          Incident and remediation lifecycle
/docs/concepts/security/           Local data, secrets, LLM, tool, human gates
/docs/guides/tencent-cls/          First supported cloud-log path
/docs/guides/signed-webhooks/      Supported ingestion path
/docs/guides/git-and-baseline/     Repository and deployed-commit semantics
/docs/guides/llm-providers/        Opt-in, redaction, provider behavior
/docs/operations/troubleshooting/  Startup, readiness, connector, ingestion
/docs/reference/configuration/     Supported environment/configuration surface
/docs/reference/roles/             System roles, project roles, capabilities
/docs/project/status/              Available, preview, planned taxonomy
```

Every route has a `/zh-cn/...` counterpart. Exact page count may be reduced only
by merging adjacent pages without breaking the complete operator journey.
Exhaustive REST API reference, every connector, version switching, release
notes, contribution portal, and hosted search are deferred.

## Introduction Page

The introduction page is a real product surface, not a generic Starlight splash
page. It uses a custom Astro layout while retaining site title, locale switcher,
Docs navigation, theme behavior, and accessible landmarks.

First viewport:

- `FixThe` is the H1 and primary brand signal.
- Supporting copy names the literal category and customer-managed deployment.
- Replace only the right-hand screenshot with an original flow illustration.
  Preserve the hero's left copy/actions, existing columns, and subsequent page
  sections. Do not relocate the screenshot or add another product section.
  See `research/hero-flow-design.md` and `research/hero-flow-concept.html`.
  The external reference sets an aesthetic bar only, not a layout template.
- Primary action: `Get started`; secondary action: `Read the architecture`.

Following sections explain the real workflow, evidence provenance, deterministic
operation without a remote LLM, optional redacted LLM analysis, exact Git
baseline, restricted repair lifecycle, and mandatory human merge/deployment
gate. Claims use the availability taxonomy and never promote planned behavior as
shipped.

Use restrained operational styling derived from the application tokens: neutral
canvas/surfaces, teal accent, compact radii, clear status colors, and system
sans/monospace fonts. Share token values by copying an intentionally small,
documented brand token layer into `docs/`; do not import the React runtime CSS.
Avoid decorative gradients, nested cards, oversized marketing type, and imagery
that does not show the product.

## Content And Translation Contracts

English is the default locale at root; Simplified Chinese uses `zh-CN` and the
`/zh-cn/` prefix. Browser language does not trigger forced redirects.

The English and Chinese page trees are release-coupled:

- Required pages must exist in both locales.
- Navigation labels, metadata, alt text, and search-visible content are
  translated.
- Product names, status values, API fields, environment variables, paths, and
  commands remain literal where translation would make them incorrect.
- A checked-in terminology file defines stable translations for project,
  observation, incident, evidence, remediation, production baseline, and human
  review concepts.
- Behavior/configuration/security changes update both locales in the same PR.

A validation script uses parsed content metadata and normalized route IDs to
check locale parity. It must not compare frontmatter with fragile regular
expressions.

## Truth And Availability

Authoritative sources, in descending order, are current code/tests and shipped
configuration; package READMEs; completed task acceptance evidence; then active
product PRDs for explicitly labeled future behavior.

Content metadata includes an availability value where a page discusses evolving
features:

- `available`: implemented and verified against current code or a release.
- `preview`: implemented but not approved for broad production use.
- `planned`: roadmap only and never used in getting-started instructions.

The installation page cannot become `available`, and the production site cannot
publish its deployment CTA, until a tested installation artifact, release/support
statement, and security boundary exist. The docs task consumes that artifact; it
does not create the product deployment mechanism.

## Assets

Authorized logo source files, favicon inputs, and brand variants are committed
under `docs/src/assets/brand/`. Required site assets are local and optimized at
build time; no critical image or font depends on a third-party CDN.

The initial product media set uses production components with stable synthetic
data:

- incident detail and remediation review as the primary image;
- project configuration and event stream as supporting images;
- desktop and mobile crops where the composition differs.

Screenshot generation or recapture must prove there are no credentials, internal
URLs, customer identifiers, or prototype-only controls. Explicit dimensions and
responsive constraints prevent layout shift. English and Chinese alt text describe
what the image proves rather than repeating nearby copy.

## SEO And Search

The production custom domain is supplied through build/deployment configuration
and is required before public release. Astro/Starlight generates canonical URLs,
locale alternates, Open Graph metadata, and sitemaps from that origin.

Cloudflare Pages preview URLs must not become canonical or indexable production
URLs. Preview deployments set an environment-specific no-index policy. The
production build verifies:

- one canonical URL per page;
- reciprocal English/Chinese alternate links;
- sitemap routes for both locales;
- local Open Graph images and meaningful titles/descriptions;
- no duplicate introduction or docs index routes.

Use built-in Pagefind. Validate search only after `astro build` because it is not
a complete development-mode feature. Tests query representative English and
Chinese terms and verify that hidden/navigation-only content does not dominate
results.

## Cloudflare Pages

`docs/` owns its package lock and declares Node `>=22.12.0`. Cloudflare Pages
runs `npm ci && npm run build` from the docs package and publishes `docs/dist`.
Branch preview deployments are the visual/content review environment.

Static public assets include `_headers`, `_redirects`, and a generated or static
robots policy as appropriate. Security headers start conservatively with content
type sniffing prevention, referrer policy, permissions policy, and a CSP aligned
to local assets and Starlight's actual scripts. CSP is verified against built
pages before tightening; it must not silently break theme or search behavior.

No Astro Cloudflare adapter or Pages Function is added without a server-side use
case. Production rollback is a Cloudflare Pages deployment rollback to the last
validated static build.

## Validation

The docs package provides scripts for format/lint where configured, Astro type
checking, content-schema and locale-parity checks, production build, internal
link validation, and Playwright E2E.

Playwright verifies desktop and mobile introduction/docs routes, both locales,
navigation, locale switcher, theme, search modal/results, code overflow, and
404 behavior. Accessibility checks cover landmarks, keyboard navigation, focus,
contrast, headings, image alt text, reduced motion, and zoom/text reflow.

Visual review uses screenshots at representative desktop and mobile viewports.
Canvas/pixel checks are unnecessary because the site has no canvas or 3D scene,
but media loading and nonblank image regions are verified.

## Risks And Rollback

- Deployment documentation may lead implementation. Mitigation: production
  publish gate and availability metadata.
- Two locales may drift. Mitigation: same-PR policy, route parity validation,
  terminology ownership, and feature-owner review.
- Deep Starlight component overrides may make upgrades costly. Mitigation: prefer
  config, custom CSS, MDX components, and isolated custom introduction routes;
  override framework internals only for a tested requirement.
- Screenshots may disclose sensitive context or become stale. Mitigation: stable
  synthetic fixtures, explicit review, local assets, and update triggers.
- Missing domain or brand files may block final production polish. They do not
  block the package skeleton and content previews, but production release remains
  gated.

Rollback removes the independent `docs/` deployment or promotes the prior Pages
build. It does not require changes to the React application or Go API.
