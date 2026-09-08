# Core Bilingual Operator Documentation

## Goal

Publish the paired English and Simplified Chinese operator documentation needed
to evaluate the current FixThe implementation, configure a project, ingest a
supported signal, and review an incident and remediation result without reading
source code. Keep unsupported installation and automation behavior visibly
separate from executable guidance.

## Background

- The independent Astro/Starlight package and bilingual introduction are now
  implemented under `docs/`.
- `backend/README.md`, backend executable contracts, tests, and Trellis specs are
  the authority for current runtime behavior. Product planning documents may
  explain intent but do not prove availability.
- Source-based development startup, explicit migrations, administrator
  bootstrap, local sessions, project membership, component configuration,
  signed webhook ingress, project incidents, and remediation review exist.
- A tested customer installation artifact, production support statement, and
  public deployment package do not exist. The installation route must remain a
  non-executable release-gate page.
- SSH, cloud, and MCP source configuration can be stored, but configuration
  alone does not prove every connector executes. Automatic merge, deployment,
  rollback, and autonomous recovery remain outside the current authority
  boundary.

## Required Route Set

Create and maintain an English route and a `/zh-cn/` counterpart for each page:

- documentation overview and get-started overview;
- prerequisites, installation status, administrator bootstrap, first project,
  and first incident;
- architecture, data model, incident/remediation lifecycle, and security;
- Tencent CLS, signed webhooks, Git/baseline, LLM providers, and
  troubleshooting;
- configuration, roles, and product status reference.

English routes remain under `/docs/*`; Simplified Chinese routes remain under
`/zh-cn/docs/*`. Existing route URLs must remain valid.

## Content Requirements

- Organize the operator journey in dependency order: prerequisites, release
  status, migration/startup context, administrator bootstrap, authentication,
  first project, supported signal setup, first incident, and review.
- Use `available`, `preview`, and `planned` consistently. Executable commands
  must be backed by the current repository or a tested artifact. Planned
  behavior must never appear inside an executable procedure.
- Keep the installation page blocked and non-executable until the production
  readiness task receives a tested customer installation artifact.
- Cite authoritative repository paths near operational claims and commands so a
  feature owner can revalidate them when behavior changes.
- Document PostgreSQL as business source of truth and Redis as disposable
  session storage. Document explicit migrations, stable encryption-key
  handling, HTTPS requirements for secure cookies, request/body boundaries,
  and local logging/telemetry behavior.
- Explain system administrator and project `admin`, `operator`, and `viewer`
  roles without conflating system and project authorization.
- Document signed webhook ingress as the supported public signal path,
  including token confidentiality, generic and Tencent CLS provider behavior,
  asynchronous processing, and safe failure boundaries. Do not claim HMAC is
  required when the implemented token contract does not require it.
- Describe Git baseline, evidence provenance, deterministic gates, optional
  configured remote LLM behavior, and human-controlled merge/deployment using
  the current implementation boundaries.
- Keep API fields, status values, environment variables, paths, command names,
  provider names, and code identifiers literal. Maintain a checked-in bilingual
  terminology source for stable domain translations.
- Use task-oriented prose, short procedures, tables, asides, and code blocks.
  Avoid marketing claims, exhaustive REST reference, and card-heavy layouts.
- Provide localized titles, descriptions, navigation labels, status labels,
  link text, and alt text. English and Chinese pages must be updated together.

## Validation Requirements

- Extend structured route inventory and parity checks to cover every required
  page and fail on a missing counterpart.
- Add a content contract that validates required availability metadata and
  authoritative source references for operator pages.
- Extend generated-link validation and Pagefind checks with representative
  English and Chinese operational queries.
- Add browser checks for the full get-started path, representative concept and
  guide pages, localized navigation/search, code overflow, heading structure,
  keyboard focus, mobile reflow, and both themes.
- Keep preview metadata non-indexable and preserve the current static-only,
  analytics-free package contract.

## Acceptance Criteria

- [x] Every required route exists in English and Simplified Chinese with
      localized metadata, navigation, labels, and reciprocal links.
- [x] A reader can complete every currently supported source-based evaluation
      step from prerequisites through first incident and remediation review
      without undocumented repository knowledge.
- [x] The installation page clearly blocks production installation guidance and
      contains no invented image, Compose, package, or deployment command.
- [x] Every executable command and major behavior claim is traceable to current
      code, tests, configuration, or a tested release artifact.
- [x] Availability labels correctly separate implemented behavior, preview-only
      workflows, and planned capabilities across both locales.
- [x] Terminology, literal identifiers, commands, status values, and security
      boundaries are consistent between locales.
- [x] Route parity, content contracts, internal links, Pagefind English/Chinese
      queries, Astro checks/build, accessibility, and desktop/mobile Playwright
      checks pass.
- [x] Conventional documentation pages remain readable at 320px and 200% zoom,
      with no horizontal document overflow or broken Starlight navigation.

## Out Of Scope

- Creating the missing customer deployment artifact or publishing production
  installation commands.
- Changing frontend/backend behavior to make a guide possible.
- Exhaustive REST API reference, all connector/provider documentation,
  versioned docs, release notes, analytics, or a contributor portal.
- Claiming automatic merge, deployment, rollback, recovery, or unrestricted
  model/tool authority.

## External Dependency

Production installation content remains blocked on the production-readiness
child task and its tested installation, support, security, and rollback inputs.
This dependency does not block preview publication of the rest of the operator
corpus.
