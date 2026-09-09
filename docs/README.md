# FixThe documentation

This package builds the bilingual FixThe documentation site as static files. It
does not import the React application or call the API at build time.

## Development and verification

The package pins Node.js `22.19.0` in [`.node-version`](./.node-version). From
this directory, install the lockfile and run the same platform-neutral gate that
a future CI provider can invoke:

```bash
npm ci
npm run verify
```

The gate runs formatting, Astro/content and route checks, Node contract tests,
the production dependency audit, a non-indexable preview build, Playwright
browser checks, and the public static-output contract. The focused commands are
available separately as `npm run lint`, `npm run check`, `npm run test`,
`npm run build`, and `npm run test:e2e`.

## Build modes

Preview builds are the default. They omit canonical and locale alternate URLs,
emit `noindex, nofollow`, disallow crawlers in `robots.txt`, and do not emit a
sitemap.

The public contract is deliberately separate and uses the reserved
`https://docs.fixthe.invalid` origin:

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
