# InstaEditLogin — Multi-stage Dockerfile (Blocco #2.1)
#
# Targets:
#   api         — HTTP server only (cmd/api). Local-dev single-process shape.
#   worker      — 5 background goroutines only (cmd/worker). Local-dev single-process.
#   migrate     — one-shot pre-deploy migration (cmd/migrate).
#   server      — legacy single-bundle wrapper (cmd/server) for local recovery.
#
# Build:
#   docker build --target api         -t instaedit-api         .
#   docker build --target worker      -t instaedit-worker      .
#   docker build --target migrate     -t instaedit-migrate     .
#   docker build --target server      -t instaedit-server      .   # legacy single-process
#
# Default target (when no --target is supplied): api.

# ────────────────────────────────────────────────────────────────────────
# Stage 1: Builder — compile all 4 binaries from a single source tree.
# ────────────────────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder
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
#
# ffmpeg (with the ffprobe binary) is installed here — the upload
# worker probes every ingested asset (migration 092) to populate
# duration/resolution/FPS/audio for the live-streaming wizard. The
# probe is best-effort: a missing binary is a soft skip, never a
# job failure.
# ────────────────────────────────────────────────────────────────────────
FROM alpine:3.21 AS base
RUN apk --no-cache add ca-certificates wget ffmpeg && \
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

# Health check for Docker Compose and the host supervisor.
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://localhost:8080/api/v1/health || exit 1

CMD ["/app/api"]

# ────────────────────────────────────────────────────────────────────────
# Stage 4: worker — 5 background goroutines only. No HTTP server.
# Used as the worker service in the self-hosted Compose stack.
# ────────────────────────────────────────────────────────────────────────
FROM base AS worker
COPY --from=builder /out/worker /app/worker
RUN chown -R appuser:appuser /app
USER appuser

CMD ["/app/worker"]

# ────────────────────────────────────────────────────────────────────────
# Stage 5: migrate — one-shot pre-deploy job. No server, no workers.
# Designed to run as the one-shot Compose migration service.
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
# for local recovery and development, never as production topology.
# ────────────────────────────────────────────────────────────────────────
FROM base AS server
COPY --from=builder /out/server /app/server
RUN chown -R appuser:appuser /app
USER appuser
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://localhost:8080/api/v1/health || exit 1

CMD ["/app/server"]
