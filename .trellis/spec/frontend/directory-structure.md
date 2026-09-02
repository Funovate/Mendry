# Directory Structure

## Production Graph

`src/main.tsx` is the only browser entry point. It imports the composed app and
`src/styles/production.css`. The production dependency graph is:

```text
main.tsx
  -> app/                 router, query client, context, root boundary
  -> layouts/             authenticated application shell
  -> features/<feature>/  route screen and feature-owned projections/styles
  -> shared/              reusable UI and presentation-only utilities
  -> api.ts               HTTP DTOs, schemas, errors, and request helper
```

`src/App.tsx`, `src/data.ts`, and `src/styles.css` are prototype references.
Production modules must not import them.

## Directory Layout

```text
src/
  app/
    App.tsx
    AppErrorBoundary.tsx
    context.tsx
    queries.ts
    query.ts
  features/
    auth/
    projects/
    incidents/
    observations/
    configuration/
    members/
    audit/
  layouts/
    AppShell.tsx
    AppShell.css
    ProjectSwitcher.tsx
  shared/
    format.ts
    ui.tsx
    ui.css
  styles/
    tokens.css
    base.css
    production.css
  api.ts
  main.tsx
```

## Ownership Rules

- `app/` composes routes and providers; it does not own business forms or copy API DTOs.
- A feature may import `api.ts`, `app/` query/context primitives, and `shared/`.
- `shared/` must not import a feature.
- `api.ts` is the sole HTTP DTO, runtime schema, request, and error owner.
- Route-specific CSS lives with its feature. Shell and shared controls own their CSS beside their TypeScript module.
- `styles/production.css` is an ordered import manifest only: tokens, base, shell, shared, then features.
- Keep prototype CSS isolated in `styles.css`; never import it from `main.tsx`.

## Naming And Examples

- Route components use `PascalCase` and end in `Page` or `Route`.
- Query hooks start with `use` and live in `app/queries.ts` only when shared across routes.
- Feature projections use descriptive lower-camel names, for example
  `buildSourceConfig` in `features/configuration/configuration.ts`.
- Use `features/incidents/IncidentsPage.tsx` as the route-owned selection example and
  `features/configuration/ConfigurationEditorPage.tsx` as the project-scoped mutation example.

## Forbidden Patterns

- Reintroducing a monolithic application component that owns several feature workflows.
- Importing `data.ts` or prototype roles into production code.
- Adding global feature selectors to `styles/base.css` or `shared/ui.css`.
- Defining a second request helper or backend DTO inside a feature.
