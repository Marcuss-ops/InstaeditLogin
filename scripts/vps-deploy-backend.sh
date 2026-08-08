#!/usr/bin/env bash
# scripts/vps-deploy-backend.sh
#
# Reusable backend release for the production VPS (api.instaedit.org).
# This is the safe wrapper around the effective release command documented
# in docs/DEPLOY.md §5 (live-migration exception): it NEVER runs `down -v`,
# never edits deployment files on the host, and stops at the first failure.
#
# Pipeline (each step bails on failure, exit code != 0):
#   1. preflight  : on main, clean tree, compose/env files present,
#                   API_HOST_PORT == 8080 (Caddy target), no orphaned
#                   instaeditlogin-dev binary shadowing :8080
#   2. sync       : git fetch + git pull --ff-only origin main, verify
#                   HEAD == origin/main (never deploy a dirty tree)
#   3. config gate: docker compose config --quiet  → STOP here on error,
#                   no `up` is attempted (the runbook forbids it)
#   4. deploy     : docker compose up -d --build (api + worker + migrate
#                   job; migrate applies pending migrations before the
#                   long-running services are released)
#   5. verify     : compose ps + logs, then the public gates:
#                   /api/v1/health, /ready (retried through warm-up),
#                   app.instaedit.org; optional post-deploy smoke
#                   (scripts/ops/post_deploy_smoke.sh) with RUN_SMOKE=1
#
# Usage:
#   ./scripts/vps-deploy-backend.sh                  # full release
#   ./scripts/vps-deploy-backend.sh --dry-run        # preflight + print commands only
#   RUN_SMOKE=1 ./scripts/vps-deploy-backend.sh      # also run post_deploy_smoke.sh
#   CHECK_CI=1 ./scripts/vps-deploy-backend.sh       # warn if integration-fast is not green (needs gh auth)
#
# Env overrides (all optional; defaults = live VPS shape):
#   REPO_DIR        repo checkout to release (default: this repo root)
#   BRANCH          branch to deploy (default: main)
#   ENV_FILE        env file name/path (default: .env.dev)
#   COMPOSE_FILES   space-separated compose files (default: docker-compose.yml docker-compose.local.yml)
#   EXPECTED_API_PORT  loopback port Caddy proxies to (default: 8080)
#   PUBLIC_API_BASE public API origin (default: https://api.instaedit.org)
#   PUBLIC_APP_BASE public frontend origin (default: https://app.instaedit.org)
#
# Exit codes:
#   0  release completed and public gates passed
#   1  any preflight/deploy/gate failure (see message for remediation)
#   2  missing required tool (git / docker / curl / grep)

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"

# ─── Config ─────────────────────────────────────────────────────────────
REPO_DIR="${REPO_DIR:-$REPO_ROOT}"
BRANCH="${BRANCH:-main}"
ENV_FILE="${ENV_FILE:-.env.dev}"
COMPOSE_FILES="${COMPOSE_FILES:-docker-compose.yml docker-compose.local.yml}"
EXPECTED_API_PORT="${EXPECTED_API_PORT:-8080}"
PUBLIC_API_BASE="${PUBLIC_API_BASE:-https://api.instaedit.org}"
PUBLIC_APP_BASE="${PUBLIC_APP_BASE:-https://app.instaedit.org}"
RUN_SMOKE="${RUN_SMOKE:-0}"
CHECK_CI="${CHECK_CI:-0}"

DRY_RUN=0
if [[ "${1:-}" == "--dry-run" ]]; then
  DRY_RUN=1
elif [[ $# -gt 0 ]]; then
  echo "Usage: $0 [--dry-run]" >&2
  exit 2
fi

# Colour switch off when piping.
if [[ -t 1 ]]; then
  C_CYAN=$'\033[1;36m'; C_GREEN=$'\033[1;32m'; C_RED=$'\033[1;31m'; C_YELLOW=$'\033[1;33m'; C_OFF=$'\033[0m'
else
  C_CYAN=""; C_GREEN=""; C_RED=""; C_YELLOW=""; C_OFF=""
fi

log()  { printf '\n%s==> %s%s\n' "$C_CYAN" "$*" "$C_OFF"; }
ok()   { printf '%s✓ %s%s\n' "$C_GREEN" "$*" "$C_OFF"; }
warn() { printf '%s! %s%s\n' "$C_YELLOW" "$*" "$C_OFF"; }
fail() { printf '%sERROR: %s%s\n' "$C_RED" "$*" "$C_OFF" >&2; exit 1; }

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "Comando mancante: $1"
}

# Compose argv, shared by every docker compose invocation. Built once from a
# single source so `config` and `up` can never diverge.
read -r -a COMPOSE_FILE_LIST <<< "$COMPOSE_FILES"
COMPOSE_ARGS=(--env-file "$ENV_FILE")
for _f in "${COMPOSE_FILE_LIST[@]}"; do
  COMPOSE_ARGS+=(-f "$_f")
done

compose_run() {
  # API_HOST_PORT is FORCED to the Caddy target: compose interpolates
  # ${API_HOST_PORT:-8080} from the shell env too, so an operator-exported
  # value (e.g. 8082 from the 2026-08-05 incident) would otherwise drift
  # the published port away from what Caddy proxies to.
  #
  # Global flags (--env-file, -f) MUST precede the subcommand in Compose v2
  # (verified on 2.40.3: after `config` they fail with "unknown flag").
  INSTAEDIT_ENV_FILE="$ENV_FILE" API_HOST_PORT="$EXPECTED_API_PORT" \
    docker compose "${COMPOSE_ARGS[@]}" "$@"
}

# ─── Preflight ──────────────────────────────────────────────────────────
require_command git
require_command docker
require_command curl
require_command grep

docker compose version >/dev/null 2>&1 || fail "Plugin docker compose non disponibile (docker compose version fallisce)"

# Shell-exported API_HOST_PORT would override .env.dev at interpolation
# time; the script forces the Caddy target in compose_run, so warn loudly
# instead of silently ignoring the drift.
if [[ -n "${API_HOST_PORT:-}" ]] && [[ "$API_HOST_PORT" != "$EXPECTED_API_PORT" ]]; then
  warn "API_HOST_PORT esportato nella shell ($API_HOST_PORT) diverge dal target Caddy ($EXPECTED_API_PORT): verrò forzato a $EXPECTED_API_PORT."
fi

cd "$REPO_DIR"

log "Preflight"
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || fail "Non dentro un repo git: $REPO_DIR"

CURRENT_BRANCH="$(git branch --show-current)"
[[ "$CURRENT_BRANCH" == "$BRANCH" ]] ||
  fail "Branch corrente: $CURRENT_BRANCH, richiesto: $BRANCH (switcha prima del deploy)"

if [[ -n "$(git status --porcelain)" ]]; then
  fail "Working tree sporco: non si deploya un albero non committato. Committa o stash prima."
fi

for f in $COMPOSE_FILES; do
  [[ -f "$f" ]] || fail "Compose file mancante: $f"
done
[[ -f "$ENV_FILE" ]] || fail "Env file mancante: $ENV_FILE (è gitignored; deve esistere sulla VPS)"

# API_HOST_PORT must match the Caddy target, or Caddy proxies to a stale
# port (incident 2026-08-05 — orphaned binary on :8080 serving old code).
API_PORT_FROM_ENV="$(grep -E '^API_HOST_PORT=' "$ENV_FILE" 2>/dev/null | tail -1 | cut -d= -f2 || true)"
if [[ -n "$API_PORT_FROM_ENV" ]] && [[ "$API_PORT_FROM_ENV" != "$EXPECTED_API_PORT" ]]; then
  fail "API_HOST_PORT=$API_PORT_FROM_ENV in $ENV_FILE, ma Caddy proxy a 127.0.0.1:$EXPECTED_API_PORT. Correggi prima del deploy (docs/DEPLOY.md §5)."
fi
ok "API_HOST_PORT=${API_PORT_FROM_ENV:-$EXPECTED_API_PORT} (target Caddy: $EXPECTED_API_PORT)"

# Orphaned single-process dev binary shadowing the Compose api container.
if pgrep -af instaeditlogin-dev >/dev/null 2>&1; then
  fail "Binario orfano instaeditlogin-dev attivo su :8080. Rimuovilo prima del deploy: sudo systemctl disable --now instaeditlogin.service"
fi
ok "Nessun binario orfano instaeditlogin-dev"

echo "  Listener attuale su :$EXPECTED_API_PORT (atteso: container compose api):"
ss -ltnp 2>/dev/null | grep ":$EXPECTED_API_PORT" || echo "  (nessun listener rilevato su :$EXPECTED_API_PORT)"

# Optional CI gate: warn (do not block) when integration-fast is red on the
# commit about to be deployed; requires `gh` authenticated.
if [[ "$CHECK_CI" == "1" ]] && command -v gh >/dev/null 2>&1; then
  CI_SHA="$(git rev-parse HEAD)"
  CI_CONCLUSION="$(gh run list --repo "$(git config --get remote.origin.url | sed -E 's#.*[:/]([^:/]+/[^/]+)\.git#\1#')" \
    --workflow integration-fast.yml --branch "$BRANCH" --commit "$CI_SHA" --limit 1 \
    --json conclusion --jq '.[0].conclusion // "unknown"' 2>/dev/null || echo "unknown")"
  [[ "$CI_CONCLUSION" == "success" ]] ||
    warn "integration-fast sul commit da deployare: '$CI_CONCLUSION'. Il frontend Vercel NON parte finché la CI non è verde."
fi

if [[ "$DRY_RUN" == "1" ]]; then
  log "DRY-RUN — comandi che verranno eseguiti (nessuna modifica)"
  echo "  git fetch origin $BRANCH && git pull --ff-only origin $BRANCH"
  echo "  INSTAEDIT_ENV_FILE=$ENV_FILE docker compose --env-file $ENV_FILE -f ${COMPOSE_FILES// / -f } config --quiet"
  echo "  INSTAEDIT_ENV_FILE=$ENV_FILE docker compose --env-file $ENV_FILE -f ${COMPOSE_FILES// / -f } up -d --build"
  echo "  curl -fsS $PUBLIC_API_BASE/api/v1/health"
  echo "  curl -fsS $PUBLIC_API_BASE/ready   (con retry warm-up)"
  echo "  curl -fsSI $PUBLIC_APP_BASE/"
  ok "Preflight superato. Nessuna azione eseguita."
  exit 0
fi

# ─── Sync ────────────────────────────────────────────────────────────────
log "Sincronizzazione $BRANCH"
git fetch origin "$BRANCH" --quiet
git pull --ff-only origin "$BRANCH"

SHA="$(git rev-parse HEAD)"
REMOTE_SHA="$(git rev-parse "origin/$BRANCH")"
[[ "$SHA" == "$REMOTE_SHA" ]] || fail "HEAD locale ($SHA) diverso da origin/$BRANCH ($REMOTE_SHA)"

ok "HEAD: $SHA"
git log -n 5 --oneline

# ─── Config gate ─────────────────────────────────────────────────────────
log "Verifica config compose (config --quiet)"
compose_run config --quiet
ok "Config compose valida — procedo con up"

# ─── Deploy ──────────────────────────────────────────────────────────────
log "Deploy backend (up -d --build)"
compose_run up -d --build

# Post-deploy port ownership: the Compose api container (not an orphan or
# a drifted binding) must own 127.0.0.1:$EXPECTED_API_PORT — the exact
# failure mode documented in docs/DEPLOY.md §5.
API_PORT_PUBLISHED="$(compose_run port api "$EXPECTED_API_PORT" 2>/dev/null || true)"
grep -q "127.0.0.1:$EXPECTED_API_PORT" <<<"$API_PORT_PUBLISHED" ||
  fail "Il container compose api non pubblica su 127.0.0.1:$EXPECTED_API_PORT (pubblicato: '${API_PORT_PUBLISHED:-nessuno}'). Controlla: ss -ltnp | grep :$EXPECTED_API_PORT"
ok "Container api pubblica su 127.0.0.1:$EXPECTED_API_PORT"

# ─── Verify ──────────────────────────────────────────────────────────────
log "Stato container"
compose_run ps || true

log "Log recenti (migrate api worker)"
compose_run logs --tail=80 migrate api worker || true

log "Health check pubblici"
for i in $(seq 1 60); do
  if curl -fsS --max-time 10 "$PUBLIC_API_BASE/ready" >/dev/null 2>&1; then
    break
  fi
  if [[ "$i" -eq 60 ]]; then
    fail "/ready non risponde 200 dopo 3 minuti (warm-up worker o DB). Controlla: docker compose logs migrate api worker"
  fi
  sleep 3
done
ok "/ready → 200"

HEALTH_BODY="$(curl -fsS --max-time 10 "$PUBLIC_API_BASE/api/v1/health")"
grep -q '"status":"ok"' <<<"$HEALTH_BODY" ||
  fail "/api/v1/health non riporta status=ok: $HEALTH_BODY"
ok "/api/v1/health → 200 + status=ok"

curl -fsSI --max-time 15 "$PUBLIC_APP_BASE/" >/dev/null 2>&1 ||
  fail "Frontend $PUBLIC_APP_BASE non risponde"
ok "$PUBLIC_APP_BASE → HTTP valido"

if [[ "$RUN_SMOKE" == "1" ]]; then
  log "Smoke post-deploy (scripts/ops/post_deploy_smoke.sh)"
  # Advisory, non fatale: il deploy è già completo; un smoke rosso non deve
  # nascondere il verdetto di successo qui sotto.
  "$SCRIPT_DIR/ops/post_deploy_smoke.sh" || warn "Smoke post-deploy fallita (vedi output sopra) — indagare separatamente."
fi

# ─── Summary ─────────────────────────────────────────────────────────────
log "DEPLOY COMPLETATO"
printf 'Commit:        %s\n' "$SHA"
printf 'Env file:      %s\n' "$ENV_FILE"
printf 'Compose files: %s\n' "$COMPOSE_FILES"
printf 'API:           %s\n' "$PUBLIC_API_BASE"
printf 'Frontend:      %s\n' "$PUBLIC_APP_BASE"
echo
echo "Prossimo passo consigliato: verificare la hub Copertine e, per il frontend,"
echo "attendere CI verde + deploy Vercel automatico (o ./scripts/push-deploy-verify.sh)."
