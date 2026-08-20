# Technical Design: Incident Console Prototype

## Delivery Shape

Build a standalone React and TypeScript static prototype backed only by local
fixture data. It has no authentication backend, API, database, connector, or
LLM dependency. Navigation and local controls are interactive so the reviewer
can assess workflow and information hierarchy.

## Visual System

The console is a compact, quiet operational interface with a light neutral
surface, dark readable text, restrained borders, and a small set of semantic
status colors. Use familiar iconography for navigation and utilities, compact
tables for scanning, and plain status markers for severity/lifecycle. Avoid
marketing composition, decorative gradients, nested cards, and production
write affordances.

## Information Architecture

```text
Project / environment switcher
  Incidents
    Incident list -> Incident detail
      Overview | Evidence | Report | Remediation | Activity
  Event stream
  Sources
    Project setup wizard
      SCM provider -> repository -> write-only credential -> production branch
      -> deployed commit -> evidence scope
  Notification policies
  Audit
```

The default list uses `real-estate / production / cls-backend`. It includes an
open `Info` incident based on the language-validator error, a recovered
incident, muted policy state, occurrence trends, and a visible notification
state. The detail view separates observed facts, hypotheses, missing evidence,
verification steps, and remediation. Evidence rows expose connector, query,
time, scope, and retention metadata.

The remediation view shows a simulated, automated AI result: the resolved
production baseline, restricted `hotfix/*` branch, cited patch rationale,
configured test result, and draft PR. It makes clear that hotfix creation and
draft PR creation are automated within that scope, while only a human can
approve a production-branch merge. The prototype never contacts Yunxiao or
creates a real branch or PR.

The prototype includes three expanded review surfaces. A guided project setup
wizard reduces configuration to a sequence of validated choices with inferred
defaults: SCM provider, repository, write-only Git HTTP(S) or SSH transport
credential reference, production
branch, immutable deployed commit, and read-only evidence scope. Yunxiao is
the representative default fixture, not a hard-coded UI dependency; GitHub
Enterprise, GitLab, and generic HTTPS Git appear as provider choices. An
evidence drawer shows a representative redacted
source log along with query parameters, time window, integrity hash, and the
diagnosis statements that cite it. A remediation rationale surface makes the
connection from facts and code context to patch explicit, including rejected
alternatives, a scoped diff, configured test command/results, and the
remaining production-merge approval boundary.

Evidence configuration is separate from Git configuration. The prototype
models a connector catalog for cloud logs over API, cloud logs over MCP, and
local SSH log collection. Trigger policies independently combine signed
webhooks and custom rules; all selected paths deduplicate into the same
evidence and incident model.

## Interaction Model

- Sidebar navigation changes views; mobile replaces the fixed sidebar with a
  menu control.
- Selecting a list row opens its detail view; local tabs show overview,
  evidence, report, remediation, and activity data.
- Filters and status controls visibly update local list data.
- A role switch demonstrates admin, operator, and viewer restrictions without
  simulating real authentication.
- Settings views show source, notification, and provider metadata only; secret
  values and write-to-production controls do not exist.
- Configuration and evidence controls update local state only. Buttons that
  mimic validation, query replay, baseline resolution, and patch inspection
  reveal local fixture results and audit context; they do not make requests.
- Credential fields model write/replace only. The UI clears the entered value
  after local save and then renders only a secret reference, provider, and
  least-privilege permission summary.
- Git transport credentials are independent of optional provider APIs for
  release/deployment provenance or draft-PR metadata. The prototype does not
  imply an API access token is required to clone, fetch, branch, or push.

Project creation treats Git repository and transport credential as mandatory
foundation. It does not use fixed starter bundles for signals: administrators
can combine SSH logs, cloud logs, and MCP as independent read-only sources,
then combine signed webhooks and custom rules as independent trigger modes.
The final review lists every selected source and trigger, and verification is
cleared whenever the composition changes.

## Responsive Behavior

Desktop favors a dense split list/detail workflow. Small screens use one
column, a compact header, horizontally scrollable data tables only where
necessary, and a detail back action. Fixed controls retain stable dimensions;
text must wrap or truncate predictably rather than overlap.
