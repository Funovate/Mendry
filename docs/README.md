# FixThe documentation

This package builds the bilingual FixThe documentation site as static files.
It does not import the React application or call the API at build time.

```bash
npm ci
npm run dev
npm run check
npm run build
npm run test:e2e
```

Preview builds omit a canonical origin and emit `noindex, nofollow`. A public
build requires both an approved HTTPS custom domain and an explicit release
gate:

```bash
DOCS_PUBLIC_RELEASE=true PUBLIC_SITE_ORIGIN=https://docs.example.com npm run build
```

Cloudflare Pages uses `npm run build`, the `dist` output directory, Node.js
22.12 or newer, and the repository root directory `docs`.
