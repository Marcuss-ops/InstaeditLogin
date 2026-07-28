#!/usr/bin/env bash
# scripts/push-deploy-verify.sh
#
# End-to-end CLI deploy verifier for Marcuss-ops/InstaeditLogin.
# Runs in this order and bails on any failure (exit code ≠ 0):
#
#   1. preflight (git clean, on main, gh+vercel authenticated)
#   2. git pull --ff-only origin main  +  git push origin main
#   3. wait for github actions: integration run on pushed SHA → SUCCESS
#   4. wait for github actions: deploy       run on the SAME SHA → SUCCESS
#   5. vercel list --prod --meta githubCommitSha=<SHA> shows READY
#   6. vercel inspect <deployment> --wait until complete
#   7. smoke-test https://app.instaedit.org  (HTTP valid, x-vercel-id present)
#   8. fetch /assets/index-*.js and assert EXPECTED_MARKER if set
#
# Exit code is 0 only when every gate passes. On failure the script
# dumps the failed GitHub Actions step log and the failing Vercel
# inspect output so the operator can diagnose without opening a
# browser.
#
# Required env vars (set in the agent / CI runner, NEVER committed):
#   GH_TOKEN            github PAT with repo + workflow scopes
#   VERCEL_TOKEN        https://vercel.com/account/tokens (Full Account)
#   VERCEL_PROJECT      Vercel project name (default: instaedit-login-267l)
#   VERCEL_SCOPE        Vercel team / scope (default: marcuss-ops-projects)
#   EXPECTED_MARKER     substring that must appear in the production
#                       bundle after deploy (opt-in; empty = skip)

set -Eeuo pipefail

# ──────────────────────────────────────────────────────────────
# Configuration
# ──────────────────────────────────────────────────────────────

REPO="${REPO:-Marcuss-ops/InstaeditLogin}"
BRANCH="${BRANCH:-main}"

CI_WORKFLOW="${CI_WORKFLOW:-integration-fast.yml}"
DEPLOY_WORKFLOW="${DEPLOY_WORKFLOW:-deploy.yml}"

VERCEL_PROJECT="${VERCEL_PROJECT:-instaedit-login-267l}"
VERCEL_SCOPE="${VERCEL_SCOPE:-marcuss-ops-projects}"
VERCEL_CLI_VERSION="${VERCEL_CLI_VERSION:-57.0.0}"

PRODUCTION_URL="${PRODUCTION_URL:-https://app.instaedit.org}"

# Optional text expected inside the compiled JavaScript bundle.
# Override for every release:
#
# EXPECTED_MARKER="Fast-Track Partner Monetization" \
#   ./scripts/push-deploy-verify.sh
EXPECTED_MARKER="${EXPECTED_MARKER:-}"

# Number of attempts when waiting for GitHub to register a run.
RUN_LOOKUP_ATTEMPTS="${RUN_LOOKUP_ATTEMPTS:-40}"
RUN_LOOKUP_INTERVAL="${RUN_LOOKUP_INTERVAL:-3}"

# ──────────────────────────────────────────────────────────────
# Helpers
# ──────────────────────────────────────────────────────────────

log() {
  printf '\n\033[1;36m==> %s\033[0m\n' "$*"
}

fail() {
  printf '\n\033[1;31mERROR: %s\033[0m\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 ||
    fail "Comando mancante: $1"
}

get_run_id() {
  local workflow="$1"
  local sha="$2"

  gh run list \
    --repo "$REPO" \
    --workflow "$workflow" \
    --branch "$BRANCH" \
    --commit "$sha" \
    --limit 20 \
    --json databaseId,headSha,createdAt,status,conclusion \
    --jq '.[0].databaseId // empty'
}

wait_for_run_registration() {
  local workflow="$1"
  local sha="$2"
  local run_id=""

  for ((attempt = 1; attempt <= RUN_LOOKUP_ATTEMPTS; attempt++)); do
    run_id="$(get_run_id "$workflow" "$sha")"

    if [[ -n "$run_id" ]]; then
      printf '%s' "$run_id"
      return 0
    fi

    sleep "$RUN_LOOKUP_INTERVAL"
  done

  return 1
}

watch_run() {
  local run_id="$1"
  local label="$2"

  log "Monitoraggio $label — run $run_id"

  if ! gh run watch "$run_id" \
    --repo "$REPO" \
    --compact \
    --exit-status; then

    printf '\n--- LOG DEGLI STEP FALLITI ---\n' >&2

    gh run view "$run_id" \
      --repo "$REPO" \
      --log-failed || true

    fail "$label fallito"
  fi

  gh run view "$run_id" \
    --repo "$REPO" \
    --json workflowName,conclusion,headSha,url \
    --jq '"Workflow: \(.workflowName)\nConclusione: \(.conclusion)\nSHA: \(.headSha)\nURL: \(.url)"'
}

verify_remote_sha() {
  git fetch origin "$BRANCH" --quiet

  local local_sha
  local remote_sha

  local_sha="$(git rev-parse HEAD)"
  remote_sha="$(git rev-parse "origin/$BRANCH")"

  if [[ "$local_sha" != "$remote_sha" ]]; then
    fail "HEAD locale ($local_sha) diverso da origin/$BRANCH ($remote_sha)"
  fi
}

# ──────────────────────────────────────────────────────────────
# Preflight
# ──────────────────────────────────────────────────────────────

require_command git
require_command gh
require_command curl
require_command grep
require_command npx

log "Verifica autenticazione GitHub"
gh auth status

[[ -n "${VERCEL_TOKEN:-}" ]] ||
  fail "VERCEL_TOKEN non impostato nell'ambiente dell'agente"

CURRENT_BRANCH="$(git branch --show-current)"

[[ "$CURRENT_BRANCH" == "$BRANCH" ]] ||
  fail "Branch corrente: $CURRENT_BRANCH. Richiesto: $BRANCH"

if [[ -n "$(git status --porcelain)" ]]; then
  fail "Working tree non pulito. Committare prima di distribuire."
fi

log "Sincronizzazione $BRANCH"
git pull --ff-only origin "$BRANCH"

SHA="$(git rev-parse HEAD)"
SHORT_SHA="$(git rev-parse --short HEAD)"

log "Push di $SHORT_SHA"
git push origin "$BRANCH"

verify_remote_sha

log "Ultimi commit verificati"
git log -n 5 --oneline

printf '\nSHA da distribuire: %s\n' "$SHA"

# ──────────────────────────────────────────────────────────────
# Integration workflow
# ──────────────────────────────────────────────────────────────

log "Ricerca integration per $SHORT_SHA"

CI_RUN_ID="$(wait_for_run_registration "$CI_WORKFLOW" "$SHA" || true)"

if [[ -z "$CI_RUN_ID" ]]; then
  log "Integration non partita automaticamente: avvio workflow_dispatch"

  gh workflow run "$CI_WORKFLOW" \
    --repo "$REPO" \
    --ref "$BRANCH"

  CI_RUN_ID="$(wait_for_run_registration "$CI_WORKFLOW" "$SHA" || true)"
fi

[[ -n "$CI_RUN_ID" ]] ||
  fail "Impossibile trovare la run integration per $SHA"

watch_run "$CI_RUN_ID" "Integration"

# Evita di distribuire un commit diverso se qualcuno ha pushato
# mentre la CI era in esecuzione.
verify_remote_sha

# ──────────────────────────────────────────────────────────────
# Deploy workflow
# ──────────────────────────────────────────────────────────────

log "Ricerca deploy automatico per $SHORT_SHA"

DEPLOY_RUN_ID=""

# Lascia alcuni secondi al workflow_run per creare il deploy.
for ((attempt = 1; attempt <= 20; attempt++)); do
  DEPLOY_RUN_ID="$(get_run_id "$DEPLOY_WORKFLOW" "$SHA")"

  if [[ -n "$DEPLOY_RUN_ID" ]]; then
    break
  fi

  sleep 3
done

if [[ -z "$DEPLOY_RUN_ID" ]]; then
  log "Deploy automatico non trovato: avvio workflow_dispatch"

  verify_remote_sha

  gh workflow run "$DEPLOY_WORKFLOW" \
    --repo "$REPO" \
    --ref "$BRANCH"

  DEPLOY_RUN_ID="$(wait_for_run_registration "$DEPLOY_WORKFLOW" "$SHA" || true)"
fi

[[ -n "$DEPLOY_RUN_ID" ]] ||
  fail "Impossibile trovare la run deploy per $SHA"

watch_run "$DEPLOY_RUN_ID" "Deploy Vercel"

# ──────────────────────────────────────────────────────────────
# Vercel verification
# ──────────────────────────────────────────────────────────────

log "Verifica deployment Vercel associato a $SHORT_SHA"

VERCEL_OUTPUT="$(
  npx --yes "vercel@${VERCEL_CLI_VERSION}" list "$VERCEL_PROJECT" \
    --prod \
    --status READY \
    --meta "githubCommitSha=${SHA}" \
    --scope "$VERCEL_SCOPE" \
    --token "$VERCEL_TOKEN" \
    --no-color
)"

printf '%s\n' "$VERCEL_OUTPUT"

DEPLOYMENT_URL="$(
  printf '%s\n' "$VERCEL_OUTPUT" |
    grep -oE 'https://[^[:space:]]+\.vercel\.app' |
    head -n 1 || true
)"

if [[ -z "$DEPLOYMENT_URL" ]]; then
  fail "Nessun deployment Production READY trovato per SHA $SHA"
fi

log "Ispezione deployment $DEPLOYMENT_URL"

npx --yes "vercel@${VERCEL_CLI_VERSION}" inspect "$DEPLOYMENT_URL" \
  --wait \
  --timeout=5m \
  --scope "$VERCEL_SCOPE" \
  --token "$VERCEL_TOKEN"

# ──────────────────────────────────────────────────────────────
# Production smoke test
# ──────────────────────────────────────────────────────────────

log "Smoke test $PRODUCTION_URL"

HEADERS_FILE="$(mktemp)"
HTML_FILE="$(mktemp)"
BUNDLE_FILE="$(mktemp)"

cleanup() {
  rm -f "$HEADERS_FILE" "$HTML_FILE" "$BUNDLE_FILE"
}

trap cleanup EXIT

curl \
  --fail \
  --silent \
  --show-error \
  --location \
  --max-time 30 \
  --dump-header "$HEADERS_FILE" \
  --output "$HTML_FILE" \
  "$PRODUCTION_URL"

STATUS_LINE="$(head -n 1 "$HEADERS_FILE" | tr -d '\r')"

printf 'HTTP: %s\n' "$STATUS_LINE"

if grep -qi '^x-vercel-id:' "$HEADERS_FILE"; then
  grep -iE '^x-vercel-id:|^x-vercel-cache:|^age:' "$HEADERS_FILE" |
    tr -d '\r'
else
  fail "La risposta non contiene x-vercel-id: dominio non servito da Vercel?"
fi

ASSET_PATH="$(
  grep -oE '/assets/index-[A-Za-z0-9_-]+\.js' "$HTML_FILE" |
    head -n 1 || true
)"

[[ -n "$ASSET_PATH" ]] ||
  fail "Bundle index-*.js non trovato nell'HTML di produzione"

curl \
  --fail \
  --silent \
  --show-error \
  --location \
  --max-time 60 \
  --output "$BUNDLE_FILE" \
  "${PRODUCTION_URL%/}${ASSET_PATH}"

printf 'Bundle: %s\n' "$ASSET_PATH"

if [[ -n "$EXPECTED_MARKER" ]]; then
  if grep -Fq "$EXPECTED_MARKER" "$BUNDLE_FILE"; then
    printf 'Marker trovato: %s\n' "$EXPECTED_MARKER"
  else
    fail "Marker non trovato nel bundle: $EXPECTED_MARKER"
  fi
fi

log "DEPLOY COMPLETATO E VERIFICATO"
printf 'Commit:     %s\n' "$SHA"
printf 'Deployment: %s\n' "$DEPLOYMENT_URL"
printf 'Production: %s\n' "$PRODUCTION_URL"
