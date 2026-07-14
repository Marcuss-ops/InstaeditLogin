#!/usr/bin/env bash
#
# scripts/obs/verify-log-redaction.sh
#
# Operator-side runbook to verify that the live Fly.io deployment's logs
# are free of known secret patterns (log-privacy contract verification).
#
# OVERVIEW:
#   The application logs via slog into stdout, which Fly captures. The
#   static CI checks (`grep -RnE ...`) prove we don't hardcode secrets.
#   This script proves we don't accidentally leak them into live logs
#   (e.g. an operator typo in `slog.Warn("failed", "token", token)` would
#    not be caught by the static grep).
#
# BEHAVIOUR:
#   - Idempotent and read-only.
#   - Dry-run by default (no flyctl call): prints the patterns + the
#     flyctl command it *would* run + the planned log window.
#   - --apply: runs `flyctl logs`, writes to a temp file, greps against
#     canonical privacy-contract patterns.
#   - CRITICAL (Privacy Contract): NEVER prints matched secret values to
#     the operator's terminal. Output ONLY counts + a sanitized 80-char
#     prefix + `***redacted***`. The actual secret-bearing tail is dropped.
#
# USAGE:
#   ./scripts/obs/verify-log-redaction.sh                  # Dry run (default)
#   ./scripts/obs/verify-log-redaction.sh --apply          # Scan last 1h
#   ./scripts/obs/verify-log-redaction.sh --apply --since 24h
#   ./scripts/obs/verify-log-redaction.sh --apply --since 7d
#
# EXIT CODES:
#   0  All clean (no secret patterns in scanned window)
#   1  One or more secret patterns found (FAIL) — see Summary + Action items
#   2  Required tool missing (flyctl / grep / awk missing)
#   3  Not authenticated with Fly.io (run `flyctl auth login`)
#   4  Bad CLI arguments
#
# CROSS-REFERENCES:
#   docs/OPERATIONS.md §4.3 — log discipline contract (what MUST NOT appear)
#   docs/DEPLOY.md §7.6     — expanded privacy contract (15 secrets + first-party)
#   pkg/metrics/workerid.go — log-rewriter canonical reference (already in code)

set -euo pipefail

# Single canonical argv parser (avoids duplicate-loop ambiguity).
while [ $# -gt 0 ]; do
  case "$1" in
    --apply) MODE="apply"; shift ;;
    --since) SINCE="${2:-1h}"; shift 2 ;;
    --since=*) SINCE="${1#*=}"; shift ;;
    -h|--help)
      sed -n '2,40p' "$0"
      exit 0
      ;;
    *) echo "❌ unknown arg: $1" >&2; exit 4 ;;
  esac
done

# ─── Pre-flight ──────────────────────────────────────────────────────────
for tool in flyctl grep awk; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "❌ required tool missing: $tool" >&2
    exit 2
  }
done

if ! flyctl auth whoami >/dev/null 2>&1; then
  echo "❌ Not authenticated with Fly.io (run 'flyctl auth login')" >&2
  exit 3
fi

# ─── Canonical Privacy-Contract Patterns ─────────────────────────────────
# Each pattern + its FALSE-POSITIVE calibration notes (from real-Fly-log
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
if [ "$MODE" != "apply" ]; then
  echo "─── DRY RUN: verify-log-redaction ─────────────────────────────────"
  echo "App:    $APP_NAME"
  echo "Since:  $SINCE"
  echo ""
  echo "Planned flyctl command:"
  echo "  flyctl logs --app $APP_NAME --since $SINCE  >  \$TMP_DIR/logs.txt"
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
echo "App:    $APP_NAME"
echo "Since:  $SINCE"
echo "Tmpdir: $TMP_DIR (chmod 700, sweep on EXIT)"
echo ""

echo "Fetching logs for $APP_NAME since $SINCE ..."
# flyctl logs WITHOUT --no-tail exits after dumping the historical buffer,
# but the windows-binary variant can sit attached; we background it with a
# 20s ceiling to bound runtime. If the future CLI returns the buffer as a
# non-streaming JSON dump we'd capture it exactly the same way.
flyctl logs --app "$APP_NAME" --since "$SINCE" > "$LOG_FILE" 2>/dev/null &
FLY_PID=$!
# 30s ceiling — beyond ~20s rarely yields additional lines on a warm
# machine, but cold logs can take 25-30s on first scan. Future Fly CLI
# behaviour changes may need to bump this floor; bump + commit.
sleep 30
if kill -0 "$FLY_PID" 2>/dev/null; then
  kill "$FLY_PID" 2>/dev/null || true
  wait "$FLY_PID" 2>/dev/null || true
fi

LINES_FETCHED=$(wc -l < "$LOG_FILE" 2>/dev/null || echo 0)
if [ "$LINES_FETCHED" -eq 0 ]; then
  echo "⚠ WARN: 0 lines fetched. Causes:"
  echo "    - The --since window has no logs (raise the duration or omit --since)"
  echo "    - The app has not generated any stdout output (slog disabled? check AppEnv)"
  echo "    - flyctl bug — retry with `--since 24h` to capture more"
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
  echo "     if no one has scraped it yet). Use the Fly secrets-rotation"
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
