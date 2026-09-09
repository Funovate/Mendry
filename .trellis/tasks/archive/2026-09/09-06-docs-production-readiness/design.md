# Production Publication Readiness Design

## Boundary

This task turns the existing static docs package into a reproducible publication
candidate. It does not create Cloudflare resources, select a domain, publish an
artifact, or make the site indexable.

## Verification Contract

`npm ci` remains the clean-install boundary. A new package-level `npm run
verify` command composes existing focused commands in this order:

1. formatting;
2. Astro/content and route checks;
3. Node contract tests;
4. production dependency audit;
5. preview build plus Playwright tests;
6. public-mode static build against `https://docs.fixthe.invalid`.

The reserved `.invalid` origin exercises canonical, alternate, robots, and
sitemap output without representing a deployable production identity. The
command must not contact an application API or Cloudflare account.

## Build Modes

Preview remains the default. It has no Astro `site`, emits `noindex, nofollow`,
omits canonical metadata, and disallows crawlers in `robots.txt`. Cloudflare
`pages.dev` responses additionally retain `X-Robots-Tag: noindex` through
`_headers`.

Public mode remains opt-in through both `DOCS_PUBLIC_RELEASE=true` and an HTTPS
`PUBLIC_SITE_ORIGIN` that is not under `pages.dev`. Its build contract verifies:

- indexable robots metadata;
- canonical URLs rooted at the configured origin;
- reciprocal English and Simplified Chinese alternate links;
- an allowing `robots.txt` that references the sitemap;
- generated sitemap output.

## Operations Documentation

`docs/operations/publication.md` is the provider-facing runbook and source of
truth for Pages project settings, preview checks, production prerequisites,
promotion verification, rollback, and the release checklist. `docs/README.md`
keeps the short contributor entry points and links to the runbook.

The runbook uses Cloudflare Dashboard terminology and links to current official
Pages build, headers, redirects, custom-domain, and rollback documentation.
Project/account/domain values remain explicit placeholders.

## Failure And Rollback

Any verification failure blocks publication. A preview can be deleted or left
non-indexable. A bad production deployment is rolled back in Cloudflare Pages
Deployments to the last verified production deployment, followed by the same
HTTP smoke checks. DNS/custom-domain or artifact decisions are not automated by
this repository.
