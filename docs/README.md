# Mendry documentation

This package builds the bilingual Mendry documentation site as static files. It
does not import the React application or call the API at build time.

Source repository: [Funovate/Mendry](https://github.com/Funovate/Mendry).
The documentation source lives in the repository's `docs/` directory.

## Development and verification

The package pins Node.js `22.19.0` in [`.node-version`](./.node-version). From
this directory, install the locked dependencies and the Chromium browser used by
the desktop and mobile tests:

```bash
npm ci
npx playwright install chromium
```

On Linux machines missing browser system libraries, use
`npx playwright install --with-deps chromium` instead. Then run the verification gate:

```bash
npm run verify
```

The gate runs formatting, Astro/content and route checks, Node contract tests,
the production dependency audit, a non-indexable preview build, Playwright
browser checks, and the public static-output contract. The dependency audit
requires network access. The gate restores a non-indexable preview build after
the public contract check, including when an earlier check fails. The focused
commands are available separately as `npm run lint`, `npm run check`,
`npm run test`, `npm run build`, and `npm run test:e2e`.

For local editing, run `npm run dev` and open the URL printed by Astro
(normally `http://localhost:4321`). English documentation is under `/docs/`;
Chinese documentation is under `/zh-cn/docs/`.

## Updating documentation

- Edit English pages in `src/content/docs/docs/` and their Chinese counterparts in `src/content/docs/zh-cn/docs/`. Keep `translationKey`, `availability`, and cited `sources` aligned with the implemented behavior.
- For a new page, add both language files, a sidebar entry in `astro.config.mjs`, and a route entry in `scripts/site-contract.mjs`. Update the expected route counts in `scripts/site-contract.test.mjs`.
- Add relevant links from the documentation overview, feature map, configuration reference, or product status so readers can discover a new feature.
- Run `npm run check`, `npm test`, and `npm run build` to check content metadata, locale parity, generated pages, and internal links. Run `npm run verify` for the full formatting, browser, and publication checks.

The incident-application setup lives in the [root README](../README.md) and
[backend README](../backend/README.md); the commands in this package only build
and serve documentation.

## Brand assets

`public/brand/` contains copies of the supplied assets from the repository's
`logo/` directory; usage guidance is in `../logo/mendry-brand-guide.md`.
The header and homepage footer switch between the original and reversed
horizontal logos with the site theme. The console uses the reversed mark on its
dark background. The icon tile supplies the SVG favicon and PNG touch icon.
When updating the brand, copy the corresponding source files into this directory.

## Build modes

Preview builds are the default. They omit canonical and locale alternate URLs,
emit `noindex, nofollow`, disallow crawlers in `robots.txt`, and do not emit a
sitemap.

The public contract is deliberately separate and uses the reserved
`https://www.mendry.net` origin:

```bash
npm run build:public
```

This command is a static contract test only; it does not deploy, attach a
custom domain, or enable indexing. A real Pages production build may set
`DOCS_PUBLIC_RELEASE=true` and an approved HTTPS `PUBLIC_SITE_ORIGIN` only
after the release checklist is approved. See the
[publication runbook](./operations/publication.md) for the required settings,
smoke checks, and rollback procedure.

## Cloudflare Pages

Pages uses the `docs` root directory, `npm run build` as the build command, and
`dist` as the output directory. The tracked `.node-version` file pins the Pages
build image to Node.js `22.19.0`. This package remains static and does not add
Pages Functions or runtime service dependencies.
