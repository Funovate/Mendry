# Implementation Plan

1. Add the hooks application normalization contract and deterministic fallback helpers.
2. Add model-backed analyzer parsing, redaction, bounded prompts, canonical grouping hash, and fallback behavior.
3. Add the hooks LLM adapter bridge and wire it to the existing OpenAI client in `bootstrap.RunAPI`.
4. Update webhook service ordering so body/token validation stays synchronous while normalization and persistence run in the background; add failure reporting and tests proving the HTTP response does not wait for the model.
5. Fix PostgreSQL debug pointer formatting and add regression coverage.
6. Update backend specs to describe the new inbound contract and safety boundary.
7. Run formatting, unit/race tests, vet, build, generation checks, GitNexus change detection, and final quality review.

## Validation

From `backend/`:

```bash
gofmt -w <changed Go files>
go test ./internal/modules/hooks/... ./internal/platform/postgres/...
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/...
make generate-check
```

## Risk Controls

- Keep analyzer optional at the service boundary and always fallback on model errors.
- Do not write incident/observation persistence changes until the normalized contract is validated.
- Keep the existing plain-text fingerprint behavior to avoid silently changing existing incident grouping.
- Verify no raw webhook payload is introduced into application logs.
- Run impact analysis before editing changed exported symbols and inspect direct callers.
