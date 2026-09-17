GO ?= go
SQLC_VERSION ?= $(shell cd tools && $(GO) list -m -f '{{.Version}}' github.com/sqlc-dev/sqlc)
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BIN_DIR ?= bin

LDFLAGS := -X mendry/backend/internal/platform/buildinfo.Version=$(VERSION) \
	-X mendry/backend/internal/platform/buildinfo.Commit=$(COMMIT) \
	-X mendry/backend/internal/platform/buildinfo.BuildDate=$(BUILD_DATE)

.PHONY: fmt vet test test-race build build-agentcore-local build-mcp-workspace build-check check generate generate-check run-api run-migrate bootstrap-admin seed

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

build:
	mkdir -p $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/api ./cmd/api
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/migrate ./cmd/migrate
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/bootstrap-admin ./cmd/bootstrap-admin
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/seed ./cmd/seed
	$(GO) build -o $(BIN_DIR)/agentcore-local ./cmd/agentcore-local
	$(GO) build -o $(BIN_DIR)/mcp-workspace ./examples/mcp-workspace

build-agentcore-local:
	mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/agentcore-local ./cmd/agentcore-local

build-mcp-workspace:
	mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/mcp-workspace ./examples/mcp-workspace

build-check:
	$(GO) build ./cmd/...

generate:
	cd tools && $(GO) run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION) generate -f ../sqlc.yaml

generate-check:
	@set -e; \
	generated_dirs="internal/commands/migrate/migratedb internal/modules/auth/adapter/postgres/authdb internal/modules/incidents/adapter/postgres/incidentdb internal/modules/projects/adapter/postgres/projectdb internal/modules/observations/adapter/postgres/observationdb internal/modules/remediation/adapter/postgres/remediationdb"; \
	before="$$(find $$generated_dirs -type f -name '*.go' ! -name doc.go -exec sha256sum {} + | sort)"; \
	$(MAKE) generate; \
	after="$$(find $$generated_dirs -type f -name '*.go' ! -name doc.go -exec sha256sum {} + | sort)"; \
	test "$$before" = "$$after" || { echo "sqlc generated files were stale"; exit 1; }

check: fmt vet test test-race build-check

run-api:
	$(GO) run ./cmd/api

run-migrate:
	$(GO) run ./cmd/migrate

bootstrap-admin:
	@test -n "$$USERNAME" || { echo "USERNAME is required"; exit 1; }; \
	$(GO) run ./cmd/bootstrap-admin --username "$$USERNAME"

seed:
	$(GO) run ./cmd/seed
