# Project Introduction and Documentation Site

## Goal

Create a public-facing bilingual project introduction and documentation site
using Astro Starlight. It must help production operations technical leads
understand FixThe, evaluate its authority and security boundaries, and complete
a verified path from self-hosted installation to first incident review without
relying on source-code exploration.

## Background

- FixThe is a single repository with independently buildable React/Vite
  `frontend/` and Go `backend/` packages. The documentation site will be a third
  independent package.
- The current product visual system is a restrained operational interface with
  compact radii, neutral surfaces, system sans/monospace typography, status
  colors, and teal accent (`frontend/src/styles/tokens.css`).
- The root README only identifies package structure. `backend/README.md` is the
  strongest runnable source, but it describes development startup rather than a
  supported customer installation.
- The repository has no configured Git remote, release tags, public license,
  security/contribution policy, or Docker Compose/deployment manifest.
- Product PRDs describe a customer-managed deployment, local evidence
  processing, deterministic behavior without an LLM, optional redacted remote
  LLM analysis, exact Git baselines, controlled remediation, and mandatory
  human merge/deployment approval. Some final remediation integration and the
  production installation artifact remain planned work.
- Current registry research pins the planning baseline at Astro 7.3.1 and
  Starlight 0.42.0. Astro 7 requires Node `>=22.12.0`.

## Requirements

### Audience And Outcome

- The primary audience is a production operations technical lead evaluating and
  self-hosting FixThe.
- The primary site action is a deployment-oriented `Get started` journey.
  Architecture, security boundaries, product status, and source links support
  that decision.
- The first release covers one complete operator journey: evaluation,
  prerequisites, installation, administrator bootstrap, first project and
  supported signal path, first incident, evidence/remediation review, and common
  troubleshooting.

### Routes And Content

- Use one static Astro/Starlight site. English is the default locale at `/` and
  `/docs/*`; Simplified Chinese uses `/zh-cn/` and `/zh-cn/docs/*`.
- Do not force redirects from browser language. Provide an obvious locale
  switcher and reciprocal locale metadata.
- The MVP sitemap contains paired English/Chinese pages for the introduction,
  docs overview, getting started, prerequisites, installation, bootstrap, first
  project, first incident, architecture, data model, lifecycle, security,
  Tencent CLS, signed webhooks, Git/baseline, LLM providers, troubleshooting,
  configuration, roles, and product status.
- Keep exhaustive REST API reference, every connector, versioned docs, release
  notes, and a full contributor portal out of the first release unless required
  to complete the operator journey.
- Use `available`, `preview`, and `planned` labels. Executable guides and primary
  product claims describe only behavior verified in current code or a tested
  release; roadmap content is visibly separate.

### Introduction And Media

- Implement purpose-built introduction routes while retaining consistent
  Starlight navigation, locale, theme, and accessibility behavior.
- `FixThe` is the first-viewport H1 and primary brand signal. Supporting copy
  states the literal product category, customer-managed deployment model, and
  authority boundary. The next section remains visibly discoverable on desktop
  and mobile.
- Replace the first-viewport product screenshot with an original, polished
  illustration explaining FixThe's operation: evidence inputs, investigation,
  diagnostic and candidate-plan outputs, and an explicit human-review boundary.
  The user supplied https://teammodel.ai/ as an aesthetic quality reference only,
  explicitly not as a layout or visual-design template. Do not assume a central
  hub, orbiting nodes, reference palette, or reference animation.
- Keep the existing hero's left copy, actions, column layout, and all subsequent
  page sections unchanged. Replace only the right-hand screenshot with the flow
  illustration. Do not move the screenshot elsewhere or add a product section.
  This scope supersedes the earlier full-width concept.
- The illustration composition requires user review before implementation.
  Review `research/hero-flow-concept.html` and `research/hero-flow-design.md`.
  Do not copy third-party assets or introduce unsupported product claims.
- Capture media from production components using stable synthetic data. Exclude
  credentials, internal endpoints, customer/personal identifiers, and obsolete
  prototype-only behavior; validate desktop and mobile composition.
- Authorized external logo/favicon/brand source files must be committed into the
  docs package with confirmation of repository and public-site usage rights.
  Required assets are local to the build; do not invent replacement branding or
  depend on a private repository/CDN.
- Reuse a small, docs-owned set of brand token values without importing the
  React application's runtime stylesheets.

### Localization And Maintenance

- The initial public release provides complete English and Simplified Chinese
  navigation, required pages, metadata, alt text, and search-visible content.
- Maintain a terminology source for stable domain translations. Product names,
  statuses, API fields, environment variables, paths, and commands remain
  literal when translation would make them incorrect.
- User-visible behavior, configuration, commands, and security-boundary changes
  update the English source and Chinese counterpart in the same pull request.
  The feature owner reviews operational truth; CI checks route parity, links,
  search indexes, and production builds.

### Search, SEO, Privacy, And Deployment

- Use Starlight's static Pagefind search for both locales and validate it from
  production build output. Do not add hosted search initially.
- Do not load analytics or visitor-tracking scripts in the first release.
- Deploy static `dist` output to Cloudflare Pages with branch preview builds. Do
  not add an Astro Cloudflare adapter or Pages Function without a server-side
  requirement.
- Keep the canonical origin configurable in previews. A custom production domain
  is required before public release; preview `pages.dev` URLs must not become
  permanent canonicals or indexable production URLs.
- Define and validate sitemap, canonical/alternate metadata, robots behavior,
  Open Graph metadata, `_headers`, `_redirects`, cache policy, CSP, Node version,
  and production/preview branch settings.

### Publication Gate

- Site implementation, content drafts, and non-indexable Pages previews may be
  completed before product deployment packaging is ready.
- Public production publication and the deployment CTA are blocked until all of
  the following exist: a tested customer installation artifact, explicit
  release/support status, documented security boundary, custom canonical domain,
  and authorized brand assets.
- This task documents and consumes the installation artifact. It does not invent
  or implement the product deployment mechanism.

## Acceptance Criteria

- [ ] English and Simplified Chinese introduction routes present the approved
      product narrative, real current product media, authority boundaries, and
      valid get-started/architecture actions at desktop and mobile sizes.
- [ ] Every required sitemap route exists in both locales with translated
      navigation, metadata, alt text, terminology-consistent content, and no
      forced locale redirect.
- [ ] A reader can follow the documented operator journey through a tested
      installation and first incident review without undocumented repository
      knowledge or aspirational commands.
- [ ] Product claims and guides link to authoritative shipped sources or carry a
      visible `preview`/`planned` status; planned behavior never appears as an
      executable production step.
- [ ] Static Pagefind returns representative English and Chinese results from the
      production build without a hosted search or analytics dependency.
- [ ] Generated production pages have correct canonical, reciprocal locale,
      sitemap, robots, Open Graph, security-header, and redirect behavior;
      preview deployments are non-canonical and non-indexable.
- [ ] Locale-parity, content-schema, internal-link, type/build, accessibility,
      and Playwright desktop/mobile checks pass in CI.
- [ ] Required brand and product assets are local, authorized, optimized,
      intrinsically sized, accessible, and reviewed for sensitive or stale data.
- [ ] Cloudflare Pages can produce a branch preview from the locked docs package
      and roll production back to a prior validated static deployment.
- [ ] Production promotion remains blocked until the tested installation,
      release/security statement, custom domain, and authorized assets are all
      present and reviewed.

## Out Of Scope

- Changing React application or Go API behavior solely to support site copy.
- Creating the missing customer deployment package inside this docs task.
- Automatic production merge, deployment, rollback, or claims of autonomous
  recovery.
- Exhaustive API/connector reference, documentation version switching, visitor
  analytics, hosted search, a full contribution portal, or server-rendered site
  behavior.

## External Release Dependencies

These do not block package/content preview work but do block public production
promotion:

- Authorized external brand files and usage confirmation.
- Final custom domain and Cloudflare Pages domain configuration.
- Tested customer installation artifact plus explicit release/support and
  security-boundary documentation from its owning product task.
