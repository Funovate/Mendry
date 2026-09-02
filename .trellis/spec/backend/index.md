# Backend Development Guidelines

> Best practices for backend development in this project.

---

## Overview

This directory records the backend conventions established by the current Go
implementation. Deferred integrations remain explicit rather than being filled
with aspirational or unimplemented behavior.

---

## Guidelines Index

| Guide | Description | Status |
|-------|-------------|--------|
| [Directory Structure](./directory-structure.md) | Module organization, process and HTTP/config contracts | Established |
| [Database Guidelines](./database-guidelines.md) | PostgreSQL pool, typed query, transaction, migration, and test isolation conventions | Established |
| [Redis Guidelines](./redis-guidelines.md) | API session-store client, safe command telemetry, lifecycle, and test isolation | Established |
| [Authentication Guidelines](./authentication-guidelines.md) | Local users, bcrypt, Redis Sessions, cookies, auth routes, roles, and administrator bootstrap | Established |
| [Project Boundary Guidelines](./project-guidelines.md) | Project membership authorization, encrypted configuration, Event Stream, and audit contracts | Established |
| [Incident Guidelines](./incident-guidelines.md) | Incident domain, application, PostgreSQL repository, protected REST routes, and API contracts | Established |
| [Remediation Adapter Guidelines](./remediation-adapter-guidelines.md) | Frozen-port Git / SSH-log / OpenAI adapters, credential isolation, and composition-root loaders | Established |
| [Remediation Evidence Guidelines](./remediation-evidence-guidelines.md) | Evidence citations, confidence gates, source coverage, and bounded Docker runtime reads | Established |
| [Error Handling](./error-handling.md) | Error ownership, wrapping, and safe diagnostics | Established |
| [Quality Guidelines](./quality-guidelines.md) | Code, comment/documentation, testing, and validation gates | Established |
| [Logging Guidelines](./logging-guidelines.md) | Structured logging, event ownership, sensitive data | Established |

---

## How to Fill These Guidelines

For each guideline file:

1. Document your project's **actual conventions** (not ideals)
2. Include **code examples** from your codebase
3. List **forbidden patterns** and why
4. Add **common mistakes** your team has made

The goal is to help AI assistants and new team members understand how YOUR project works.

---

**Language**: All documentation should be written in **English**.
