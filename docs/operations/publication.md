# Cloudflare Pages publication runbook

This runbook prepares the static `docs/` package for a later Cloudflare Pages
release. It is not a deployment authorization and does not make the preview
public. Until the release checklist is approved, keep previews non-indexable and
keep the installation page `planned`.

Track repository-verified facts and unresolved external inputs in the
[publication release-input record](./release-inputs.md). That record is a
release dependency log, not an approval to deploy, attach DNS, or enable
indexing.

## Package gate

Run the same platform-neutral gate locally and from any future CI provider:

```bash
cd docs
npm ci
npm run verify
```

The pinned Node.js line is `22.19.0`, recorded in [`docs/.node-version`](../.node-version).
The package engine and the Pages build image must remain compatible with that
line. `npm ci` is the clean-install boundary; `npm run verify` then runs
formatting, Astro/content and route checks, Node contract tests, the production
dependency audit, a preview build, Playwright browser checks, and the reserved
public-build contract.

The public contract builds canonical metadata and a sitemap for the approved
`https://www.mendry.net` origin. This is a static contract test only; it does not
deploy or attach a custom domain.

## Pages project settings

Create or modify a Pages project only after the release checklist is approved.
The source repository is [Funovate/Mendry](https://github.com/Funovate/Mendry).
Keep placeholders for account ownership, project name, and production branch
until those inputs are supplied:

| Pages setting          | Value                                |
| ---------------------- | ------------------------------------ |
| Account                | `<CLOUDFLARE_ACCOUNT>`               |
| Project name           | `<PAGES_PROJECT>`                    |
| Connected repository   | `https://github.com/Funovate/Mendry` |
| Production branch      | `<PRODUCTION_BRANCH>`                |
| Root directory         | `docs`                               |
| Build command          | `npm run build`                      |
| Build output directory | `dist`                               |
| Node version           | `22.19.0` from `.node-version`       |

The `www.mendry.net` build contract is not a deployment authorization. A Pages
production build must still use the approved environment variables and release
checklist below.

The `docs` package is static Astro output. Do not add Pages Functions, an
adapter, a runtime API dependency, or a hosted search service as part of
publication. The committed `public/_headers` and `public/_redirects` files are
copied to `dist/` and are interpreted by Pages for static responses.

Cloudflare's current references for these settings are:

- [Pages build configuration](https://developers.cloudflare.com/pages/configuration/build-configuration/)
- [Pages build image and version overrides](https://developers.cloudflare.com/pages/configuration/build-image/)
- [Astro on Pages](https://developers.cloudflare.com/pages/framework-guides/deploy-an-astro-site/)
- [Pages headers](https://developers.cloudflare.com/pages/configuration/headers/)
- [Pages redirects](https://developers.cloudflare.com/pages/configuration/redirects/)

### Environment variables

Set variables by Pages environment, not in committed provider configuration:

| Environment                       | `DOCS_PUBLIC_RELEASE` | `PUBLIC_SITE_ORIGIN`     |
| --------------------------------- | --------------------- | ------------------------ |
| Local preview                     | absent or not `true`  | absent                   |
| Pages preview                     | absent or not `true`  | absent                   |
| Pages production, before approval | absent or not `true`  | absent                   |
| Pages production, after approval  | `true`                | `https://www.mendry.net` |

`PUBLIC_SITE_ORIGIN` must be the approved HTTPS custom origin, without a port,
path, query, or fragment. A `pages.dev` hostname is never an acceptable
canonical origin. Do not put the reserved `.invalid` test origin in the production
variables. Cloudflare Pages supports pinning Node with either the tracked
`.node-version` file or the `NODE_VERSION` environment variable; use the file
as the source of truth and set `NODE_VERSION=22.19.0` in the Pages dashboard
only when an explicit dashboard override is required.

The production gate must remain disabled until every external release input is
approved. A production build with the gate disabled stays non-indexable and has
no canonical or sitemap claim.

## Preview verification

Every preview deployment must be treated as an evaluation surface. Check the
Pages deployment URL before sharing it:

- `/` and `/zh-cn/` return `200`, show the localized introduction, render the
  semantic execution model, and do not request an external runtime asset.
- `/docs/` and `/zh-cn/docs/` return `200`; representative concept, guide,
  reference, and get-started routes return `200` in both locales.
- `/docs` and `/zh-cn/docs` follow the committed trailing-slash redirects.
- `/does-not-exist/` returns the localized `404` surface with a `404` status.
- Pagefind assets load and localized search returns a result.
- `meta[name="robots"]` is `noindex, nofollow`; no page emits a canonical or
  locale alternate URL; `robots.txt` contains `Disallow: /` and no `Sitemap:`.
- The `pages.dev` response retains `X-Robots-Tag: noindex` from `_headers`.
- `Content-Security-Policy`, `Referrer-Policy`, `X-Content-Type-Options`, and
  `X-Frame-Options` are present on static responses.
- Browser checks pass for accessibility, responsive layouts, theme and locale
  controls, the semantic execution model, and no horizontal overflow.

Record the deployment URL, commit, build result, and these HTTP checks in the
release record. A failed preview is not a release candidate.

## Production promotion

Only promote after the release checklist below is complete and `npm run verify`
has passed from a clean install on Node `22.19.0`.

1. Confirm the Pages project uses `https://github.com/Funovate/Mendry`,
   `<PRODUCTION_BRANCH>`, root `docs`, build command `npm run build`, and output
   `dist`.
2. Confirm the production environment alone has
   `DOCS_PUBLIC_RELEASE=true` and the approved
   `PUBLIC_SITE_ORIGIN=https://www.mendry.net`.
3. Build the candidate from the approved commit and inspect the Pages build log
   for a zero exit status and the expected `dist` output.
4. Attach the approved custom domain in the Workers & Pages dashboard under
   the Pages project: Custom domains > Set up a domain. Configure the required
   DNS record or nameservers only after the domain owner approves the change.
5. Verify the production URL and canonical metadata before enabling any search
   engine submission or other indexing workflow. This task does not perform
   that enablement.

Cloudflare's custom-domain procedure is documented in [Custom
domains](https://developers.cloudflare.com/pages/configuration/custom-domains/).
A custom CNAME alone is not sufficient; associate the domain with the Pages
project through the dashboard first.

## Production smoke checks

Run the same route and asset checks against `https://www.mendry.net`. The public
build contract is a local static-output test; it does not prove that the domain
is attached to the intended Pages deployment:

- All 40 expected routes (38 operator pages plus 2 introduction pages) return
  `200`.
- Canonical URLs use the approved origin exactly; `en`, `zh-CN`, and `x-default`
  alternates point to the reciprocal routes.
- `robots.txt` allows crawling and references
  `https://www.mendry.net/sitemap-index.xml`; the sitemap index and its
  child sitemap contain only the approved origin.
- The response is indexable only after the explicit release gate is approved;
  `X-Robots-Tag: noindex` must remain on `pages.dev` preview responses.
- The 404 status, three committed redirects, Pagefind search, local media,
  semantic execution model, CSP and other security headers, and both locale/theme browser paths work.
- No response contains an unexpected external script, font, image, or runtime
  service request.

Keep the HTTP response headers and the tested commit with the release record.

## Rollback

If a production deployment fails smoke checks or introduces incorrect content,
stop promotion and use the Pages dashboard:

1. Open **Workers & Pages** and select `<PAGES_PROJECT>`.
2. Open **Deployments > All deployments**.
3. Find the last verified production deployment.
4. Open its three-dot actions menu and select **Rollback to this deployment**.
5. Confirm the rollback, then repeat the production smoke checks above.

Cloudflare states that a successfully built production deployment is a valid
rollback target and preview deployments are not. See the official [Pages
rollbacks](https://developers.cloudflare.com/pages/configuration/rollbacks/)
procedure. DNS, domain ownership, and release-artifact decisions remain manual
and are not changed by rollback.

## Release checklist

Do not set `DOCS_PUBLIC_RELEASE=true`, attach a custom domain, submit the site
to search engines, or publish installation commands until every item is checked:

- [x] The approved static-build origin is recorded as `https://www.mendry.net`;
      domain ownership and Pages attachment remain separate unchecked inputs.
- [ ] Cloudflare account ownership and operator access are recorded as
      `<CLOUDFLARE_ACCOUNT>` and `<PAGES_PROJECT>`.
- [x] The source repository is recorded as `https://github.com/Funovate/Mendry`.
- [ ] The production branch is recorded as `<PRODUCTION_BRANCH>`.
- [ ] Repository license, authorized brand assets, and attribution obligations
      are approved and recorded.
- [ ] Version and tag policy, changelog ownership, and release commit policy are
      approved.
- [ ] A tested, versioned, installable distribution artifact exists, with
      documented upgrade, support, security, and rollback boundaries.
- [ ] Production installation instructions are reviewed against that artifact;
      no unsupported image, package, Compose command, or shell block has been
      added to the `planned` installation page.
- [ ] `npm ci && npm run verify` passes on Node `22.19.0`, and the candidate
      commit is recorded.
- [ ] Preview and production smoke checks pass, with canonical and sitemap
      output reviewed against the approved domain.
- [ ] A final release owner approves indexing, domain activation, and the
      publication record.

Until the artifact and support inputs exist, the installation page remains
`planned` and the docs package remains a non-indexable preview.
