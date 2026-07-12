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

# Run integration tests against real ephemeral Postgres containers
# via testcontainers-go. Requires Docker on the runner (GitHub-hosted
# ubuntu-latest has it; local `make test-integration` needs a Docker
# daemon). Distinct from `backend-test` so `make test` stays portable
# (no Docker surprise on dev laptops). The integration command covers
# BOTH internal/database (migration tests) and internal/worker
# (PublishWorker + ReconcileWorker two-goroutine pipeline tests).
# This Makefile target is the canonical command invoked by
# .github/workflows/integration.yml — if you change the command here,
# CI follows automatically.
test-integration:
	go test -tags=integration -v -timeout 10m ./internal/database/... ./internal/worker/...


# Run frontend lint, tests and build
frontend-test:
	cd web && npm ci && npm run lint && npm run test && npm run build

# Run formatters and linters
lint:
	gofmt -w .
	go vet ./...
	cd web && npm run lint
