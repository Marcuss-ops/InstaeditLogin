# InstaEditLogin — Multi-stage Dockerfile (Blocco #2.1 / Blocco #4.1)
#
# Targets:
#   api         — HTTP server only (cmd/api). Local-dev single-process shape.
#   worker      — 5 background goroutines only (cmd/worker). Local-dev single-process.
#   migrate     — one-shot pre-deploy migration (cmd/migrate).
#   production  — UNIFIED bundle for Fly.io (Blocco #4.1). Ships
#                 /app/api + /app/worker + /app/migrate in ONE image;
#                 fly.toml [processes] picks which binary runs. This
#                 is what Fly builds in production.
#   server      — legacy single-bundle wrapper (cmd/server) for dev / Railway.
#
# Build:
#   docker build --target api         -t instaedit-api         .
#   docker build --target worker      -t instaedit-worker      .
#   docker build --target migrate     -t instaedit-migrate     .
#   docker build --target production  -t instaedit-fly         .   # Fly.io target
#   docker build --target server      -t instaedit-server      .   # legacy single-process
#
# Default target (when no --target is supplied): api.

# ────────────────────────────────────────────────────────────────────────
# Stage 1: Builder — compile all 4 binaries from a single source tree.
# ────────────────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux \
    go build -ldflags="-s -w" -o /out/api     ./cmd/api     && \
    CGO_ENABLED=0 GOOS=linux \
    go build -ldflags="-s -w" -o /out/worker  ./cmd/worker  && \
    CGO_ENABLED=0 GOOS=linux \
    go build -ldflags="-s -w" -o /out/migrate ./cmd/migrate && \
    CGO_ENABLED=0 GOOS=linux \
    go build -ldflags="-s -w" -o /out/server  ./cmd/server

# ────────────────────────────────────────────────────────────────────────
# Stage 2: Base — alpine + ca-certificates + non-root user (shared by all
# final stages below).
# ────────────────────────────────────────────────────────────────────────
FROM alpine:3.21 AS base
RUN apk --no-cache add ca-certificates wget && \
    adduser -D -g '' appuser
WORKDIR /app

# ────────────────────────────────────────────────────────────────────────
# Stage 3: api — HTTP server only. Default target.
# ────────────────────────────────────────────────────────────────────────
FROM base AS api
COPY --from=builder /out/api /app/api
RUN chown -R appuser:appuser /app
USER appuser
EXPOSE 8080

# Health check for Railway / container orchestrators
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://localhost:8080/api/v1/health || exit 1

CMD ["/app/api"]

# ────────────────────────────────────────────────────────────────────────
# Stage 4: worker — 5 background goroutines only. No HTTP server.
# Use with HPA / background-pod patterns (k8s Deployment).
# ────────────────────────────────────────────────────────────────────────
FROM base AS worker
COPY --from=builder /out/worker /app/worker
RUN chown -R appuser:appuser /app
USER appuser

CMD ["/app/worker"]

# ────────────────────────────────────────────────────────────────────────
# Stage 5: migrate — one-shot pre-deploy job. No server, no workers.
# Designed to run as a Railway pre-deploy job / k8s Job / helm hook.
# Exits 0 on success, 1 on any migration failure.
# ────────────────────────────────────────────────────────────────────────
FROM base AS migrate
COPY --from=builder /out/migrate /app/migrate
RUN chown -R appuser:appuser /app
USER appuser

CMD ["/app/migrate"]

# ────────────────────────────────────────────────────────────────────────
# Stage 6: server — legacy single-bundle wrapper (Blocco #2.1 backward
# compatibility). Runs API + workers + migrate in one process. Use ONLY
# for local dev / Railway single-process deploys.
# ────────────────────────────────────────────────────────────────────────
FROM base AS server
COPY --from=builder /out/server /app/server
RUN chown -R appuser:appuser /app
USER appuser
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://localhost:8080/api/v1/health || exit 1

CMD ["/app/server"]

# ────────────────────────────────────────────────────────────────────────
# Stage 7: production — Blocco #4.1 Fly.io unified-image target.
#
# Ships /app/api, /app/worker, AND /app/migrate in ONE image. fly.toml
# [processes] picks which binary runs per process group:
#   - [processes] api    = "/app/api"     (HTTP server)
#   - [processes] worker = "/app/worker"  (5 background goroutines)
#
# WORKDIR=/app keeps the cmd/migrate binary at ./migrate so the
# release_command = "./migrate" Fly idiom lands on the right path.
# The docker HEALTHCHECK below is a fallback for raw `docker run`
# debugging; Fly has per-process health checks of its own
# ([[services.http_checks]] on the api group + [[services.tcp_checks]]
# on the worker group, both pointing at this image's listener rather
# than a Docker HEALTHCHECK).
# ────────────────────────────────────────────────────────────────────────
FROM base AS production
COPY --from=builder /out/api     /app/api
COPY --from=builder /out/worker  /app/worker
COPY --from=builder /out/migrate /app/migrate
RUN chown -R appuser:appuser /app
USER appuser
WORKDIR /app
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -qO- http://localhost:8080/api/v1/health || exit 1

# Default CMD is the api; fly.toml [processes] overrides per process group.
CMD ["/app/api"]
