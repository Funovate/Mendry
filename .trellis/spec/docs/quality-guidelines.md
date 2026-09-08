# Documentation Quality Guidelines

## Scenario: Static Documentation Build And Release Gate

### 1. Scope / Trigger

Apply this contract to changes under `docs/`, especially routes, locale content,
metadata, media, Cloudflare Pages files, dependencies, and build scripts.

The docs site is a standalone static package. It must not add an Astro server
adapter, Pages Function, hosted search, analytics, remote font, visitor tracking,
or runtime import from `frontend/` or `backend/`.

### 2. Signatures

The supported package commands are:

```text
npm run dev       local Astro development server
npm run lint      Prettier verification
npm run check     Astro type/content check plus locale route parity
npm run test      Node contract tests
npm run build     static build plus generated-output contract checks
npm run test:e2e  static build plus Playwright browser checks
```

Cloudflare Pages builds from `docs/` with `npm run build`, publishes `dist/`,
and uses Node `>=22.12.0`.

### 3. Contracts

| Input                                      | Requirement                             | Output behavior                                                                  |
| ------------------------------------------ | --------------------------------------- | -------------------------------------------------------------------------------- |
| `DOCS_PUBLIC_RELEASE` absent or not `true` | Preview mode                            | No canonical URL; `noindex, nofollow`; `robots.txt` disallows `/`                |
| `DOCS_PUBLIC_RELEASE=true`                 | `PUBLIC_SITE_ORIGIN` is required        | Canonical, locale alternates, absolute Open Graph image, and sitemap are emitted |
| `PUBLIC_SITE_ORIGIN`                       | HTTPS custom origin only in public mode | Origins ending in `.pages.dev` are rejected                                      |

English is the default locale. Simplified Chinese uses `zh-CN` and the
`/zh-cn/` prefix. Operator pages require `availability`, non-empty `sources`,
and a shared `translationKey`; get-started pages may add `journeyOrder`. The
route validator parses YAML frontmatter, verifies each cited repository path,
requires paired metadata to agree, and rejects shell code blocks on `planned`
pages. Keep stable translations in `docs/src/content/terminology.json` and keep
API fields, environment variables, paths, commands, statuses, and provider
identifiers literal.

Each committed product image must have a record in
`docs/src/assets/media.yml` covering source, capture date, dimensions,
synthetic-data status, sensitive-data review, staleness review, and availability.

### 4. Validation & Error Matrix

| Condition                                                | Required result                               |
| -------------------------------------------------------- | --------------------------------------------- |
| Public release without `PUBLIC_SITE_ORIGIN`              | Build fails before output                     |
| Public release with HTTP origin                          | Build fails before output                     |
| Public release with `*.pages.dev` origin                 | Build fails before output                     |
| Preview output contains a canonical                      | Build contract fails                          |
| A generated page links to a missing internal page        | Build contract reports source page and target |
| Locale counterpart source is absent                      | `npm run check` fails                         |
| Product image is missing or unloadable                   | Build or Playwright fails                     |
| Desktop, 390px, or 320px document overflows horizontally | Playwright fails                              |

### 5. Good / Base / Bad Cases

- Good: `DOCS_PUBLIC_RELEASE=true PUBLIC_SITE_ORIGIN=https://docs.example.com npm run build`
  emits indexed metadata and a sitemap for the approved custom domain.
- Base: `npm run build` produces a non-canonical, non-indexable preview with
  local Pagefind output.
- Bad: `DOCS_PUBLIC_RELEASE=true PUBLIC_SITE_ORIGIN=https://preview.pages.dev npm run build`
  fails because a transient Pages hostname cannot become the canonical origin.

### 6. Tests Required

- Unit: locale counterpart mapping, static route mapping, and internal-link
  extraction including query and fragment removal.
- Build: every expected route, every generated internal page link, reviewed
  media, `_headers`, `_redirects`, robots metadata, and `robots.txt`.
- Browser: both introduction locales, the complete paired get-started journey,
  representative conventional operator pages in both themes, localized
  Pagefind results, planned-page command exclusion, landmarks, H1, theme and
  locale controls, reciprocal switching, image loading, 404 status, preview
  metadata, axe with zero violations, and no horizontal overflow.
- Reflow: Simplified Chinese at 320px with reduced motion; assert the media
  query, automatic scroll behavior, bounded transition duration, and no
  document overflow.
- Dependency changes: run `npm audit --omit=dev`; inspect advisories rather than
  applying a forced audit fix.

### 7. Wrong vs Correct

#### Wrong

```bash
DOCS_PUBLIC_RELEASE=true PUBLIC_SITE_ORIGIN=https://project.pages.dev npm run build
```

This attempts to promote a transient preview hostname to the permanent origin.

#### Correct

```bash
# Default preview: deliberately non-indexable and non-canonical.
npm run build

# Public release: explicit gate plus approved HTTPS custom domain.
DOCS_PUBLIC_RELEASE=true PUBLIC_SITE_ORIGIN=https://docs.example.com npm run build
```

## Content And Media Boundaries

- Use text-only `FixThe` branding until authorized logo and favicon source files
  and usage rights exist. Do not invent replacement brand assets.
- Describe installation as preview/readiness information until a tested customer
  installation artifact is released.
- State that deterministic operation does not require a remote LLM, optional
  remote analysis receives redacted context, and merge/deploy/recovery conclusions
  remain human-controlled.
- Capture product media from current production components with stable synthetic
  fixtures. Do not publish credentials, private endpoints, personal/customer
  identifiers, or obsolete prototype behavior.

## Custom Introduction Styling

- Wrap custom introduction content in `class="intro-shell not-content"` to use
  Starlight's Markdown opt-out. Keep introduction selectors unlayered so the
  framework's unlayered reset cannot erase explicit heading and spacing rules.
  Scope narrow header adaptations with `html:has(.intro-shell)`.
- Pair accent backgrounds with `--fx-on-accent`; pair inverse bands with
  `--fx-inverse-surface` and `--fx-on-inverse`. White text on the dark theme's
  light `--fx-ink` value is invalid. Run axe in both themes.
- For intrinsically sized media use `width: 100%; height: auto` along with HTML
  width/height. The height attribute must not leave an oversized image box when
  the Markdown opt-out removes the framework's responsive image rule.
- Keep captions and section labels in `IntroContent` for both locales. Verify
  actual first-viewport kicker bounds; `toBeVisible()` alone permits content
  below the viewport.
- `tests/refinement.spec.ts` checks both themes/locales, image aspect ratio,
  caption localization, 200% CSS zoom overflow, and architecture navigation.
  At narrow widths retain accessible native theme/language selectors; action
  text must shrink and wrap independently from its fixed-size icon.
