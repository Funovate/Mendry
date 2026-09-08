# Documentation Development Guidelines

> Executable contracts for the independently built Astro and Starlight site.

## Overview

The `docs/` package owns the static bilingual documentation site. It is built
and released independently from `frontend/` and `backend/` and must not import
their runtime modules.

## Guidelines Index

| Guide                                         | Description                                                          | Status |
| --------------------------------------------- | -------------------------------------------------------------------- | ------ |
| [Quality Guidelines](./quality-guidelines.md) | Static build, locale, preview, release, media, and browser contracts | Active |

## Pre-Development Checklist

1. Read [Quality Guidelines](./quality-guidelines.md) before changing routes,
   metadata, release configuration, media, or browser tests.
2. Keep English and Simplified Chinese route counterparts synchronized.
3. Run GitNexus impact analysis before editing an indexed symbol. New untracked
   docs symbols may report `UNKNOWN`; record that limitation and inspect the
   package checks directly.

## Quality Check

Run the complete gate from `docs/`:

```bash
npm run lint
npm run check
npm run test
npm run build
npm run test:e2e
npm audit --omit=dev
cd ..
git diff --check
```

---

**Language**: Code-spec documentation is written in **English**. Product pages
may provide explicit English and Simplified Chinese content.
