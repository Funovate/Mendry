# Core Bilingual Operator Documentation Implementation Plan

## Preconditions

- [x] User reviews and approves the converged PRD, design, and this plan.
- [x] Curate `implement.jsonl` and `check.jsonl` with real docs/backend specs and
      parent research references.
- [x] Run `task.py start 09-06-docs-bilingual-operator-content` only after review.
- [x] Load `trellis-before-dev` and the docs quality guidelines before editing
      the site.

## 1. Establish Content Contracts

- [x] Add the availability, source, journey-order, and translation-key schema.
- [x] Replace the hard-coded minimal route map with the full paired structured
      inventory.
- [x] Add a checked-in English/Simplified Chinese terminology source.
- [x] Extend unit checks for counterpart metadata, missing citations, duplicate
      translation keys, invalid availability, and shell blocks on planned pages.

Validation:

```bash
npm --prefix docs run check
npm --prefix docs run test
```

Rollback point: restore the prior schema and eight-route inventory together;
do not leave only one locale on the new contract.

## 2. Write The Operator Journey

- [x] Rewrite paired docs overview and get-started overview pages around the
      source-evaluation versus production-installation distinction.
- [x] Add paired prerequisites and installation-gate pages.
- [x] Add paired administrator bootstrap and first-project procedures using
      verified migration, startup, login, membership, and component-save facts.
- [x] Add paired first-incident procedures using signed webhook ingress and
      current incident/remediation review behavior.
- [x] Link each step forward/back and add `Verified against` citations.

Review gate: execute every shell command that is safe in the current repository.
Commands requiring external services must be checked against tests/config and
clearly list prerequisites; do not fabricate output.

## 3. Write Concepts And Security Boundaries

- [x] Expand the paired architecture page.
- [x] Add paired data-model, lifecycle, and security pages.
- [x] Document project ownership, observation/incident/remediation distinctions,
      evidence provenance, state transitions, secrets/session storage,
      redaction, LLM/tool authority, and human merge/deployment control.
- [x] Reconcile terminology and availability across all concept pages.

## 4. Write Guides And Reference

- [x] Add paired Tencent CLS and signed-webhook guides with distinct provider,
      token, asynchronous processing, and failure semantics.
- [x] Add paired Git/baseline and LLM-provider guides with current capability and
      authority boundaries.
- [x] Add paired troubleshooting pages for startup, readiness, auth, webhook,
      configuration completeness, incident ingestion, and remediation evidence.
- [x] Add paired configuration, roles, and product-status references.
- [x] Ensure stored-but-not-executed connector settings and planned automation
      are never presented as working procedures.

## 5. Navigation, Search, And Generated Output

- [x] Replace the preview autogeneration sidebar with explicit localized groups
      and operator journey ordering.
- [x] Validate every internal link and generated counterpart.
- [x] Add Pagefind assertions for representative English and Chinese queries and
      locale-correct result URLs.
- [x] Preserve preview noindex behavior and static-only output.

## 6. Browser And Editorial Review

- [x] Extend Playwright through the full get-started sequence and representative
      concept, guide, reference, and planned installation pages.
- [x] Check desktop/mobile, light/dark, keyboard focus, heading hierarchy,
      code/table overflow, 320px reflow, and 200% zoom.
- [x] Run axe on representative conventional docs pages in both locales.
- [x] Perform a bilingual parity pass for titles, descriptions, labels, literal
      identifiers, commands, warnings, status, and source citations.
- [x] Confirm introduction links still resolve and shared styles have not
      regressed.

## 7. Quality Gate

Run:

```bash
npm --prefix docs run lint
npm --prefix docs run check
npm --prefix docs run test
npm --prefix docs run build
npm --prefix docs run test:e2e
npm --prefix docs audit --omit=dev
git diff --check
```

Also run a public-origin build with an approved placeholder custom HTTPS domain
to validate sitemap/canonical/alternate generation without publishing it.
Run GitNexus `detect_changes({scope: "compare", base_ref: "main"})` before commit.

## Verification Record

Completed implementation and verification on 2026-09-08 with Node 24.18.0:

- Added 19 paired English/Simplified Chinese operator pages (38 content routes)
  plus explicit localized Starlight navigation for overview, get started,
  concepts, guides, reference, and product status.
- Added required `availability`, `sources`, and `translationKey` frontmatter,
  optional journey ordering, checked-in terminology, source-path validation,
  paired metadata checks, and a planned-page shell-command prohibition.
- Kept production installation `planned` and non-executable. Source evaluation
  remains `preview`; current contracts and boundaries use `available` only where
  backed by code/tests.
- Corrected the first-project path after implementation review: webhook lookup
  requires an enabled source in the trigger environment, so the guide now saves
  environment and `push_ingestion` source components before the trigger.
- Removed runtime Google Fonts requests to comply with the static/privacy
  contract. Added `yaml@2.9.0`; the initially tried 2.8.1 was immediately
  upgraded after its moderate security advisory.
- `npm run lint`, `npm run check` (zero diagnostics), `npm run test` (5 tests),
  preview `npm run build`, `npm audit --omit=dev`, and `git diff --check` passed.
- Preview build generated 41 HTML pages; route/link/output contracts verified 40
  expected routes and Pagefind indexed all 41 pages.
- Public-mode build passed with
  `DOCS_PUBLIC_RELEASE=true PUBLIC_SITE_ORIGIN=https://docs.fixthe.example` and
  emitted `sitemap-index.xml` plus canonical/alternate metadata.
- Playwright passed 40 tests with 2 deliberate mobile-search skips. Coverage
  includes both locales, the complete journey, planned installation exclusion,
  localized Pagefind queries, light/dark axe checks, 320px reflow, and 200% CSS
  zoom.
- GitNexus impact returned UNKNOWN for docs symbols because the package was not
  in the active index. Package checks are the detailed regression authority.

## Completion Boundary

Archive this child when the complete paired route corpus, terminology/status
contracts, operator journey, search checks, and browser checks pass. Leave
Cloudflare Pages production promotion, authorized brand assets, public source
links, and replacement of the planned installation page to
`09-06-docs-production-readiness`.
