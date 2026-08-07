#!/usr/bin/env bash
# scripts/verify-instaeditor-routing.sh
#
# Static/optional operational guard for the InstaEditor infrastructure
# contract. The product is branded InstaEditor, but deployed editor_url
# values still use the stable Next base path /dark_editor_v2.
#
# Default mode is offline and read-only. Set CHECK_INSTAEDITOR=1 and
# INSTAEDITOR_URL (or EDITOR_URL) to perform an optional HTTP probe of the
# configured editor root. A project-specific /editor/<id> probe belongs to
# the authenticated API smoke flow and is intentionally not fabricated here.

set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
LOCAL_CADDY="$ROOT/ops/local/Caddyfile"
VPS_CADDY="$ROOT/ops/vps/Caddyfile"
COMPOSE="$ROOT/docker-compose.yml"
SMOKE="$ROOT/scripts/ops/post_deploy_smoke.sh"
DEPLOY_VERIFY="$ROOT/scripts/push-deploy-verify.sh"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}
pass() {
  printf 'PASS: %s\n' "$*"
}

[ -f "$LOCAL_CADDY" ] || fail "missing $LOCAL_CADDY"
[ -f "$VPS_CADDY" ] || fail "missing $VPS_CADDY"
[ -f "$COMPOSE" ] || fail "missing $COMPOSE"
[ -x "$SMOKE" ] || fail "$SMOKE must be executable"
[ -x "$DEPLOY_VERIFY" ] || fail "$DEPLOY_VERIFY must be executable"

# The path is a compatibility contract, not a branding suggestion.
grep -Fq '@instaeditor path /dark_editor_v2 /dark_editor_v2/*' "$LOCAL_CADDY" ||
  fail "Caddy no longer declares the InstaEditor /dark_editor_v2 compatibility matcher"
grep -Fq 'handle @instaeditor' "$LOCAL_CADDY" ||
  fail "Caddy no longer mounts the named InstaEditor handler"
grep -Fq 'reverse_proxy 127.0.0.1:3001' "$LOCAL_CADDY" ||
  fail "Caddy InstaEditor handler no longer proxies to the Next.js editor"

# Adapt validates Caddyfile syntax without loading local TLS certificates.
# Full `caddy validate` remains an operator check when /certs/ is mounted.
if command -v caddy >/dev/null 2>&1; then
  caddy adapt --config "$LOCAL_CADDY" --adapter caddyfile >/dev/null ||
    fail "Caddyfile cannot be adapted by the installed Caddy binary"
fi

# A root-level _next handler would break Next basePath isolation.
if grep -Eq '^[[:space:]]*handle(_path)?[[:space:]]+/_next' "$LOCAL_CADDY"; then
  fail "Caddy contains a forbidden root-level /_next handler"
fi

# Both aliases must remain documented in the self-hosted stack.
grep -Fq 'INSTAEDITOR_URL' "$COMPOSE" || fail "Compose does not document INSTAEDITOR_URL"
grep -Fq 'EDITOR_URL' "$COMPOSE" || fail "Compose does not document the EDITOR_URL fallback"
grep -Fq '/dark_editor_v2' "$COMPOSE" || fail "Compose no longer documents the stable editor path"

grep -Fq 'CHECK_INSTAEDITOR' "$SMOKE" || fail "post-deploy smoke lacks the optional InstaEditor probe"
grep -Fq 'verify-instaeditor-routing.sh' "$DEPLOY_VERIFY" || fail "deploy verifier does not run the routing guard"

for env_file in .env.dev.example .env.production.example .env.test.example; do
  path="$ROOT/$env_file"
  [ -f "$path" ] || fail "missing $env_file"
  grep -Fq 'INSTAEDITOR_URL' "$path" || fail "$env_file lacks INSTAEDITOR_URL"
  grep -Fq 'EDITOR_URL' "$path" || fail "$env_file lacks EDITOR_URL fallback"
done

pass "InstaEditor naming and /dark_editor_v2 compatibility contract is intact"

if [ "${CHECK_INSTAEDITOR:-0}" != "1" ]; then
  pass "offline mode (set CHECK_INSTAEDITOR=1 for an HTTP probe)"
  exit 0
fi

EDITOR_BASE_URL="${INSTAEDITOR_URL:-${EDITOR_URL:-}}"
[ -n "$EDITOR_BASE_URL" ] || fail "CHECK_INSTAEDITOR=1 requires INSTAEDITOR_URL or EDITOR_URL"
case "$EDITOR_BASE_URL" in
  */dark_editor_v2|*/dark_editor_v2/) ;;
  *) fail "editor URL must preserve /dark_editor_v2, got $EDITOR_BASE_URL" ;;
esac

probe_url="${EDITOR_BASE_URL%/}/"
status="$(curl -sS -L --max-time "${INSTAEDITOR_TIMEOUT_SECONDS:-15}" -o /dev/null -w '%{http_code}' "$probe_url" || true)"
case "$status" in
  2*|3*) pass "InstaEditor root probe $probe_url returned HTTP $status" ;;
  *) fail "InstaEditor root probe $probe_url returned HTTP ${status:-000}" ;;
esac
