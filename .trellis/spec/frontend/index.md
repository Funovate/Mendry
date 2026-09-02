# Frontend Development Guidelines

> Best practices for frontend development in this project.

---

## Overview

This directory contains guidelines for frontend development. Fill in each file with your project's specific conventions.

---

## Guidelines Index

| Guide | Description | Status |
|-------|-------------|--------|
| [Directory Structure](./directory-structure.md) | Production graph, feature ownership, and stylesheet boundaries | Active |
| [Component Guidelines](./component-guidelines.md) | Route components, shared UI, capability gates, and accessibility | Active |
| [Hook Guidelines](./hook-guidelines.md) | TanStack Query ownership, cancellation, and mutation updates | Active |
| [State Management](./state-management.md) | Project-scoped server state and authorization capabilities | Active |
| [Quality Guidelines](./quality-guidelines.md) | Build and route-mocked E2E quality gates | Active |
| [Type Safety](./type-safety.md) | Typed API boundary and DTO ownership | Active |

---

## Pre-Development Checklist

Before changing production frontend code:

1. Read [Directory Structure](./directory-structure.md) before adding modules or styles.
2. Read [State Management](./state-management.md) and [Hook Guidelines](./hook-guidelines.md) before reading or mutating server state.
3. Read [Type Safety](./type-safety.md) before changing an HTTP DTO or request.
4. Read [Component Guidelines](./component-guidelines.md) before adding route or shared UI components.
5. Read [Quality Guidelines](./quality-guidelines.md) before completing the change.

## Quality Check

- Production imports start at `src/main.tsx` and must not reach prototype fixtures.
- Every project resource query key contains the stable project key.
- Authorization uses backend `capabilities`; role labels are informational.
- API responses are validated in `src/api.ts` before components receive them.
- Run lint, type-check, unit tests, build, E2E, and `git diff --check`.

---

**Language**: All documentation should be written in **English**.
