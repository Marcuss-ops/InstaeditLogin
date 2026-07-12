.PHONY: dev stop seed test lint backend-test frontend-test test-integration \
        run-api run-worker run-migrate

# Start the full local development stack modeled on Blocco #2.1's
# production-true topology: 3 services (api + worker + migrate) plus
# the legacy `server` profile for users who still want the single-process
# shape. See docker-compose.yml for the service definitions.
#
# Blocco #2.1 NOTE: `make dev` no longer starts the pre-split single-bundle
# dev shape. The 3-service production topology IS the new dev default.
# For the legacy single-process shape, use `make run-server` (local)
# or `docker compose --profile legacy up` (container).
dev:
	docker compose up --build

# Stop the development stack
stop:
	docker compose down

# Apply development seed data (requires a running database and .env.dev)
seed:
	go run cmd/seed/main.go

# ──────────────────────────────────────────────────────────────────
# Blocco #2.1: individual-binary run targets. Useful when iterating
# against a remote DB (e.g. staging) — run cmd/migrate once, then
# `make run-api` and `make run-worker` in separate terminals.
# Each target is independent; they assume the .env.dev file has been
# populated (same shape as docker-compose).
# ──────────────────────────────────────────────────────────────────

# One-shot pre-deploy: connect + apply pending migrations + exit.
run-migrate:
	go run ./cmd/migrate

# HTTP server only (cmd/api). No workers spawned.
run-api:
	go run ./cmd/api

# 5 background goroutines only (cmd/worker). No HTTP server.
run-worker:
	go run ./cmd/worker

# Legacy single-bundle wrapper (cmd/server). RUN_WORKERS=false disables
# workers for HTTP-only debugging. Default true (matches docker-compose
# `server` profile).
run-server:
	RUN_WORKERS=true go run ./cmd/server

# Same wrapper, HTTP-only mode (RUN_WORKERS=false)
run-server-api-only:
	RUN_WORKERS=false go run ./cmd/server

# Run all tests (Go + frontend)
test: backend-test frontend-test

# Run Go tests with race detection (unit only — no Docker required)
backend-test:
	go test -race ./...

# Run integration tests against real ephemeral containers via
# testcontainers-go. Requires Docker on the runner (GitHub-hosted
# ubuntu-latest has it; local `make test-integration` needs a Docker
# daemon). Distinct from `backend-test` so `make test` stays portable
# (no Docker surprise on dev laptops). The integration command covers:
#   - internal/database      — migration tests on testcontainer
#                              postgres:17-alpine.
#   - internal/worker        — PublishWorker + ReconcileWorker
#                              two-goroutine pipeline tests on
#                              testcontainer postgres:17-alpine +
#                              real httptest.Server for the TikTok
#                              wire.
#   - internal/testutil/redis — smoke test (PING/SET/GET roundtrip)
#                              on testcontainer redis:7-alpine,
#                              validating the runtime abstraction
#                              works for non-SQL backends.
# The runtime package's unit tests (WaitReady + WaitReadyMatch)
# run under `go test -race ./...` via the `backend-test` target — no
# integration tag needed.
# This Makefile target is the canonical command invoked by
# .github/workflows/integration.yml — if you change the command here,
# CI follows automatically.
test-integration:
	go test -tags=integration -v -timeout 10m ./internal/database/... ./internal/worker/... ./internal/testutil/redis/...


# Run frontend lint, tests and build
frontend-test:
	cd web && npm ci && npm run lint && npm run test && npm run build

# Run formatters and linters
lint:
	gofmt -w .
	go vet ./...
	cd web && npm run lint
