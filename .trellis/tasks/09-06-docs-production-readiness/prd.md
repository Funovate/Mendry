# Production publication readiness

## Goal

Make the bilingual FixThe documentation package reproducibly deployable to
Cloudflare Pages without claiming that the project is publicly released.
Provide automated quality gates and an operator runbook so a later release can
be approved, deployed, verified, and rolled back without rediscovering the
process.

## Requirements

- Pin and document the Node.js version used by local and Cloudflare Pages builds.
- Provide a single platform-neutral verification command that installs from the
  lockfile when appropriate and runs formatting, Astro/content, contract,
  preview/public build, browser, and dependency-security checks. A future CI
  provider must be able to invoke the same command without changing semantics.
- Keep the site static and deployable from the `docs` package with build output
  in `docs/dist`; do not add Pages Functions or runtime service dependencies.
- Preserve preview safety: builds without an approved HTTPS origin and explicit
  public-release gate must remain `noindex, nofollow` and omit public canonical
  and sitemap claims.
- Preserve the installation page as `planned` and non-executable until a real,
  versioned distribution artifact exists.
- Document Cloudflare Pages project settings, preview verification, production
  promotion, post-deploy smoke checks, and rollback procedure.
- Add a release checklist that names unresolved external inputs, including the
  approved custom domain, Cloudflare project/account ownership, repository
  remote, license, version/tag policy, and installable artifacts.
- Do not deploy or enable public indexing in this task.

## Acceptance Criteria

- [x] A fresh docs install on the pinned Node version can run every documented
      local and CI quality command.
- [x] A platform-neutral verification entry point runs docs lint, Astro/content
      checks, contract tests, preview and public static-build contracts,
      Playwright browser tests, and production-dependency audit.
- [x] Preview and public build modes remain mechanically distinct and covered by
      checks; public mode requires both the explicit gate and an HTTPS origin.
- [x] Cloudflare Pages build/root/output settings and environment-variable rules
      are documented without repository-specific values that do not exist.
- [x] The runbook contains deployment verification and dashboard rollback steps,
      with authoritative Cloudflare documentation links.
- [x] The release checklist blocks public indexing and install commands until
      domain, ownership, licensing, versioning, and artifact prerequisites are
      approved.
- [x] Existing 40-route bilingual parity, accessibility, search, responsive, and
      static-output checks continue to pass.

## Out of Scope

- Creating or authenticating a Cloudflare account or Pages project.
- Choosing or attaching a production custom domain.
- Publishing a release artifact, container image, package, or version tag.
- Adding install commands unsupported by a real artifact.
- Deploying the site or enabling search-engine indexing.

## Decisions

- 2026-09-08: Prepare the publication path but keep preview `noindex` and the
  installation page `planned` until external release inputs exist.
- 2026-09-08: Do not bind the repository to a CI provider while it has no remote
  or existing CI convention. Supply a local `npm` verification entry point that
  future CI can call unchanged.
