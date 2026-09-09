# Production Publication Readiness Implementation Plan

## 1. Reproducible Package Gate

- [x] Add focused scripts for production audit and public-mode build validation.
- [x] Add one `npm run verify` command that composes every required package gate.
- [x] Pin the local/Pages Node line in a tracked version file.

Validation:

```bash
cd docs
npm ci
npm run verify
```

Rollback point: package scripts and Node version file can be reverted without
changing generated content.

## 2. Public Build Contract

- [x] Extend static output checks for public canonical and locale alternate URLs.
- [x] Require public `robots.txt` to allow crawling and reference the sitemap.
- [x] Require generated sitemap output in public mode and keep it absent from
      preview mode.
- [x] Add focused Node tests for build-mode URL expectations where practical.

Validation:

```bash
cd docs
npm run build
DOCS_PUBLIC_RELEASE=true PUBLIC_SITE_ORIGIN=https://docs.fixthe.invalid npm run build
```

Rollback point: contract changes do not alter runtime routes and can be reverted
independently from the runbook.

## 3. Cloudflare Pages Runbook

- [x] Document root, build command, output directory, Node version, and release
      environment variables.
- [x] Document preview and production HTTP smoke checks, including metadata,
      assets, localized routes, redirects, search, and security headers.
- [x] Document production promotion and Dashboard rollback using official
      Cloudflare Pages references.
- [x] Add an explicit release checklist for domain, ownership, repository remote,
      license, tag/version policy, artifacts, support/security policy, and final
      approval.
- [x] Link the runbook and unified verification command from `docs/README.md`.

Validation:

```bash
npm --prefix docs run lint
npm --prefix docs run check
```

## 4. Final Review

- [x] Run the clean-install verification command on the pinned Node line.
- [x] Run `git diff --check`.
- [x] Run GitNexus staged change detection and confirm no unexpected execution
      flows.
- [x] Confirm preview remains non-indexable and installation remains `planned`
      with no shell blocks.
- [x] Record the verification result before commit.

## Verification Record

- `docs/.node-version` pins `22.19.0`. `npm ci` passed on Node `v22.19.0`
  with npm `10.9.3`; the clean install emitted no engine warning for the
  resolved dependency graph.
- Focused `npm run lint`, `npm run check`, and `npm run test` passed. The Node
  contract suite now passes 14 tests, including Playwright config default,
  boundary, dynamically allocated, and invalid `PLAYWRIGHT_PORT` loading
  cases; missing-origin, HTTP, pages.dev bare/subdomain/trailing-dot,
  credentials, port, path, and pre-output rejection cases; plus preview
  restoration/error-preservation cases. `npm run build`, `npm run build:public`,
  and `npm run audit:production` also passed.
- `npm run verify` passed on the pinned Node line: formatting, Astro/content and
  route checks, 14 Node contract tests, `npm audit --omit=dev` (0
  vulnerabilities), preview build contracts, 54 Playwright tests with 52
  passed and 2 expected mobile Pagefind skips, and the public contract at
  `https://docs.fixthe.invalid`. The verification command then rebuilt and
  checked preview output after the public contract; the final `docs/dist`
  contains 40 verified routes, `noindex, nofollow` metadata, no canonical
  links, no sitemap files, and a disallow-all `robots.txt`.
- Public-origin validation now runs while Astro loads `astro.config.mjs`, so
  invalid values fail before Astro writes the requested output. The wrappers
  invoke `process.execPath` with package-resolved JavaScript entrypoints rather
  than platform-specific `npm.cmd` or `astro.cmd` shims.
- The publication runbook has valid Markdown emphasis and describes the 40
  expected routes accurately as 38 operator pages plus 2 introduction pages.
  The existing runbook URL checks resolved to HTTP 200 after retries for build
  configuration, build image, Astro on Pages, headers, redirects, custom
  domains, and rollbacks.
- `git diff --check` passed for the docs/task scope. GitNexus impact was LOW
  for the indexed docs symbols checked (`expectedRoutes` had no upstream
  dependents; `readHtml` had one direct `check-build.mjs` caller), and UNKNOWN
  for the new unindexed production scripts. Staged change detection was LOW
  with zero affected execution flows; unrelated concurrent work remained
  outside the staged boundary.
- Final review fix: `PLAYWRIGHT_PORT` is now validated before interpolation into
  the Playwright web-server command. Only canonical decimal integers from 1
  through 65535 are accepted; the default remains 4321 and verify's dynamically
  allocated port is covered by the config-loading tests. Root `README.md` now
  points contributors from Node.js 22.19.0 and `npm ci`/`npm run verify` to the
  docs guide and publication runbook.
- A temporary archive of the staged Git index passed `npm ci && npm run verify`
  independently on Node `22.19.0`: 14 Node tests, 50 Playwright passes, 2
  expected mobile Pagefind skips, and a final restored preview with no sitemap
  or canonical URL. Review was performed with `home/gpt-5.6-luna` at maximum
  thinking and found no remaining blocking issue.
- No deployment, Cloudflare authentication, public indexing, commit, or archive
  was performed during implementation.
