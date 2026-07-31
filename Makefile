.PHONY: dev stop seed lint lint-check backend-test test-integration \
        run-api run-worker run-migrate run-server run-server-api-only \
        docker-build-migrate-only \
        docker-build-local-api docker-build-local-worker \
        ops-smoke ops-isolation ops-isolation-dry-run \
        verify-log-redaction

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
	docker compose --env-file .env.dev up --build

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

# Run all Go tests
test: backend-test

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
# .github/workflows/integration-fast.yml — if you change the command here,
# CI follows automatically.
test-integration:
	go test -tags=integration -v -timeout 10m ./internal/database/... ./internal/worker/... ./internal/testutil/redis/...

# Task 9/10: end-to-end pipeline suite. Spins up Postgres via
# testcontainers-go + boots in-process httptest fakes for Drive /
# YouTube / Velox. Drives the 7-scenario acceptance matrix pinned
# by the source document (Drive ingest 201/no-dupes, crash+resume,
# Velox idempotency, S3 verify, schedule gate, YouTube crash+resume,
# Velox callback).
#
# Requires Docker on the runner. Local dev: install Docker
# Desktop or use `make test-integration` which is hermetic.
# CI: runs in the dedicated `e2e` job in
# .github/workflows/integration-fast.yml so the fast unit PR gate stays
# untouched. ~5-15 s of container spin-up runs once per suite.
#
# Build tag `e2e` is required because tests/e2e/*.go is gated
# behind `//go:build e2e` — `go test ./...` won't see them at all.
test-e2e:
	go test -tags=e2e -timeout 15m -v ./tests/e2e/...


# Run formatters and linters
#
# `make lint` is the DEVELOPER-friendly shape: it AUTO-FIXES gofmt
# (-w) and re-runs the lints. Convenience for local iteration.
#
# `make lint-check` is the CI-friendly shape: gofmt CHECKS and FAILS
# on unformatted files (no -w), identical to the gate in
# .github/workflows/integration-fast.yml. Use this in pre-commit hooks and
# other CI surfaces where mutation is wrong.
#
# The canonical CI command remains `make lint-check` so PRs that
# ship with unformatted Go files block instead of silently rewriting
# the working tree on the runner.
lint:
	gofmt -w .
	go vet ./...

# CI-friendly variant: FAIL on unformatted Go files (no -w).
# The check mirrors the gate inside .github/workflows/integration-fast.yml
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

# Build the migrate-only stage (one-shot pre-deploy; also baked into
# the production stage above so release_command resolves ./migrate).
docker-build-migrate-only:
	docker build --target migrate -t instaedit-migrate .

# Local-dev single-process Docker builds (NOT used by Fly).
docker-build-local-api:
	docker build --target api -t instaedit-api .

docker-build-local-worker:
	docker build --target worker -t instaedit-worker .

# ───────────────────────────────────────────────────────────────────
# Blocco #5.3: Operator-side observability + log-privacy assurance.
#
# `make verify-log-redaction` wraps `./scripts/obs/verify-log-redaction.sh --apply`
# which sshes to the VPS (instaedit@$VPS_IP, default 51.91.11.36 per
# docs/OPERATIONS.md §1.1) and runs `docker compose logs --since 1h
# api worker` into a chmod-700 tempdir, then greps against the 7
# canonical privacy-contract patterns documented in docs/OPERATIONS.md
# §4.3 + docs/DEPLOY.md §7.6. Use this (a) after every VPS deploy
# (`git pull && docker compose up -d --build`) to confirm a fresh
# rollout hasn't regressed the redaction discipline, and (b) weekly
# as a regression tripwire. Exit codes propagate: 0 = clean /
# 1 = hit / 2 = missing tool (ssh/docker/grep/awk) / 3 = VPS
# unreachable via ssh OR compose stack down / 4 = bad args. The
# script MUST NEVER print actual matched secrets to stdout -- only
# sanitized 80-char prefixes + ***redacted***.
#
# Override env vars: VPS_IP, VERIFY_LOG_SERVICES (default "api worker"),
# COMPOSE_DIR (default /opt/instaedit/InstaeditLogin). Override args:
# --since / --timeout (the latter is the background-fetch ceiling;
# default 60s fits a warm VPS; bump to 120 for cold scans or wider
# --since windows). The script's --since auto-translates `Nd` -> `Nh`
# (e.g. `--since 7d` -> `168h`) so existing operator muscle memory
# still works after the cutover.
# ───────────────────────────────────────────────────────────────────
verify-log-redaction:
	@if [[ ! -x ./scripts/obs/verify-log-redaction.sh ]]; then \
		echo "❌ scripts/obs/verify-log-redaction.sh not found or not executable"; \
		echo "   Run: chmod +x scripts/obs/verify-log-redaction.sh"; \
		exit 1; \
	fi
	./scripts/obs/verify-log-redaction.sh --apply

# ────────────────────────────────────────────────────────────────────────
# Blocco #5.1: Post-deploy operator runbooks.
#
# `make ops-smoke` runs the comprehensive Phase 9 sub-1-5+7 end-to-end
# verification against https://api.instaedit.org. Read-only by default.
# Set APPLY_PUBLISH=1 (env) before the make call to actually trigger a
# real publish + poll (still non-destructive on the workspace).
#
# `make ops-isolation` runs Phase 9 sub-6 — creates 2 fresh users,
# asserts cross-tenant boundaries across 4 endpoints, CASCADE-deletes
# test users on EXIT (success OR failure). Requires DATABASE_URL on the
# operator machine for the cleanup.
#
# `make ops-isolation-dry-run` previews the full plan + cleanup SQL
# without mutating. Use this BEFORE the real run to verify the script
# will hit the expected endpoints with the expected test-user suffix.
#
# Both targets are BSD bash-portable, have NO Go dependency, can run on
# laptops without the dev Docker stack. They cross-reference docs/DEPLOY.md
# §5 and docs/OPERATIONS.md §3.
# ────────────────────────────────────────────────────────────────────────
ops-smoke:
	@if [[ ! -x ./scripts/ops/post_deploy_smoke.sh ]]; then \
		echo "❌ scripts/ops/post_deploy_smoke.sh not found or not executable"; \
		echo "   Run: chmod +x scripts/ops/post_deploy_smoke.sh"; \
		exit 1; \
	fi
	./scripts/ops/post_deploy_smoke.sh

ops-isolation:
	@if [[ ! -x ./scripts/ops/workspace_isolation_test.sh ]]; then \
		echo "❌ scripts/ops/workspace_isolation_test.sh not found or not executable"; \
		echo "   Run: chmod +x scripts/ops/workspace_isolation_test.sh"; \
		exit 1; \
	fi
	./scripts/ops/workspace_isolation_test.sh

ops-isolation-dry-run:
	@if [[ ! -x ./scripts/ops/workspace_isolation_test.sh ]]; then \
		echo "❌ scripts/ops/workspace_isolation_test.sh not found or not executable"; \
		echo "   Run: chmod +x scripts/ops/workspace_isolation_test.sh"; \
		exit 1; \
	fi
	./scripts/ops/workspace_isolation_test.sh --dry-run
