# Load local .env for backend commands

## Goal

Local backend commands can start from a gitignored `backend/.env` without
manually exporting every `FIXTHE_*` variable. Existing process environment
still wins, and production/CI binaries keep working when no file is present.

## Background

`make run-api`, `make run-migrate`, `make seed`, and `make bootstrap-admin`
all call `go run ./cmd/...`. Each command injects `os.LookupEnv` and never
reads a file. `backend/.env.example` documents keys; a local `backend/.env`
already exists and is gitignored. Operators currently have to `set -a; source
.env; set +a` before Make.

## Requirements

- R1. Running `make run-api`, `make run-migrate`, `make seed`, or
  `make bootstrap-admin` from `backend/` applies assignments from a present
  `.env` in the current working directory.
- R2. A missing `.env` is not an error. Process environment and documented
  defaults remain the only sources.
- R3. A variable already present in the process environment is not overwritten
  by `.env`.
- R4. `FIXTHE_BOOTSTRAP_ADMIN_PASSWORD` in `.env` is visible to
  `bootstrap-admin` the same way other keys are visible to API/migrate/seed.
- R5. Parse or read failures name the file and line/field, never the raw
  secret value.
- R6. Configuration tests continue to inject `Lookup` and do not need a real
  process environment or a repo-root `.env`.
- R7. `backend/README.md` documents copying `.env.example` to `.env` and
  starting commands from `backend/`.

## Acceptance Criteria

- [x] `cd backend && make run-api` uses values from a local `.env` when those
      keys are unset in the shell.
- [x] `cd backend && make run-migrate`, `make seed`, and
      `make bootstrap-admin USERNAME=...` use the same file overlay.
- [x] Exporting a key in the shell still overrides the same key in `.env`.
- [x] Starting a command with no `.env` in cwd behaves as today.
- [x] Invalid `.env` syntax fails startup without printing assignment values.
- [x] Unit tests cover overlay precedence, missing file, comments/blank
      lines/quoted values, and rejected malformed lines.
- [x] README states the cwd `.env` convention.

## Out of Scope

- Makefile `include .env`
- `joho/godotenv` or other new production-module dependencies
- `.env.local` / multi-file cascade
- Walking parent directories or loading `backend/.env` from the repo root
- Variable interpolation (`$VAR`, `$(...)`)
- Mutating the process environment with `os.Setenv`
