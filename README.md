# FixThe

FixThe is organized as a single repository with independently buildable
frontend, backend, and documentation packages.

```text
frontend/  React and TypeScript application
backend/   Go API and operational migration command
docs/      Astro and Starlight documentation site
```

Each package documents its own development and validation commands.

The documentation package requires Node.js 22.12 or newer:

```bash
cd docs
npm ci
npm run dev
npm run check
npm run build
```
