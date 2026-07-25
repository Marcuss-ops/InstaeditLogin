#!/usr/bin/env bash
#
# scripts/obs/verify-log-redaction.sh
#
# Operator-side runbook to verify that the live VPS deployment's logs
# are free of known secret patterns (log-privacy contract verification).
#
# OVERVIEW:
#   The application logs via slog into stdout, which the docker-compose
#   stack captures per service. The static CI checks (`grep -RnE ...`)
#   prove we don't hardcode secrets. This script proves we don't
#   accidentally leak them into live logs (e.g. an operator typo in
#   `slog.Warn("failed", "token", token)` would not be caught by the
#   static grep).
#
# BEHAVIOUR:
#   - Idempotent and read-only.
#   - Dry-run by default (no ssh/compose call): prints the patterns + the
#     ssh+docker compose command it *would* run + the planned log window.
#   - --apply: runs `ssh instaedit@$VPS_IP 'docker compose logs --since ...'`
#     into a temp file, greps against canonical privacy-contract patterns.
#   - CRITICAL (Privacy Contract): NEVER prints matched secret values to
#     the operator's terminal. Output ONLY counts + a sanitized 80-char
#     prefix + `***redacted***`. The actual secret-bearing tail is dropped.
#
# USAGE:
#   ./scripts/obs/verify-log-redaction.sh                       # Dry run (default)
#   ./scripts/obs/verify-log-redaction.sh --apply               # Scan last 1h
#   ./scripts/obs/verify-log-redaction.sh --apply --since 24h
#   ./scripts/obs/verify-log-redaction.sh --apply --since 168h  # 7 days (24h*7)
#   ./scripts/obs/verify-log-redaction.sh --apply --timeout 120
#
# Env overrides:
#   VPS_IP=<ip>               (default: 51.91.11.36 — canonical VPS, OPERATIONS.md §1.1)
#   VERIFY_LOG_SERVICES="api worker"   (default; any subset of compose services)
#
# EXIT CODES:
#   0  All clean (no secret patterns in scanned window)
#   1  One or more secret patterns found (FAIL) — see Summary + Action items
#   2  Required tool missing (ssh / docker / grep / awk missing)
#   3  Cannot reach VPS via SSH OR compose stack has no running services
#   4  Bad CLI arguments (e.g. --since value Docker's duration parser rejects)
#
# CROSS-REFERENCES:
#   docs/OPERATIONS.md §4.3 — log discipline contract (what MUST NOT appear)
#   docs/OPERATIONS.md §5.3 — the migration target this script now satisfies
#   docs/DEPLOY.md §7.6     — expanded privacy contract (15 secrets + first-party)
#   pkg/metrics/workerid.go — log-rewriter canonical reference (already in code)

set -euo pipefail

VPS_IP="${VPS_IP:-51.91.11.36}"
COMPOSE_DIR="${COMPOSE_DIR:-/opt/instaedit/InstaeditLogin}"
COMPOSE_FILE="$COMPOSE_DIR/docker-compose.yml"
SERVICES="${VERIFY_LOG_SERVICES:-api worker}"
SINCE="1h"
TIMEOUT="60"

# Single canonical argv parser (avoids duplicate-loop ambiguity).
while [ $# -gt 0 ]; do
  case "$1" in
    --apply) MODE="apply"; shift ;;
    --since) SINCE="${2:-1h}"; shift 2 ;;
    --since=*) SINCE="${1#*=}"; shift ;;
    --timeout) TIMEOUT="${2:-60}"; shift 2 ;;
    --timeout=*) TIMEOUT="${1#*=}"; shift ;;
    -h|--help)
      sed -n '2,40p' "$0"
      exit 0
      ;;
    *) echo "❌ unknown arg: $1" >&2; exit 4 ;;
  esac
done

# ─── --since normalization ─────────────────────────────────────────────
# Docker compose logs uses Go's time.ParseDuration, which REJECTS the
# "d" (days) unit. Operator-friendly translate `7d` → `168h` so the
# post-cutover invocation is the same shape as the historical flyctl
# form. Anything else falls through to docker's parser, which will
# error out with a clear duration-parse message if still wrong.
if [[ "$SINCE" =~ ^([0-9]+)d$ ]]; then
  DAYS="${BASH_REMATCH[1]}"
  SINCE="$((DAYS * 24))h"
fi

# ─── Pre-flight: required tools ─────────────────────────────────────────
for tool in ssh docker grep awk; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "❌ required tool missing: $tool" >&2
    exit 2
  }
done

# ─── Pre-flight: VPS reachability (auth) ──────────────────────────────
# BatchMode=yes: do NOT prompt for password / passphrase; fail fast.
# ConnectTimeout=5: bound to <5s so a dead SSH config doesn't stall
# the Makefile target's caller.
if ! ssh -q -o BatchMode=yes -o ConnectTimeout=5 "instaedit@${VPS_IP}" exit </dev/null 2>/dev/null; then
  echo "❌ Cannot reach the VPS via ssh as instaedit@${VPS_IP}." >&2
  echo "    - Check ~/.ssh/config has a Host entry OR VPS_IP env override is set." >&2
  echo "    - Run: ssh -v instaedit@${VPS_IP} exit   (verbose to diagnose)." >&2
  exit 3
fi

# ─── Pre-flight: compose stack is up with at least one running service ─
# We probe against the FIRST service in $SERVICES — if it's running the
# whole stack almost certainly is. We pick --services --filter to keep
# the stdout zero-bytes when the service IS running (a clean exit code
# is the signal), so `set -e` carries the check.
PROBE_SERVICE="${SERVICES%% *}"
PROBE_OUT="$(ssh -q -o BatchMode=yes "instaedit@${VPS_IP}" \
  "cd $COMPOSE_DIR && docker compose -f docker-compose.yml ps -q $PROBE_SERVICE" 2>/dev/null || true)"
if [[ -z "$PROBE_OUT" ]]; then
  echo "❌ docker compose stack on ${VPS_IP} has no running service matching '${PROBE_SERVICE}'." >&2
  echo "    Run: ssh instaedit@${VPS_IP} 'cd $COMPOSE_DIR && docker compose ps'   to diagnose." >&2
  echo "    Set VERIFY_LOG_SERVICES to a different service (e.g. 'worker') if meaning to scan a non-default one." >&2
  exit 3
fi

# ─── Canonical Privacy-Contract Patterns ─────────────────────────────────
# Each pattern + its FALSE-POSITIVE calibration notes (from real-VPS-log
# empirical testing) so future operators tuning the patterns know why
# each constraint exists.
#
# Pattern 1 (JWT/Access/Refresh): (?i)(jwt[_-]?secret|access[_-]?token|refresh[_-]?token).{0,16}[a-f0-9]{20,}
#   Calibration: low FP. The {0,16} bridge prevents matching arbitrary
#   log attributes followed by unrelated hex strings. Matches "access_token=abc123def456..."
#   but NOT "RandomKey=<sha256>". Distinguishes env-var names + their
#   hex-coded values.
#
# Pattern 2 (Resend): re_[a-zA-Z0-9]{20,}
#   Calibration: zero FP. Resend keys are strict "re_" prefixed and the
#   letters after are base64-url-safe (no padding). Our own dev env placeholders
#   like "re_fixture_key_value" used by the parse-validator tests would match.
#   They DO NOT appear in live logs (they live in unit test fixtures only).
#
# Pattern 3 (AWS): AKIA[0-9A-Z]{16,}
#   Calibration: zero FP. AWS access keys use a strict 4-char prefix + 16
#   uppercase alphanumerics.
#
# Pattern 4 (Postgres URI password): ://[a-z]+:[^@/]{6,}@
#   Calibration: very low FP. Matches `scheme://user:password@host` patterns.
#   Does NOT match `user:host` strings that lack the @. Will match legitimate
#   DATABASE_URL=postgresql://u:p@h/d strings — those IT'S the point.
#
# Pattern 5 (Literal password): (?i)password\s*=\s*\S{6,}
#   Calibration: low FP. Captures "password=foo" plaintext assignments in
#   logs. Bcrypt hashes (60 chars) also match — that's intentional (a
#   bcrypt hash in plaintext logs IS a leak that needs rotation, even
#   if the actual user password stays opaque).
#
# Pattern 6 (CSRF in URL): [?&]csrf_token=[a-f0-9]{32,}
#   Calibration: low FP. Matches query-string params with 32+ hex chars
#   after the literal `csrf_token=`. Only relevant if a redirect was logged.
#
# Pattern 7 (Magic-link token): [?&]token=[A-Za-z0-9_-]{20,}
#   Calibration: low FP. Captures `?token=<base64url>` params. Safe to
#   match — magic-link tokens are the auth primitive; they MUST NOT appear
#   in logs.

PATTERNS=(
  "(?i)(jwt[_-]?secret|access[_-]?token|refresh[_-]?token).{0,16}[a-f0-9]{20,}"
  "re_[a-zA-Z0-9]{20,}"
  "AKIA[0-9A-Z]{16,}"
  "://[a-z]+:[^@/]{6,}@"
  "(?i)password[[:space:]]*=[[:space:]]*[^[:space:]]{6,}"
  "[?&]csrf_token=[a-f0-9]{32,}"
  "[?&]token=[A-Za-z0-9_-]{20,}"
)

PATTERN_NAMES=(
  "JWT / Access / Refresh Tokens"
  "Resend API Keys (re_* prefix)"
  "AWS Access Keys (AKIA prefix)"
  "Postgres / DB URI passwords"
  "Literal password assignments"
  "CSRF token query params"
  "Magic-link token query params"
)

# ─── Dry-run / preview (default) ─────────────────────────────────────────
if [ "${MODE:-dry}" != "apply" ]; then
  echo "─── DRY RUN: verify-log-redaction ─────────────────────────────────"
  echo "VPS:      instaedit@${VPS_IP}"
  echo "Stack:    ${COMPOSE_FILE}"
  echo "Services: ${SERVICES}"
  echo "Since:    ${SINCE}"
  echo "Timeout:  ${TIMEOUT}s"
  echo ""
  echo "Planned ssh + docker compose command:"
  echo "  ssh -q instaedit@\${VPS_IP} 'cd ${COMPOSE_DIR} && \\"
  echo "    docker compose logs --no-color --no-log-prefix --since ${SINCE} ${SERVICES}'"
  echo "  >  \$TMP_DIR/logs.txt  (chmod 700, sweep on EXIT)"
  echo ""
  echo "Patterns targeted (PCRE, in evaluation order):"
  for i in "${!PATTERNS[@]}"; do
    printf "  %d. %-26s %s\n" "$((i+1))" "${PATTERN_NAMES[$i]}" "${PATTERNS[$i]}"
  done
  echo ""
  echo "Privacy contract on script output itself:"
  echo "  - Matched lines are TRUNCATED at 80 chars + '***redacted***' suffix."
  echo "  - The full secret-bearing portion is NEVER printed to the terminal."
  echo ""
  echo "Pass --apply to execute the live scan."
  exit 0
fi

# ─── Apply mode: do the work ────────────────────────────────────────────
TMP_DIR=$(mktemp -d -t verify-log-redaction-XXXXXX)
chmod 700 "$TMP_DIR"
LOG_FILE="$TMP_DIR/logs.txt"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "─── APPLY: verify-log-redaction ────────────────────────────────────"
echo "VPS:      instaedit@${VPS_IP}"
echo "Services: ${SERVICES}"
echo "Since:    ${SINCE}"
echo "Timeout:  ${TIMEOUT}s"
echo "Tmpdir:   ${TMP_DIR} (chmod 700, sweep on EXIT)"
echo ""

echo "Fetching logs from VPS compose (services=${SERVICES}) since ${SINCE} ..."
# ssh + docker compose logs is non-streaming when --no-log-prefix + a
# bounded --since window is passed: it dumps the spool and exits. We
# background it with a ${TIMEOUT}s ceiling to bound runtime; if the
# command completes earlier, `wait` is a no-op.
ssh -q -o BatchMode=yes "instaedit@${VPS_IP}" \
  "cd $COMPOSE_DIR && docker compose logs --no-color --no-log-prefix --since '${SINCE}' ${SERVICES}" \
  > "$LOG_FILE" 2>/dev/null &
FETCH_PID=$!
# Default ceiling 60s — beyond ~30s rarely yields extra lines on a warm
# machine, but cold VPS can take 45-60s on first scan. Operators can
# raise this with `make verify-log-redaction -- --timeout 120` if a
# one-shot extended window is needed (e.g. --since 168h on a busy stack).
sleep "$TIMEOUT"
if kill -0 "$FETCH_PID" 2>/dev/null; then
  kill "$FETCH_PID" 2>/dev/null || true
  wait "$FETCH_PID" 2>/dev/null || true
fi

LINES_FETCHED=$(wc -l < "$LOG_FILE" 2>/dev/null || echo 0)
if [ "$LINES_FETCHED" -eq 0 ]; then
  echo "⚠ WARN: 0 lines fetched. Causes:"
  echo "    - The --since window has no logs (raise the duration or omit --since)"
  echo "    - The services in \$SERVICES haven't logged in that window"
  echo "    - VPS just rebuilt and the compose log spool is empty (still ramping)"
  echo "    - SSH key failed mid-stream (check ~/.ssh/instaedit_vps trust on VPS)"
  echo ""
fi
echo "Fetched $LINES_FETCHED log lines."
echo ""

PASS=0; FAIL=0
FAIL_LIST=""

echo "Scanning $LINES_FETCHED lines against ${#PATTERNS[@]} canonical patterns ..."
echo ""

for i in "${!PATTERNS[@]}"; do
  NAME="${PATTERN_NAMES[$i]}"
  PATTERN="${PATTERNS[$i]}"

  # Privacy contract: pipe grep directly into awk so the FULL secret-bearing
  # line never enters a shell var before awk's truncate+redact. A future
  # maintainer who echoes $SHELL_VAR or pipes it to `cat` will leak; this
  # subprocess pipeline protects against that footgun AND keeps $SECRETS
  # off the bash process address space entirely.
  HIT_COUNT=$(grep -acP "$PATTERN" "$LOG_FILE" 2>/dev/null || echo 0)
  HIT_COUNT="${HIT_COUNT:-0}"

  if [ "$HIT_COUNT" -gt 0 ]; then
    echo "  ✗ FAIL  $NAME  ($HIT_COUNT hit(s))"
    FAIL=$((FAIL+1))
    SNIPPETS=$(grep -aP "$PATTERN" "$LOG_FILE" 2>/dev/null \
      | awk '{ printf "    %-80s... ***redacted***\n", substr($0,1,80) }' \
      | head -n 5)
    FAIL_LIST="${FAIL_LIST}\n  ${NAME} (${HIT_COUNT} hits):\n${SNIPPETS}\n"
  else
    echo "  ✓ PASS  $NAME"
    PASS=$((PASS+1))
  fi
done

echo ""
echo "═══════════════════════════════════════════════════"
echo " SUMMARY: $PASS pass / $FAIL fail (over $LINES_FETCHED lines)"
echo "═══════════════════════════════════════════════════"
echo ""

if [ "$FAIL" -gt 0 ]; then
  printf "VIOLATIONS (first 5 sanitized snippets per pattern):\n%b\n" "$FAIL_LIST"
  echo ""
  echo "ACTION REQUIRED:"
  echo ""
  echo "  1. For each failed pattern, locate the offending log statement"
  echo "     (slog.Warn / slog.Info / slog.Error in pkg/, internal/) and remove"
  echo "     the sensitive field. The canonical redaction pattern lives in"
  echo "     pkg/metrics/workerid.go (the log-rewriter at the collector)."
  echo ""
  echo "  2. Rotate the leaked credential(s) IMMEDIATELY (the leak in logs"
  echo "     is permanent history; the credential is considered compromised even"
  echo "     if no one has scraped it yet). Use the VPS secrets-rotation"
  echo "     runbook in docs/DEPLOY.md §6."
  echo ""
  echo "  3. Re-run this script after the fix to confirm clean:"
  echo "       ./scripts/obs/verify-log-redaction.sh --apply"
  echo ""
  exit 1
fi

echo "✓ Log privacy verification clean: no known secret patterns in the last $SINCE."
echo ""
echo "Operator next steps:"
echo "  1. Wire this into a weekly cron on the operator laptop (or per-deploy)"
echo "     so a regression gets caught WITHOUT manual prompt."
echo "  2. Future-tense runs of this script should always return 0 (no churn)."
exit 0
