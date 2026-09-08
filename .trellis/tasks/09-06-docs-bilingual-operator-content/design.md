# Core Bilingual Operator Documentation Design

## Architecture And Boundaries

The existing Starlight content collection remains the rendering boundary. The
work adds MDX content, a docs-owned terminology/status contract, and static
validation scripts; it does not import frontend/backend runtime modules or add a
server adapter.

Content truth flows in one direction:

```text
backend code/tests + executable specs + release artifacts
                     |
                     v
       source inventory and availability decision
                     |
                     v
        paired English/Chinese Starlight pages
                     |
                     v
 route/content/link/search/build/browser validation
```

Product PRDs may provide context but cannot upgrade a claim to `available`.
When code and planning differ, current executable behavior wins and the planned
behavior is labeled separately.

## Route Topology

The paired route inventory is:

```text
/docs/
/docs/get-started/
/docs/get-started/prerequisites/
/docs/get-started/install/
/docs/get-started/bootstrap/
/docs/get-started/first-project/
/docs/get-started/first-incident/
/docs/concepts/architecture/
/docs/concepts/data-model/
/docs/concepts/lifecycle/
/docs/concepts/security/
/docs/guides/tencent-cls/
/docs/guides/signed-webhooks/
/docs/guides/git-baseline/
/docs/guides/llm-providers/
/docs/guides/troubleshooting/
/docs/reference/configuration/
/docs/reference/roles/
/docs/project/status/
```

Each has an identical `/zh-cn`-prefixed counterpart. Navigation uses explicit
localized groups for Get started, Concepts, Guides, Reference, and Project so
order is operator-oriented rather than filename-oriented.

## Content Contract

Extend the Starlight document schema with docs-owned frontmatter fields:

- `availability`: `available | preview | planned`;
- `sources`: non-empty repository-relative paths for operational pages;
- `journeyOrder`: optional integer for get-started sequencing;
- `translationKey`: locale-independent identity shared by each pair.

The validator parses the content collection source/frontmatter through a
structured parser or Astro content APIs. It must not infer metadata with regex
from rendered prose. It verifies:

- every inventory route maps to a source file;
- each translation key has exactly one English and one Chinese page;
- counterpart availability values agree;
- executable operator pages have at least one source citation;
- planned pages contain no fenced shell commands;
- internal content links target inventory routes.

A checked-in terminology module or data file maps stable English domain terms
to Simplified Chinese. Literal identifiers are explicitly marked as unchanged.
The content validator confirms required terms exist; editorial review confirms
natural usage.

## Availability Policy

- `available`: implemented and directly verified in current code/tests. This
  does not imply a supported production distribution.
- `preview`: implemented behavior usable for source-based evaluation but not yet
  backed by a supported customer release or complete production operations.
- `planned`: no executable supported path exists; prose describes the release
  gate and owning work only.

The installation page is `planned`. Source-based prerequisites/bootstrap and
first-journey pages are `preview`, even where their component APIs are
implemented, because the customer installation artifact is absent. Concepts and
references may be `available` when they describe current contracts without
claiming production support.

## Source And Citation Model

Operational pages include a short `Verified against` section listing stable
repository paths. Primary authorities include:

- `backend/README.md` and `backend/.env.example` for runnable commands/config;
- backend auth, project, incident, hook, remediation, logging, and adapter specs;
- current route handlers and tests where the README is incomplete;
- the parent documentation PRD/design for publication gates only.

Citations are maintenance pointers, not public source links, because the
repository has no configured public remote. The production-readiness task may
convert them to public links after a remote is approved.

## Journey Behavior

The get-started path distinguishes two lanes:

1. A source-evaluation lane with verified repository commands and explicit
   development/preview framing.
2. A production-installation lane that stops at the release gate and contains no
   speculative commands.

The first-incident guide chooses signed webhook ingress as the supported public
signal path. Authenticated observation creation may be documented only as a
connector/development fallback. Tencent CLS behavior is documented separately
because trusted provider detail and grouping have stricter evidence rules.

## Search And Navigation

Starlight Pagefind remains the only search engine. Generated-output tests query
representative English and Chinese terms and assert result URLs remain inside
the correct locale. No hosted search or analytics is added.

Sidebar configuration is explicit so English and Chinese labels, grouping, and
journey order do not depend on directory autogeneration. Mobile and desktop
browser tests exercise navigation through the journey rather than only checking
that pages render.

## Compatibility And Rollback

Existing introduction and route URLs remain unchanged. New schema fields are
added only to docs pages and all existing pages are migrated in the same change.
No redirects are needed.

Rollback can remove newly added pages and restore the prior eight-route
inventory/sidebar. The installation release gate must not be rolled back into an
executable placeholder. Frontend/backend packages remain untouched.

## Trade-Offs

- One content task keeps cross-locale terminology and availability review atomic;
  it is larger than splitting by section but avoids partially publishing an
  operator journey.
- Explicit route/sidebar inventories require maintenance, but they provide
  deterministic parity and order.
- Repository-path citations are less convenient than public links, but remain
  truthful until an authorized public remote exists.
- Preview labels are deliberately conservative: code availability does not equal
  supported customer deployment.
