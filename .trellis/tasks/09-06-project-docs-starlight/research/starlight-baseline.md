# Astro Starlight Baseline Research

## Evidence Date

2026-09-06

## Repository Findings

- The current repository contains two independently buildable packages:
  `frontend/` (React 19 + Vite 8) and `backend/` (Go).
- There is no root JavaScript workspace and no existing static-site deployment
  configuration.
- The root README only identifies the package split. It is not an authoritative
  product, deployment, or user guide.
- Existing UI screenshots under `frontend/screenshots/` can support initial
  product storytelling, but each image must be checked against the production
  application before publication because the directory includes both prototype
  and real-app captures.
- The product contract is strongest in
  `.trellis/tasks/08-10-production-incident-mvp/prd.md`. It defines FixThe as a
  customer-managed production-incident service with local evidence processing,
  deterministic behavior without an LLM, auditable diagnosis, controlled
  remediation, and mandatory human merge/deployment approval.
- The current remediation parent PRD explicitly separates delivered
  capabilities from planned integration work. Public copy must not describe
  planned workspace, validation, publication, or plan-application features as
  generally available until their child tasks land.

## Current Starlight Baseline

Registry inspection on 2026-09-06 returned:

- `@astrojs/starlight`: `0.42.0`
- Starlight peer dependency: `astro ^7.2.10`
- `astro`: `7.3.1`
- Astro engine floor: Node `>=22.12.0`, npm `>=9.6.5`

The local Pi runtime uses Node 24, but the repository has not yet declared a
project-wide Node version. The docs package must declare and enforce its own
supported Node range in CI/deployment.

## Relevant Native Capabilities

Official Starlight documentation confirms support for:

- Markdown and MDX content pages with type-safe frontmatter.
- Custom Astro pages wrapped in Starlight's page shell, plus the `splash`
  template. This allows one deployment to host a purpose-built introduction
  page and conventional documentation routes.
- Custom Astro pages can use the versioned
  `@astrojs/starlight/components/StarlightPage.astro` export. Package inspection
  confirmed the component and typed `StarlightPageProps` in 0.42.0; implementation
  should still pin the dependency and compile the custom routes before relying
  on that contract.
- Locale-aware routing, locale labels, fallback content, and a language picker.
- Static search powered by Pagefind. Search only works against production build
  output, so local preview/build validation is required; dev mode is not enough.
- Configurable logo, favicon, social links, sidebar, edit links, custom CSS,
  injected head metadata, last-updated display, and component overrides.
- Sitemap integration and generated canonical/alternate metadata.

## Cloudflare Pages Deployment Findings

Official Cloudflare Pages documentation confirms the static Astro defaults:

- Build command: `npm run build`
- Build output directory: `dist`
- Branch preview deployments provide real review URLs before production.
- A `_headers` file in the static asset directory configures response headers
  for static assets.
- A `_redirects` file configures static redirects. Redirect and canonical rules
  should be planned together to avoid duplicate indexable content.

The initial site has no server-side requirement, so adding an Astro Cloudflare
adapter or Pages Functions would add runtime surface without user value.

## Recommended Architecture Direction

- Add a third independently buildable package such as `docs/`, rather than
  placing Starlight inside the React application. This preserves the existing
  package boundary and avoids coupling documentation releases to the API or SPA.
- Use a custom Astro route for `/` so FixThe and a truthful product workflow are
  visible in the first viewport. Use Starlight-managed content routes for the
  documentation hierarchy.
- Recreate a small set of shared brand tokens in the docs package instead of
  importing the React application's CSS directly. The two products have
  different layout contracts and should share values, not runtime stylesheets.
- Start with built-in Pagefind unless scale, private content, or hosted analytics
  creates a concrete requirement for Algolia or another search provider.
- Keep documentation in-repository and review it with code so commands, config,
  security boundaries, and feature availability can change atomically.

## Research And Optimization Checklist

### Product And Content

1. Decide the primary audience and conversion goal before designing the home
   page. Operators evaluating self-hosting need different proof than engineers
   already installing the service.
2. Establish a feature-availability taxonomy such as `available`, `preview`,
   and `planned`. Generate no claims directly from old prototype copy.
3. Design onboarding as an end-to-end success path: prerequisites, deployment,
   bootstrap admin, project setup, connector setup, first observation, incident
   review, and remediation review.
4. Create separate conceptual explanations for project boundaries, observation
   versus incident, evidence provenance, deterministic versus LLM behavior,
   lifecycle, and human approval boundaries.
5. Define ownership and update triggers for configuration reference, CLI/API
   reference, migration notes, screenshots, and troubleshooting.

### Information Architecture

1. Introduction: literal product name, concrete category/value proposition,
   real console image, trust boundaries, supported workflow, deployment model,
   and a direct path to getting started.
2. Get started: prerequisites, installation, bootstrap, first project, first
   signal, and verification.
3. Concepts: architecture, data boundaries, lifecycle, evidence model,
   remediation model, security, and RBAC.
4. Guides: Tencent CLS/webhook/Git/LLM configuration, operations, backup,
   upgrades, troubleshooting, and recovery.
5. Reference: environment variables, configuration schema, REST API, connector
   contracts, statuses/errors, and retention defaults.
6. Project: roadmap/status, release notes, contribution/security policy, and
   license/contact links, only where the corresponding repository artifacts
   exist.

### Technical And Operational

1. Choose URL topology (`example.com` plus `/docs`, or a single docs-oriented
   domain), host, preview environment, and cache/invalidation policy.
2. Define Node/npm versions and lockfile/CI ownership for the new package.
3. Add build, broken-link, Pagefind-index, sitemap, canonical URL, and HTML
   validation checks.
4. Add responsive Playwright screenshots and accessibility checks for the custom
   home page, docs shell, navigation, search modal, code blocks, and locale
   switcher.
5. Decide analytics and privacy policy. Prefer no analytics initially or a
   privacy-preserving tool with explicit event definitions; do not add trackers
   by default.
6. Define CSP and third-party asset rules. Self-host fonts and critical media
   where practical; avoid runtime dependence on external CDNs.
7. Establish image sizing/formats, alt-text rules, social preview generation,
   favicon/logo assets, and a screenshot refresh process.
8. Keep API/config reference manual for the MVP unless there is a stable source
   schema. Later generation should be schema-driven and checked for drift.
9. Decide whether versioned docs are truly required. Avoid adding a versioning
   plugin until multiple supported product release lines exist.
10. Validate production output, not only `astro dev`, because static search and
    final URLs depend on the built site.

## Sources

- `https://www.npmjs.com/package/@astrojs/starlight` registry metadata queried
  with `npm view`.
- `https://www.npmjs.com/package/astro` registry metadata queried with
  `npm view`.
- `https://starlight.astro.build/guides/pages/`
- `https://starlight.astro.build/guides/i18n/`
- `https://starlight.astro.build/guides/site-search/`
- `https://starlight.astro.build/reference/configuration/`
- `README.md`
- `frontend/package.json`
- `frontend/src/styles/tokens.css`
- `.trellis/tasks/08-10-production-incident-mvp/prd.md`
- `.trellis/tasks/08-14-agentic-remediation-harness/prd.md`
