#!/usr/bin/env bash
# scripts/ops/verify-tiktok-oauth-e2e.sh
#
# Operator-side E2E verification for the c9e760d
# /api/v1/auth/{provider}/start alias on api.instaedit.org.
#
# Why this script exists:
#   The /start alias was added as a backwards-compat sibling of /login
#   (commit c9e760d). The TikTok OAuth gate is the load-bearing reason
#   for the VPS-first cutover; before tearing down the legacy Fly app
#   we had to prove E2E that /start works the same as /login on the
#   VPS-served api binary.
#
# Prerequisites (operator workstation):
#   • SSH access to the VPS (key in agent): `ssh root@51.91.11.36 whoami`
#   • docker compose project root on the VPS at /opt/instaedit/InstaeditLogin
#     (per Makefile's COMPOSE_DIR; override with VPS_DIR or positional arg)
#   • A real TikTok developer account that can click "Allow" on the
#     consent screen (the console won't do this for you)
#   • A test workspace_id that already exists in connection_states
#     (or any workspace the test user belongs to)
#
# Captures the four required signals:
#   (a) /start hit              — apilog contains "tiktok.*start"
#   (b) callback received        — apilog contains "tiktok.*callback"
#   (c) code→token exchange      — apilog contains "tiktok.*(token|exchange)"
#   (d) DB row in connection_states (provider='tiktok')
#
# Usage:
#   scripts/ops/verify-tiktok-oauth-e2e.sh <workspace_id> [vps_host] [vps_dir]
#
# Flags / env:
#   NO_PROMPT=1            skip the interactive "press ENTER" step (re-runs)
#   SINCE=10m              override the `--since` window on api logs (default 5m)
#   VPS_HOST=root@…        override ssh target (positional [2] wins)
#   VPS_DIR=/path          override compose project root (positional [3] wins)
#
# Examples:
#   scripts/ops/verify-tiktok-oauth-e2e.sh 6f4d2b71-b3a4-4d29-9f12-...
#   NO_PROMPT=1 scripts/ops/verify-tiktok-oauth-e2e.sh 6f4d2b71-...   # re-run
#   VPS_HOST=root@51.91.11.36 VPS_DIR=/srv/instaedit \
#     scripts/ops/verify-tiktok-oauth-e2e.sh 6f4d2b71-...
#
# Exit codes:
#   0  all four signals present + DB row exists
#   1  pre-flight failed (network or ssh)
#   2  /start probe did NOT match /login (deploy lag suspected)
#   3  one of (a)/(b)/(c)/(d) missing
#   4  user aborted at the browser step (only with prompt mode)
#   64 (EX_USAGE) replaced by Bash-conventional exit 2 for arg-missing
#
# After success, paste the emitted probe-log row into
# docs/VPS-DEPLOY-STATUS.md at the bottom of the existing table.

set -euo pipefail

WORKSPACE_ID="${1:-}"
# Hygiene: sanitize for markdown-table usage (avoids breaking the row).
WORKSPACE_ID="${WORKSPACE_ID//|/}"
VPS_HOST="${2:-${VPS_HOST:-root@51.91.11.36}}"
VPS_DIR="${3:-${VPS_DIR:-/opt/instaedit/InstaeditLogin}}"
SINCE="${SINCE:-5m}"
MODE="prompt"
[[ "${NO_PROMPT:-0}" = "1" ]] && MODE="batch"

# Pretty section divider so paste-back is easy to read.
hr() { printf '\n%s\n' "──────────────────────────────────────────────────────────────"; }

if [[ -z "$WORKSPACE_ID" ]]; then
  cat >&2 <<USAGE
Usage: $0 <workspace_id> [vps_host] [vps_dir]

  WORKSPACE_ID  required  workspace to scope the OAuth start URL to
  vps_host      optional  defaults to root@51.91.11.36
  vps_dir       optional  defaults to /opt/instaedit/InstaeditLogin (Makefile COMPOSE_DIR)

Example:
  $0 6f4d2b71-b3a4-4d29-9f12-aaaaaaaaaaaa
USAGE
  exit 2
fi

PROBE_TS="$(date -u +'%Y-%m-%d %H:%M:%S')"
# Cross-distro portable: GNU/BSD mktemp both accept -t without a trailing suffix.
TMP_LOG="$(mktemp -t tiktok-e2e-XXXXXX)"
chmod 0600 "$TMP_LOG"   # contains OAuth code-flow text

# Cleanup runs on any termination (normal exit, Ctrl-C, SIGTERM). Removes
# the local tmp + the remote /tmp/tiktok-e2e-remote.log so a partial
# capture on the VPS never accumulates.
cleanup() {
  rm -f "$TMP_LOG" 2>/dev/null || true
  ssh -o BatchMode=yes -o ConnectTimeout=5 "$VPS_HOST" \
    "rm -f /tmp/tiktok-e2e-remote.log" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

hr
echo "TikTok OAuth E2E verification  (c9e760d /start alias)"
hr
echo "VPS host:        $VPS_HOST"
echo "VPS dir:         $VPS_DIR"
echo "Workspace id:    $WORKSPACE_ID"
echo "Log since:       $SINCE"
echo "Mode:            $MODE"
echo "Probe timestamp: $PROBE_TS UTC"
echo

# ─── Pre-flight: VPS reachable + keystroke-style check ─────────────
hr; echo "[1/8] Pre-flight: VPS reachable (BatchMode=yes, fail-fast)" ; hr
if ! ssh -o BatchMode=yes -o ConnectTimeout=10 "$VPS_HOST" \
      'echo "  ssh_ok=$(whoami)@$(hostname)"' 2>/dev/null; then
  cat >&2 <<EOF
ERROR: cannot ssh into $VPS_HOST in BatchMode=yes.
  - is your key loaded?             ssh-add -l
  - is the agent running?           eval "\$(ssh-agent -s)" && ssh-add
  - is the route open?              ping -c 1 "${VPS_HOST##*@}"
  - debug the ssh failure:          ssh -vvv "$VPS_HOST" 'echo ok'
EOF
  exit 1
fi

hr; echo "[2/8] Pre-flight: api container status on VPS" ; hr
ssh "$VPS_HOST" "cd $VPS_DIR && docker compose ps api"

# ─── Smoke probe: /start vs /login parity (catches deploy lag) ─────
hr; echo "[3/8] Smoke probe: /start vs /login parity on api.instaedit.org" ; hr
START_HTTP=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 15 \
  "https://api.instaedit.org/api/v1/auth/tiktok/start?workspace_id=$WORKSPACE_ID")
LOGIN_HTTP=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 15 \
  "https://api.instaedit.org/api/v1/auth/tiktok/login?workspace_id=$WORKSPACE_ID")
echo "  /start  HTTP=$START_HTTP"
echo "  /login  HTTP=$LOGIN_HTTP"
# Reject unexpected status as a deploy-lag sign: c9e760d routes produce
# 302 (real OAuth start) or 401/403 (no session). Anything else means
# the api binary on the VPS is missing the route registration or the
# chi mux fell through to an error handler.
is_canonical() { case "$1" in 302|401|403) return 0;; *) return 1;; esac; }
if ! is_canonical "$START_HTTP" || ! is_canonical "$LOGIN_HTTP"; then
  cat >&2 <<EOF
ERROR: /start or /login returned unexpected HTTP (start=$START_HTTP, login=$LOGIN_HTTP).
  Canonical codes for c9e760d routes are 302 (real OAuth start) or
  401/403 (no session). Anything else means the api binary on the VPS
  is missing the route registration or the chi mux fell through.
  Fix on VPS:
    cd $VPS_DIR
    docker compose pull api || true
    docker compose up -d --build api
    docker compose logs --tail 50 api | grep -i 'oauth.*start\|register'
  Then re-run: $0 $WORKSPACE_ID
EOF
  exit 2
fi
if [[ "$START_HTTP" != "$LOGIN_HTTP" ]]; then
  cat >&2 <<EOF
ERROR: /start and /login return different status codes.
  This usually means the api binary on the VPS predates commit c9e760d
  (deploy lag) or the rebuild did not pick up pkg/api/modules.go:513.
  Fix on VPS:
    cd $VPS_DIR
    docker compose pull api || true
    docker compose up -d --build api
    docker compose logs --tail 50 api | grep -i 'oauth.*start\|register'
  Then re-run: $0 $WORKSPACE_ID
EOF
  exit 2
fi
echo "  OK: parity confirmed (both return HTTP=$START_HTTP)."

# ─── Interactive: operator drives the browser ───────────────────────
hr; echo "[4/8] ACTION (operator): open this URL in your browser" ; hr
if [[ "$MODE" = "batch" ]]; then
  cat <<EOF
  ⚠ NO_PROMPT=1 batch mode active.
  You MUST have already completed the TikTok OAuth consent in your browser
  BEFORE this script started; otherwise all signals will be empty.
EOF
fi
cat <<EOF
  https://api.instaedit.org/api/v1/auth/tiktok/start?workspace_id=$WORKSPACE_ID

Steps:
  (a) Browser should 302-redirect to https://www.tiktok.com/v2/auth/...
  (b) Log in to TikTok + click "Allow"
  (c) Final redirect lands on
        https://api.instaedit.org/api/v1/auth/tiktok/callback?code=...&state=...

When the callback page has settled (200 or whatever the page shows),
press ENTER to capture the api-side signals.
EOF
if [[ "$MODE" = "batch" ]]; then
  echo "  (NO_PROMPT=1: skipping browser step; proceeding to log capture)"
else
  if ! read -rp "Press ENTER when done (or Ctrl-C to abort): "; then
    exit 4
  fi
fi

# ─── Capture (a) start hit + full slice ─────────────────────────────
hr; echo "[5/8] Capturing logs into $TMP_LOG" ; hr
SCP_OK=1
SSH_OK=1
if ssh "$VPS_HOST" "cd $VPS_DIR && docker compose logs --since $SINCE api 2>&1 \
  | grep -i tiktok > /tmp/tiktok-e2e-remote.log"; then
  if ! scp -q -o ServerAliveInterval=15 -o ServerAliveCountMax=2 \
       "$VPS_HOST:/tmp/tiktok-e2e-remote.log" "$TMP_LOG" 2>/dev/null; then
    echo "(scp_failed)" > "$TMP_LOG"
    echo "WARN: scp from $VPS_HOST failed; sentinel stored" >&2
    SCP_OK=0
  fi
else
  echo "(ssh_failed_no_remote_capture)" > "$TMP_LOG"
  echo "WARN: ssh capture on $VPS_HOST failed; sentinel stored" >&2
  SSH_OK=0
fi
# Unconditional remote cleanup (best-effort).
ssh "$VPS_HOST" "rm -f /tmp/tiktok-e2e-remote.log" 2>/dev/null || true
echo "  $(wc -l < "$TMP_LOG") tiktok-matching lines captured."

# ─── Signal (a) start hit ───────────────────────────────────────────
hr; echo "[6a/8] (a) /start hit" ; hr
SIG_A=$(grep -ciE 'tiktok.*start' "$TMP_LOG" 2>/dev/null || echo 0)
echo "  matches: $SIG_A"
grep -iE 'tiktok.*start' "$TMP_LOG" | tail -3 || true
[[ "$SIG_A" -gt 0 ]] || FAIL_A=1

# ─── Signal (b) callback received ───────────────────────────────────
hr; echo "[6b/8] (b) callback received" ; hr
SIG_B=$(grep -ciE 'tiktok.*callback' "$TMP_LOG" 2>/dev/null || echo 0)
echo "  matches: $SIG_B"
grep -iE 'tiktok.*callback' "$TMP_LOG" | tail -3 || true
[[ "$SIG_B" -gt 0 ]] || FAIL_B=1

# ─── Signal (c) code→token exchange ─────────────────────────────────
hr; echo "[6c/8] (c) code→token exchange" ; hr
SIG_C=$(grep -ciE 'tiktok.*(token|exchange)' "$TMP_LOG" 2>/dev/null || echo 0)
echo "  matches: $SIG_C"
grep -iE 'tiktok.*(token|exchange)' "$TMP_LOG" | tail -3 || true
[[ "$SIG_C" -gt 0 ]] || FAIL_C=1

# ─── Signal (d) DB row ──────────────────────────────────────────────
hr; echo "[6d/8] (d) connection_states row for provider=tiktok" ; hr
DB_ROW=$(ssh "$VPS_HOST" "cd $VPS_DIR && docker compose exec -T postgres \
  psql -U instaedit -A -t -F'|' -c \
    \"SELECT id, user_id, external_account_id, created_at \
     FROM connection_states \
     WHERE provider='tiktok' \
     ORDER BY created_at DESC LIMIT 1\"")
SIG_D=0
if [[ -n "$DB_ROW" ]]; then
  SIG_D=1
  IFS='|' read -r DB_ID DB_USER DB_EXT DB_TS <<<"$DB_ROW"
  echo "  row found: id=$DB_ID user=$DB_USER external=$DB_EXT created=$DB_TS"
else
  echo "  NO ROW for provider=tiktok in connection_states."
fi

# ─── Verdict ────────────────────────────────────────────────────────
hr; echo "[7/8] Verdict" ; hr
echo "  (a) /start hit:           $([[ "$SIG_A" -gt 0 ]] && echo GREEN || echo RED)"
echo "  (b) callback received:    $([[ "$SIG_B" -gt 0 ]] && echo GREEN || echo RED)"
echo "  (c) code→token exchange:  $([[ "$SIG_C" -gt 0 ]] && echo GREEN || echo RED)"
echo "  (d) DB row exists:        $([[ "$SIG_D" -gt 0 ]] && echo GREEN || echo RED)"
echo "  (s) ssh capture:          $([[ "$SSH_OK" -eq 1 ]] && echo GREEN || echo RED)"
echo "  (p) scp capture:          $([[ "$SCP_OK" -eq 1 ]] && echo GREEN || echo RED)"

VERDICT_NOTE="TikTok OAuth E2E via /start alias: (a)$([[ "$SIG_A" -gt 0 ]] && echo ok || echo fail) (b)$([[ "$SIG_B" -gt 0 ]] && echo ok || echo fail) (c)$([[ "$SIG_C" -gt 0 ]] && echo ok || echo fail) (d)$([[ "$SIG_D" -gt 0 ]] && echo ok || echo fail) — workspace=$WORKSPACE_ID (c9e760d)"

# ─── Emit probe-log row (paste into docs/VPS-DEPLOY-STATUS.md) ─────
# Use printf (NOT quoted/unquoted heredoc) so the backticks around the IP
# stay literal and there is no command-substitution footprint inside the row.
hr; echo "[8/8] Probe-log row (paste into docs/VPS-DEPLOY-STATUS.md)" ; hr
printf '| %s | `51.91.11.36` | Caddy | %s | %s |\n' \
  "$PROBE_TS" \
  "$([[ "$SIG_A" -gt 0 && "$SIG_D" -gt 0 ]] && echo 200 || echo 503)" \
  "$VERDICT_NOTE"

hr
if [[ "${FAIL_A:-0}${FAIL_B:-0}${FAIL_C:-0}" != "000" || "$SIG_D" -eq 0 ]]; then
  cat >&2 <<EOF
FAILED: one or more signals missing. Common causes:
  - /api/v1/auth/tiktok/start was 302-equivalent of /login at step [3/8] so
    the route IS wired (Caddy + modules.go parity green), but the OAuth
    handshake failed. Inspect:
      grep -i error "$TMP_LOG"
    or check the last 100 tiktok lines:
      tail -100 "$TMP_LOG"
  - TikTok client_id / client_secret mismatch (check TIKTOK_CLIENT_KEY and
    TIKTOK_CLIENT_SECRET in the VPS .env).
  - workspace_id is not yours (the OAuthSessionRedirect middleware rejects
    on owner mismatch). Pick a workspace your logged-in TikTok account owns.
EOF
  exit 3
fi
echo "GREEN: paste the row above into docs/VPS-DEPLOY-STATUS.md."
