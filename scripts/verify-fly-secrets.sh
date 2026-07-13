#!/usr/bin/env bash
#
# scripts/verify-fly-secrets.sh — Asserts the `instaedit-login` Fly app's
# secrets are clean (no <redacted> placeholders, no disabled-provider
# keys, all required keys present). Runs after `make fly-secrets` and
# before `make fly-deploy` to catch mistakes before they reach prod.
#
# ─── SCOPE ───────────────────────────────────────────────────────────────
# This script does THREE checks:
#   1. No `<redacted>` placeholder survived from a previous deploy
#      (Fly's `secrets list` returns the literal key value, so a
#      placeholder string would be visible here.)
#   2. No disabled-provider key prefix (TIKTOK_*, X_*, X_CLIENT_*,
#      YOUTUBE_*, LINKEDIN_*, STRIPE_*) is registered on the app.
#   3. All keys from scripts/required-fly-secrets.txt are present.
#
# It does NOT validate VALUE FORMAT (e.g. that ENCRYPTION_KEYS is
# well-formed CSV with uint32 ids). The Fly secrets list API only
# returns the key name + a server-side digest — the value never
# leaves Fly. Value-format validation is the set script's job
# (scripts/_parse_envfile.py rejects bad values BEFORE push).
#
# ─── USAGE ──────────────────────────────────────────────────────────────
#   ./scripts/verify-fly-secrets.sh
#   # or: make fly-secrets-verify
#
# Exit codes:
#   0  all checks passed
#   1  pre-flight failure (no flyctl, not authed, sibling files missing)
#   2  bad CLI argument
#   3  assertion failure (<redacted> / disabled-provider / missing key)

set -euo pipefail

APP_NAME="instaedit-login"

# Sibling-file pattern: source the same lists the set script uses, so
# the two scripts can never silently disagree on what counts as
# "required" or "disabled". Adding a 16th key means editing the .txt
# file + both scripts automatically pick it up.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REQUIRED_FILE="$SCRIPT_DIR/required-fly-secrets.txt"
DISABLED_FILE="$SCRIPT_DIR/disabled-fly-secrets-prefixes.txt"

# ─── Pre-flight ──────────────────────────────────────────────────────────
command -v flyctl >/dev/null 2>&1 || {
  echo "❌ flyctl not installed. See https://fly.io/docs/hands-on/install-flyctl/" >&2
  exit 1
}
flyctl auth whoami >/dev/null 2>&1 || {
  echo "❌ Not authenticated with Fly.io. Run: flyctl auth login" >&2
  exit 1
}
for sibling in "$REQUIRED_FILE" "$DISABLED_FILE"; do
  if [[ ! -f "$sibling" ]]; then
    echo "❌ Missing sibling file: $sibling" >&2
    exit 1
  fi
done

# ─── Load the shared key lists ──────────────────────────────────────────
# Same one-per-line format the python parser uses: # comments + blank
# lines stripped. We use grep + mapfile (no associative array needed).
mapfile -t REQUIRED_KEYS < <(grep -v '^[[:space:]]*#' "$REQUIRED_FILE" | grep -v '^[[:space:]]*$')
mapfile -t DISABLED_PREFIXES < <(grep -v '^[[:space:]]*#' "$DISABLED_FILE" | grep -v '^[[:space:]]*$')

if [[ ${#REQUIRED_KEYS[@]} -eq 0 ]]; then
  echo "❌ No required keys loaded from $REQUIRED_FILE — check the file." >&2
  exit 1
fi
# Empty DISABLED_PREFIXES would build a regex like `^()[A-Z0-9_]*([[:space:]]|$)`
# which matches EVERY key. Fail closed instead of silently disabling the
# guard — beta scope requires the disabled-prefix check to be active.
if [[ ${#DISABLED_PREFIXES[@]} -eq 0 ]]; then
  echo "❌ No disabled-provider prefixes loaded from $DISABLED_FILE — check the file." >&2
  echo "   An empty list would build a regex that matches every key (fail-open)." >&2
  exit 1
fi

# ─── Capture secrets list ───────────────────────────────────────────────
# `flyctl secrets list` returns NAME + DIGEST (server-side). The
# plaintext value never leaves Fly's servers, so this output is safe
# to print to stdout for operator audit.
raw=$(flyctl secrets list --app "$APP_NAME")

# Print for operator audit
echo "── Secrets currently set on $APP_NAME ────────────────────────────"
echo "$raw"
echo

# ─── Assertion 1: no <redacted> placeholders ─────────────────────────────
if echo "$raw" | grep -qF '<redacted>'; then
  echo "❌ FAIL: <redacted> placeholder found in deployed secrets on $APP_NAME." >&2
  echo "  A previous version pushed the literal '<redacted>' string." >&2
  echo "  Identify + unset: flyctl secrets unset <KEY> --app $APP_NAME" >&2
  exit 3
fi
echo "✓ No <redacted> placeholders"

# ─── Assertion 2: no disabled-provider keys ─────────────────────────────
# Build the regex from the shared prefix list. Each entry is matched
# as a key-name prefix at the start of a line, followed by either
# whitespace (Fly's `secrets list` columns are whitespace-separated)
# or end-of-line. This catches: STRIPE_*, TIKTOK_CLIENT_*, X_FOO_*,
# X_CLIENT_KEY, etc. — the operator doesn't have to enumerate every
# specific key, just the prefixes.
disabled_regex="^($(IFS='|'; echo "${DISABLED_PREFIXES[*]:-}"))[A-Z0-9_]*([[:space:]]|$)"
if echo "$raw" | grep -qE "$disabled_regex"; then
  echo "❌ FAIL: disabled-provider secret registered on $APP_NAME:" >&2
  echo "$raw" | grep -E "$disabled_regex" >&2
  echo "  Beta scope excludes TikTok, X, YouTube, LinkedIn, Stripe." >&2
  echo "  Identify + unset: flyctl secrets unset <KEY> --app $APP_NAME" >&2
  exit 3
fi
echo "✓ No disabled-provider keys"

# ─── Assertion 3: all required keys present ─────────────────────────────
# `flyctl secrets list` prints one row per key, with the key name in
# the first column. Skip the header row (NR>1) and extract column 1.
first_col=$(echo "$raw" | awk 'NR>1 {print $1}')
missing=()
for key in "${REQUIRED_KEYS[@]}"; do
  if ! echo "$first_col" | grep -qxF "$key"; then
    missing+=( "$key" )
  fi
done
if [[ "${#missing[@]}" -gt 0 ]]; then
  echo "❌ FAIL: missing required keys on $APP_NAME:" >&2
  for k in "${missing[@]}"; do
    echo "    - $k" >&2
  done
  echo "  Run: make fly-secrets" >&2
  exit 3
fi
echo "✓ All ${#REQUIRED_KEYS[@]} required keys present"

echo
echo "✓ All checks passed. Secrets on $APP_NAME are clean."
echo "Next: make fly-deploy"
