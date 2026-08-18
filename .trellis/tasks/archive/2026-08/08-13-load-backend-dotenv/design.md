# Design: local .env overlay

## Decision

Load `.env` in the command composition path by wrapping `config.Lookup`.
Do not teach Make to source the file, and do not add a dotenv library.

## Why this boundary

- `make run-api` and `go run ./cmd/api` / `bin/api` share one path.
- `bootstrap-admin` reads `FIXTHE_BOOTSTRAP_ADMIN_PASSWORD` before
  `RunBootstrapAdmin`; a Lookup wrapper can serve both.
- Spec forbids hidden `os.Getenv` outside the command/composition path and
  forbids tests that mutate process environment when a Lookup is available.
- Make `include` breaks on `#`, `$`, and spaces inside URLs/passwords.

## Contract

```go
func WithOptionalDotEnv(base Lookup, path string) (Lookup, error)
```

- `path` is `.env` relative to the process cwd.
- Missing file: return `base` unchanged.
- Present file: parse assignments, then `lookup(key)` returns process/base
  value when set, otherwise the file value.
- Empty assignment (`KEY=`) is a present empty value for keys not already set.
- Errors wrap as `configuration <path>:<line> <reason>` or
  `configuration <path> <reason>` and never include the raw value.

## File syntax

Support the subset already used by `.env.example` and the local `.env`:

- UTF-8 text, optional BOM
- blank lines
- full-line `#` comments, with optional leading whitespace
- `KEY=VALUE`
- optional single or double quotes around VALUE
- trimmed key whitespace
- empty VALUE

Reject:

- missing `=`
- empty or non-`[A-Za-z_][A-Za-z0-9_]*` keys
- inline comments
- `export KEY=VALUE`
- newlines inside quoted values

## Command wiring

Each `cmd/*/main.go` builds:

```go
lookup, err := config.WithOptionalDotEnv(os.LookupEnv, ".env")
```

`bootstrap-admin` uses that lookup for the password and for
`bootstrap.Options.Lookup`.

Do not call `os.Setenv`. Later accidental `os.Getenv` stays blind to file
values, which matches the existing "no hidden env reads" rule.

## Docs and spec

- `backend/README.md`: copy `.env.example` to `.env`, run commands from
  `backend/`, note that exported shell values win.
- `.trellis/spec/backend/directory-structure.md` and
  `quality-guidelines.md`: command lookup may overlay cwd `.env`; tests still
  inject Lookup and never depend on a real file unless they pass a temp path.
