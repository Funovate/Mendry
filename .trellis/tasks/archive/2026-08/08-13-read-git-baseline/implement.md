# Implement: read Git baseline from remote

1. Export decrypt on the project cipher and add `GetEncryptedSecret`.
2. Add `GitRefLister` plus `git ls-remote` adapter and parser tests.
3. Add `ProbeRepositoryRefs` and
   `POST /api/v1/projects/{projectKey}/repository/refs`.
4. Add frontend `api.probeRepositoryRefs` and Git-step probe UI.
5. Update E2E so branch/commit are read, not typed.
6. Update project and frontend specs.
7. Run backend `go test` for the touched packages and frontend lint,
   typecheck, unit, build, and E2E.
