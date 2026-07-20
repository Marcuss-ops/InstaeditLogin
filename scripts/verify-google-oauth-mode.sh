#!/usr/bin/env bash
#
# scripts/verify-google-oauth-mode.sh — Quick health check for a Google
# OAuth access token issued by InstaEdit. Calls Google's public
# tokeninfo introspection endpoint and prints the aud / expires_in /
# scope / azp fields so an operator can confirm at a glance:
#
#   * `aud`  — which OAuth client_id the token was issued to
#              (compare against YOUTUBE_CLIENT_ID in .env.production
#              to confirm Production vs Testing credentials).
#   * `expires_in` — the access token's remaining TTL in seconds.
#                    Access tokens are short-lived (~1 hour); this
#                    value DECREASES over time. A negative or zero
#                    value means the token is expired.
#   * `scope` — the space-delimited list of scopes the token carries.
#               Cross-check against docs/OAUTH-PRODUCTION.md Step 3.
#   * `azp`   — the authorized party (the client that requested the
#               token). For web-server-flow InstaEdit tokens, azp
#               should equal aud. A mismatch is suspicious.
#
# Note: this script introspects the ACCESS TOKEN, not the refresh
# token. Refresh-token TTL is not exposed by this endpoint — see
# docs/OAUTH-PRODUCTION.md "Monitoring refresh-token TTL" for the
# server-side monitoring contract.
#
# Endpoint:
#   GET https://www.googleapis.com/oauth2/v3/tokeninfo?access_token=...
# (the v3 path is the canonical public introspection URL;
# oauth2.googleapis.com/tokeninfo is the newer alias and returns the
# same shape — both are accepted.)
#
# ─── USAGE ──────────────────────────────────────────────────────────────
#   ./scripts/verify-google-oauth-mode.sh "$OAUTH_ACCESS_TOKEN"
#   # or pipe from a file:
#   ./scripts/verify-google-oauth-mode.sh < token.txt
#   # or export:
#   GOOGLE_OAUTH_ACCESS_TOKEN=ya29.... ./scripts/verify-google-oauth-mode.sh
#
# Exit codes:
#   0  token validated; fields printed
#   1  pre-flight failure (no curl / no jq / no token supplied)
#   2  bad CLI argument
#   3  network error (curl could not reach Google)
#   4  Google rejected the token (HTTP 4xx response — token is
#      expired, revoked, or malformed)
#   5  Google returned a malformed response (unexpected JSON shape)

set -euo pipefail

TOKENINFO_URL="https://www.googleapis.com/oauth2/v3/tokeninfo"

# ─── Parse args: --help FIRST (before positional capture) ───────────────
# This avoids the trap where `./verify-google-oauth-mode.sh -h` would
# treat `-h` as the access token and forward it to curl as
# `?access_token=-h` (returning a confusing Google 400 + exit 4).
if [[ $# -ge 1 ]]; then
  case "$1" in
    -h|--help)
      sed -n '2,40p' "$0"
      exit 0
      ;;
  esac
fi

# ─── Resolve the access token from one of three sources ────────────────
# Priority: 1st positional arg > stdin > GOOGLE_OAUTH_ACCESS_TOKEN env.
# The operator usually has the token in an env var from a previous
# command, so reading from env is the most ergonomic path. Stdin
# support lets the script slot into a curl pipeline.
TOKEN=""
if [[ $# -ge 1 ]]; then
  TOKEN="$1"
elif [[ ! -t 0 ]]; then
  # Strip trailing whitespace/newlines (curl needs a clean value).
  TOKEN="$(cat | tr -d '[:space:]')"
fi
if [[ -z "${TOKEN:-}" ]] && [[ -n "${GOOGLE_OAUTH_ACCESS_TOKEN:-}" ]]; then
  TOKEN="${GOOGLE_OAUTH_ACCESS_TOKEN}"
fi

# ─── Pre-flight ──────────────────────────────────────────────────────────
command -v curl >/dev/null 2>&1 || {
  echo "❌ curl not installed." >&2
  exit 1
}
command -v jq >/dev/null 2>&1 || {
  echo "❌ jq not installed (required to parse the tokeninfo JSON response)." >&2
  echo "   Install: brew install jq   |   apt-get install jq" >&2
  exit 1
}

if [[ -z "${TOKEN:-}" ]]; then
  echo "❌ No access token supplied." >&2
  echo "   Usage: $0 <access_token>" >&2
  echo "      or: GOOGLE_OAUTH_ACCESS_TOKEN=<token> $0" >&2
  echo "      or: $0 < token.txt" >&2
  exit 1
fi

# ─── Call the tokeninfo endpoint ────────────────────────────────────────
# Use --fail-with-body so curl exits non-zero on 4xx/5xx but STILL
# writes the response body to stdout (we need the error JSON to tell
# the operator WHY Google rejected the token — e.g. "Invalid token").
# --silent silences the progress meter; --show-error keeps the
# connection-level error visible on stderr.
#
# Use mktemp (not a hardcoded /tmp path) so two concurrent
# invocations (e.g. CI matrix + a manual run) don't stomp on each
# other's response body. The trap-on-exit ensures cleanup even on
# non-zero exits.
BODY_FILE="$(mktemp -t verify-google-oauth-mode.XXXXXX)"
trap 'rm -f "$BODY_FILE"' EXIT

echo "── Calling $TOKENINFO_URL ──────────────────────────────────────────"
set +e  # We handle curl's exit code explicitly below.
http_code=$(
  curl --silent --show-error --fail-with-body \
       --get \
       --data-urlencode "access_token=${TOKEN}" \
       --write-out '\n%{http_code}' \
       "$TOKENINFO_URL" \
    | tee "$BODY_FILE" \
    | tail -n1
)
curl_status=$?
set -e
if [[ $curl_status -ne 0 ]]; then
  # curl exited non-zero. The body file still holds the response;
  # surface it so the operator can see Google's error message.
  echo "❌ curl could not complete the request (exit $curl_status)." >&2
  if [[ -s "$BODY_FILE" ]]; then
    echo "   Response body:" >&2
    cat "$BODY_FILE" >&2
    echo >&2
  fi
  # 4xx → token rejected (expired / revoked / malformed)
  # 5xx → Google-side outage; treat as network-class failure
  if [[ "$http_code" =~ ^4 ]]; then
    exit 4
  fi
  exit 3
fi

body="$(cat "$BODY_FILE")"
# Body file is removed by the EXIT trap — no need to rm here.

# ─── Parse the response ────────────────────────────────────────────────
# tokeninfo returns JSON; bail with exit 5 if it's not valid JSON or
# the canonical fields are missing.
if ! echo "$body" | jq -e . >/dev/null 2>&1; then
  echo "❌ Google returned a non-JSON response:" >&2
  echo "$body" >&2
  exit 5
fi
if ! echo "$body" | jq -e 'has("aud") and has("expires_in")' >/dev/null 2>&1; then
  echo "❌ Google response is missing the expected aud / expires_in fields:" >&2
  echo "$body" >&2
  exit 5
fi

# ─── Print the verification report ─────────────────────────────────────
aud="$(echo "$body" | jq -r '.aud')"
azp="$(echo "$body" | jq -r '.azp // "(absent — single-party flow)"')"
scope="$(echo "$body" | jq -r '.scope')"
expires_in="$(echo "$body" | jq -r '.expires_in')"
issued_to="$(echo "$body" | jq -r '.issued_to // "(not set — pre-2018 token)"')"
email="$(echo "$body" | jq -r '.email // "(not present in tokeninfo response)"')"
verified_email="$(echo "$body" | jq -r '.email_verified // "(not present in tokeninfo response)"')"

# Guard the human-readable TTL computation: jq returns a string, and
# if Google ever emits a fractional or quoted number (shouldn't, but
# defensive), $((expr)) would explode under set -e. Validate integer
# first, fall back to "(unparseable)" if it isn't.
if [[ "$expires_in" =~ ^[0-9]+$ ]]; then
  # `10#` forces base-10 interpretation so a leading-zero token can't
  # confuse the shell arithmetic parser.
  expires_human="$(printf '%dh %02dm %02ds' \
    $((10#$expires_in / 3600)) \
    $(((10#$expires_in % 3600) / 60)) \
    $((10#$expires_in % 60)))"
else
  expires_human="(unparseable expires_in=${expires_in})"
fi

cat <<EOF

✓ Google accepted the token (HTTP $http_code)

  aud (client_id)  : $aud
  azp              : $azp
  issued_to        : $issued_to
  email            : $email
  email_verified   : $verified_email
  expires_in       : ${expires_in}s (~${expires_human})
  scope            : $scope

EOF

# ─── Env-var cross-check: aud ↔ YOUTUBE_CLIENT_ID ─────────────────────
# The doc (docs/OAUTH-PRODUCTION.md Step 7) tells operators to sanity-
# check that aud matches YOUTUBE_CLIENT_ID in .env.production. Do the
# check inline so the script catches a Testing-vs-Production mismatch
# instead of relying on the operator to eyeball-compare two long
# strings. Skipped if YOUTUBE_CLIENT_ID is not in the env (the script
# is also useful for ad-hoc token introspection).
if [[ -n "${YOUTUBE_CLIENT_ID:-}" ]]; then
  if [[ "$aud" == "$YOUTUBE_CLIENT_ID" ]]; then
    echo "✓ aud matches YOUTUBE_CLIENT_ID from the environment." 
  else
    echo "❌ aud mismatch! Token was issued by $aud," >&2
    echo "   but YOUTUBE_CLIENT_ID in the env is $YOUTUBE_CLIENT_ID." >&2
    echo "   This usually means the token was issued by the Testing-mode" >&2
    echo "   client (or a different OAuth client entirely)." >&2
    # Don't exit non-zero — this is advisory. The operator may be
    # deliberately using a token from a different client (e.g. a
    # parallel pre-prod client). Just make the mismatch loud.
  fi
else
  echo "ℹ️  YOUTUBE_CLIENT_ID not set in env; skipping aud cross-check." 
  echo "   Export it from .env.production for an automated mismatch alert." >&2
fi

# ─── Sanity checks (advisory — never block on these) ──────────────────
# 1. aud ↔ azp: for web-server-flow InstaEdit tokens they should match.
if [[ "$azp" != "(absent — single-party flow)" ]] && [[ "$aud" != "$azp" ]]; then
  echo "⚠️  aud ($aud) != azp ($azp). Unusual for a web-server-flow token;" >&2
  echo "   investigate if this token wasn't issued by the expected client." >&2
fi

# 2. expires_in: zero or negative means the access token is already dead.
if [[ "$expires_in" =~ ^[0-9]+$ ]] && [[ "$expires_in" -le 0 ]]; then
  echo "⚠️  expires_in is $expires_in — this access token has already expired." >&2
  echo "   Use the refresh token (vault.Renew on the server) to get a fresh one." >&2
fi

# 3. expected scopes — soft cross-check against docs/OAUTH-PRODUCTION.md.
#    The InstaEdit YouTube grant must carry youtube.upload to call
#    videos.insert and youtube.readonly to read channel metadata
#    (e.g. channels.list(mine=true)). Missing either scope is a
#    misconfiguration worth flagging, but the check is advisory so
#    single-purpose debugging tokens are not blocked.
if ! grep -q "youtube.upload" <<<"$scope"; then
  echo "⚠️  Scope set does NOT contain youtube.upload." >&2
  echo "   The token can authenticate but cannot call videos.insert." >&2
fi
if ! grep -q "youtube.readonly" <<<"$scope"; then
  echo "⚠️  Scope set does NOT contain youtube.readonly." >&2
  echo "   The token can authenticate but cannot call channels.list for binding validation." >&2
fi

echo "✓ Verification complete."