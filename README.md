# FixThe

FixThe is organized as a single repository with independently buildable
frontend, backend, and documentation packages.

```text
frontend/  React and TypeScript application
backend/   Go API and operational migration command
docs/      Astro and Starlight documentation site
```

Each package documents its own development and validation commands.

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
