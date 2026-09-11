# Mendry

Mendry is a single-user, self-hosted Agent Harness for bounded model and tool
execution, traceable artifacts, and task-defined completion. Incident
investigation and code remediation are the existing default application
scenario, not limits on the core contracts.

The shared Agent Harness foundation under `backend/internal/modules/agentcore`
is implemented and focused-tested. Delivery remains staged: the account-free
Local path is still being validated, generic Service and Product surfaces are
planned, and the existing incident coordinator has only partially migrated to
shared execution mechanics. See the [Agent Harness documentation](./docs/src/content/docs/docs/concepts/agent-harness.mdx)
and [product status](./docs/src/content/docs/docs/project/status.mdx) for the
current boundary.

```text
frontend/  React and TypeScript incident application; generic run views planned
backend/   Go Agent Harness foundation and existing incident API
           (the Local composition source is still in validation)
docs/      Bilingual Astro and Starlight documentation site
```

Each package documents its own development and validation commands. The existing
incident API requires PostgreSQL, Redis, accounts, and project configuration;
those are application prerequisites, not dependencies of the neutral Harness
core.

The documentation package uses Node.js `22.19.0` and provides one reproducible
local/CI verification gate:

```bash
cd docs
npm ci
npm run verify
```

See the [documentation package guide](./docs/README.md) for focused commands
and build modes, or the [publication runbook](./docs/operations/publication.md)
for Cloudflare Pages settings, release checks, and rollback steps.
