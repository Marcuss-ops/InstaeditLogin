.PHONY: dev stop seed test lint lint-check backend-test frontend-test test-integration \
        run-api run-worker run-migrate run-server run-server-api-only \
        docker-build-production docker-build-migrate-only \
        docker-build-local-api docker-build-local-worker \
        fly-deploy fly-verify fly-help

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
# WORKER_HEALTH_PORT defaults to "0" (off) so this does NOT bind
# 9090 on dev laptops — see cmd/worker/health_listener.go.
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
#
# `make lint` is the DEVELOPER-friendly shape: it AUTO-FIXES gofmt
# (-w) and re-runs the lints. Convenience for local iteration.
#
# `make lint-check` is the CI-friendly shape: gofmt CHECKS and FAILS
# on unformatted files (no -w), identical to the gate in
# .github/workflows/integration.yml. Use this in pre-commit hooks and
# other CI surfaces where mutation is wrong.
#
# The canonical CI command remains `make lint-check` so PRs that
# ship with unformatted Go files block instead of silently rewriting
# the working tree on the runner.
lint:
	gofmt -w .
	go vet ./...
	cd web && npm run lint

# CI-friendly variant: FAIL on unformatted Go files (no -w).
# The check mirrors the gate inside .github/workflows/integration.yml
# exactly. Run in pre-commit; CI uses the same command.
lint-check:
	@UNFORMATTED=$$(gofmt -l .); \
	if [ -n "$$UNFORMATTED" ]; then \
		echo "::error::unformatted Go files (run 'gofmt -w .' then re-push):"; \
		echo "$$UNFORMATTED"; \
		echo; \
		echo "── gofmt -d (preview of changes) ──"; \
		gofmt -d . | head -200; \
		exit 1; \
	fi
	@echo "✓ gofmt clean"
	go vet ./...
	cd web && npm run lint

# ────────────────────────────────────────────────────────────────────────
# Blocco #4.1: Fly.io deployment.
#
# ONE Fly app (`instaedit-login`), ONE fly.toml, TWO process groups
# (api + worker). The Dockerfile `[production]` stage bundles
# /app/api + /app/worker + /app/migrate into a single image; fly.toml
# [processes] picks the per-process entrypoint binary. Shared [env]
# + per-process [processes.X.env] for scoped vars.
#
# - release_command = "./migrate": applies pending migrations in a
#   fresh release machine with the SAME image before any new
#   api/worker VM rolls out.
# - min_machines_running = 1: always-at-least-one VM alive; no
#   scale-to-zero (Blocco #4.1 contract).
# - independent health checks: api [[services.http_checks]] on
#   /api/v1/health; worker [[services.tcp_checks]] on /WORKER_HEALTH_PORT
#   (cmd/worker/health_listener.go binds a tiny accept-and-close loop).
#
# `make docker-build-production` builds the Fly target. Two local-dev
# Docker targets (`--target api`, `--target worker`) remain for
# docker-compose / one-off debugging.
# ────────────────────────────────────────────────────────────────────────

# Build the unified Fly image (production stage). Same shape Fly's
# deploy pipeline uses. Cold cache: one image. Warm cache:
# incremental.
docker-build-production:
	docker build --target production -t instaedit-fly .

# Build the migrate-only stage (one-shot pre-deploy; also baked into
# the production stage above so release_command resolves ./migrate).
docker-build-migrate-only:
	docker build --target migrate -t instaedit-migrate .

# Local-dev single-process Docker builds (NOT used by Fly).
docker-build-local-api:
	docker build --target api -t instaedit-api .

docker-build-local-worker:
	docker build --target worker -t instaedit-worker .

# `flyctl deploy` wrapper. Runs migrations via release_command, then
# rolls api + worker process groups independently under Fly's
# rolling strategy.
fly-deploy:
	flyctl deploy --config fly.toml

# Local Fly.toml validator: parses fly.toml, prints the app name +
# Dockerfile build target + [processes] entries + release_command +
# min_machines_running + env surface counts + per-process health
# checks. No API calls — pure-shell parsing. Run after editing
# fly.toml to catch typos before `fly deploy`.
fly-verify:
	@echo "──── fly.toml (Blocco #4.1 unified app) ────────────────────────"
	@echo "App name:           $$(grep -E '^app *=' fly.toml | head -1 | sed 's/^app *= *//;s/\"//g')"
	@echo "Dockerfile:         $$(grep -E '^  *dockerfile' fly.toml | head -1 | sed 's/^ *//')"
	@echo "Build target:       $$(grep -E '^  *build_target' fly.toml | head -1 | sed 's/^ *//')"
	@echo "[processes] entries:$$(awk '/^\\[processes\\]/,/^$/' fly.toml | grep -E '^ *api *=|^ *worker *=' | sed 's/^ *//' | tr '\n' '; ')"
	@echo "release_command:    $$(grep -E '^  *release_command' fly.toml | head -1 | sed 's/^ *//')"
	@echo "min_machines_running:$$(grep -E '^  *min_machines_running' fly.toml | head -1 | sed 's/^ *//')"
	@echo "auto_stop_machines: $$(grep -E '^  *auto_stop_machines' fly.toml | head -1 | sed 's/^ *//')"
	@echo ""
	@echo "[env] shared keys:            $$(awk '/^\\[env\\]/,/^\\[/' fly.toml | grep -cE '^[A-Z_]+ *=')"
	@echo "[processes.api.env] keys:     $$(awk '/^\\[processes\\.api\\.env\\]/,/^\\[/' fly.toml | grep -cE '^[A-Z_]+ *=')"
	@echo "[processes.worker.env] keys:  $$(awk '/^\\[processes\\.worker\\.env\\]/,/^\\[/' fly.toml | grep -cE '^[A-Z_]+ *=')"
	@echo ""
	@echo "Health checks:"
	@echo "  api (http):  $$(grep -A4 'services.http_checks' fly.toml | grep -E 'path|interval|grace' | head -3 | sed 's/^ *//' | tr '\n' '; ')"
	@echo "  worker (tcp):$$(grep -A4 'services.tcp_checks' fly.toml | grep -E 'interval|grace' | head -2 | sed 's/^ *//' | tr '\n' '; ')"

# Print a quickstart cheat-sheet for the Fly deploy shape.
fly-help:
	@echo "── One-time setup ─────────────────────────────────────"
	@echo "  flyctl apps create instaedit-login"
	@echo "  flyctl secrets set DATABASE_URL=... ENCRYPTION_KEYS=... \\"
	@echo "       JWT_SECRET=... S3_ACCESS_KEY=... S3_SECRET_KEY=... \\"
	@echo "       EMAIL_PROVIDER_KEY=... STRIPE_WEBHOOK_SECRET=..."
	@echo ""
	@echo "── Pre-deploy dry-run ──────────────────────────────────"
	@echo "  make fly-verify"
	@echo ""
	@echo "── Deploy ─────────────────────────────────────────────"
	@echo "  make fly-deploy"
	@echo ""
	@echo "── What fly.toml does ─────────────────────────────────"
	@echo "  · Builds Dockerfile [production] (api+worker+migrate)."
	@echo "  · Runs release_command = './migrate' (idempotent)."
	@echo "  · Rolls api (HTTP :8080, http_checks /api/v1/health)"
	@echo "    + worker (tcp_checks :9090) process groups."
	@echo "  · Keeps >= 1 VM alive per group (min_machines_running=1)."
