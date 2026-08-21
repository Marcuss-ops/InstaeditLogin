#!/usr/bin/env bash
# Verify the browserless Go CLI against staging.
# Read-only by default. Set APPLY_CLI_SMOKE=1 only when a disposable
# staging media file and editor session are intentionally supplied.
set -euo pipefail

BASE_URL="${INSTAEDIT_URL:-${BASE_URL:-}}"
API_KEY="${INSTAEDIT_API_KEY:-}"
CLI_BIN="${CLI_BIN:-./bin/instaedit}"
SESSION_ID="${INSTAEDIT_SESSION_ID:-}"
MEDIA_FILE="${INSTAEDIT_SMOKE_MEDIA_FILE:-}"
COVER_FILE="${INSTAEDIT_SMOKE_COVER_FILE:-}"
APPLY="${APPLY_CLI_SMOKE:-0}"

fail() { echo "ERROR: $*" >&2; exit 1; }

[ -n "$BASE_URL" ] || fail "INSTAEDIT_URL is required"
[ -n "$API_KEY" ] || fail "INSTAEDIT_API_KEY is required"
[ -x "$CLI_BIN" ] || fail "CLI binary is not executable: $CLI_BIN"

case "$BASE_URL" in
  https://*staging*|https://*staging.*|https://api-staging.*) ;;
  *) fail "refusing non-staging URL; use an HTTPS staging hostname (got $BASE_URL)" ;;
esac

case "$APPLY" in
  0|1) ;;
  *) fail "APPLY_CLI_SMOKE must be 0 or 1" ;;
esac

export INSTAEDIT_URL="$BASE_URL"
export INSTAEDIT_API_KEY="$API_KEY"

# Never print the API key. The CLI's read-only probe requires only write scope.
if [ -n "$SESSION_ID" ]; then
  "$CLI_BIN" youtube editor-session get --session "$SESSION_ID" >/tmp/instaedit-cli-session.json
  echo "PASS: authenticated editor-session read"
else
  echo "SKIP: set INSTAEDIT_SESSION_ID to run the authenticated read-only probe"
fi

if [ "$APPLY" = "1" ]; then
  [ -n "$MEDIA_FILE" ] || fail "INSTAEDIT_SMOKE_MEDIA_FILE is required when APPLY_CLI_SMOKE=1"
  [ -n "$COVER_FILE" ] || fail "INSTAEDIT_SMOKE_COVER_FILE is required when APPLY_CLI_SMOKE=1"
  [ -n "$SESSION_ID" ] || fail "INSTAEDIT_SESSION_ID is required when APPLY_CLI_SMOKE=1"
  [ -f "$MEDIA_FILE" ] || fail "media file not found: $MEDIA_FILE"
  [ -f "$COVER_FILE" ] || fail "cover file not found: $COVER_FILE"

  echo "WARNING: APPLY_CLI_SMOKE=1 will upload media and mutate the supplied staging editor session."
  "$CLI_BIN" media upload "$MEDIA_FILE"
  "$CLI_BIN" youtube cover-and-publish \
    --session "$SESSION_ID" \
    --cover "$COVER_FILE" \
    --privacy "${INSTAEDIT_SMOKE_PRIVACY:-unlisted}"
  echo "PASS: explicit staging mutation flow"
else
  echo "PASS: read-only staging smoke completed"
  echo "INFO: set APPLY_CLI_SMOKE=1 with disposable inputs to test upload/thumbnail/publish"
fi
