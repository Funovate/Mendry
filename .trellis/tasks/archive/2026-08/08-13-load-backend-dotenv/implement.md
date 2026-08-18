# Implement: local .env overlay

1. Add `backend/internal/platform/config/dotenv.go` with
   `WithOptionalDotEnv` and an unexported parser.
2. Add `dotenv_test.go` covering missing file, precedence, comments, quotes,
   empty values, malformed lines, and no raw value in errors.
3. Wire `cmd/api`, `cmd/migrate`, `cmd/seed`, and `cmd/bootstrap-admin`.
4. Update `backend/README.md` command/config section.
5. Update backend spec: directory-structure process contract and quality
   env-key / test notes.
6. Validate with `cd backend && make check`.
