.PHONY: dev stop seed test lint backend-test frontend-test test-integration

# Start the full local development stack (Postgres + backend + frontend)
dev:
	docker compose up --build

# Stop the development stack
stop:
	docker compose down

# Apply development seed data (requires a running database and .env.dev)
seed:
	go run cmd/seed/main.go

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
# run under `go test -race ./...` via the `backend-test` target —
# no integration tag needed.
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
