#!/usr/bin/env bash
#
# scripts/verify-tiktok-app-review-config.sh
#
# Confirms the locally-configured TIKTOK_REDIRECT_URI matches the
# canonical production value documented for the VPS deployment. This is
# the TikTok-side mirror of scripts/verify-google-oauth-mode.sh —
# where the YouTube/Drive versions introspect a live token, this
# version introspects the *redirect URI* the App Review form needs.
#
# ─── Why this exists ────────────────────────────────────────────────────
# The 2026-07 TikTok App Review rejection ("demo video images
# pixelated …") was preceded by a second-class bug: the redirect
# URI registered on the TikTok Developer Portal was the operator's
# dev Cloudflare quick-tunnel URL (`*.trycloudflare.com`), which
# rotates its subdomain on every restart. A reviewer who tested the
# OAuth flow 2 hours after the form-submission landed on a dead URL.
#
# The codebase's canonical redirect URI is the production VPS value
# documented in docs/TIKTOK-APP-REVIEW.md:
# https://api.instaedit.org/api/v1/auth/tiktok/callback
# This script enforces that the operator's explicit environment
# (`.env.production`, `.env.dev`, or an exported variable) matches that
# value, so an accidental `*.trycloudflare.com` / `localhost` override
# is caught locally BEFORE the App Review form is submitted.
#
# ─── USAGE ──────────────────────────────────────────────────────────────
#   # from an explicit environment file (production first, then dev):
#   set -a; source .env.production; set +a
#   # or, for local development:
#   set -a; source .env.dev; set +a
#   ./scripts/verify-tiktok-app-review-config.sh
#
#   # or with the env var exported inline:
#   TIKTOK_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/tiktok/callback \
#     ./scripts/verify-tiktok-app-review-config.sh
#
#   # --help for the inline help banner:
#   ./scripts/verify-tiktok-app-review-config.sh --help
#
# ─── Exit codes ─────────────────────────────────────────────────────────
#   0  redirect URI matches the canonical production value
#   1  pre-flight failure (env var TIKTOK_REDIRECT_URI not set)
#   2  redirect URI is NOT the canonical (trycloudflare, localhost,
#      api-staging, or any in-review override)

set -euo pipefail

# ─── Canonical production value ────────────────────────────────────────
# Single string, period. If the production URL ever changes (e.g. a
# www→apex migration), update this script and
# docs/TIKTOK-APP-REVIEW.md together so the smoke check stays trustworthy.
CANONICAL_REDIRECT_URI="https://api.instaedit.org/api/v1/auth/tiktok/callback"

# ─── USAGE / --help banner ──────────────────────────────────────────────
# Print the file header as help; avoids a separate man page that's easy
# to forget about. Same convention as scripts/verify-google-oauth-mode.sh.
if [[ "${1:-}" == "--help" ]] || [[ "${1:-}" == "-h" ]]; then
  sed -n '2,40p' "$0"
  exit 0
fi

# ─── Pre-flight ─────────────────────────────────────────────────────────
# The env var must be set. If it is not exported, load only the
# explicit production or development file in the operator's CWD; never
# fall back to a generic root `.env`.
if [[ -z "${TIKTOK_REDIRECT_URI:-}" ]]; then
  if [[ -f .env.production ]]; then
    set -a; source .env.production; set +a
  elif [[ -f .env.dev ]]; then
    set -a; source .env.dev; set +a
  fi
fi

if [[ -z "${TIKTOK_REDIRECT_URI:-}" ]]; then
  echo "❌ TIKTOK_REDIRECT_URI is not set." >&2
  echo "   Source .env.production or .env.dev, or export it inline:" >&2
  echo "     set -a; source .env.production; set +a" >&2
  echo "     TIKTOK_REDIRECT_URI=$CANONICAL_REDIRECT_URI \\" >&2
  echo "       ./scripts/verify-tiktok-app-review-config.sh" >&2
  exit 1
fi

echo "── TIKTOK_REDIRECT_URI cross-check (TikTok App Review) ──────────────"
echo "  canonical    : $CANONICAL_REDIRECT_URI"
echo "  configured   : $TIKTOK_REDIRECT_URI"
echo ""

# ─── Canonical-case: matches the documented production value ──────────
# Both literals compared with = so glob chars (none in this case)
# would be safe, but use string compare explicitly to avoid surprises.
if [[ "$TIKTOK_REDIRECT_URI" == "$CANONICAL_REDIRECT_URI" ]]; then
  cat <<EOF
✓ Match.

  Paste this value verbatim into the TikTok Developer Portal:
    Login Kit → Redirect URI
    Content Posting API → Redirect URI

EOF
  echo "✓ Verification complete."
  exit 0
fi

# ─── Mismatch case ──────────────────────────────────────────────────────
# Don't fail silently — print every common false-positive case so the
# operator fixes root cause, not just the symptom.
echo "❌ Mismatch! TikTok App Review requires the canonical value." >&2
echo "" >&2

# Trycloudflare — the classic 2026-07 bug pattern. Cloudflare quick
# tunnels are NOT stable URLs.
if [[ "$TIKTOK_REDIRECT_URI" == *".trycloudflare.com"* ]]; then
  echo "   ⚠️  Detected a Cloudflare quick-tunnel URL (*.trycloudflare.com)." >&2
  echo "      These rotate their subdomain on every restart — a reviewer" >&2
  echo "      who tests the OAuth flow later will land on a 404." >&2
  echo "" >&2
fi

if [[ "$TIKTOK_REDIRECT_URI" == *"localhost"* ]] || [[ "$TIKTOK_REDIRECT_URI" == *"127.0.0.1"* ]]; then
  echo "   ⚠️  Detected a localhost URL." >&2
  echo "      This is the dev fallback in internal/config/config.go:510 —" >&2
  echo "      NOT acceptable for TikTok App Review, which requires a" >&2
  echo "      publicly reachable callback over HTTPS." >&2
  echo "" >&2
fi

if [[ "$TIKTOK_REDIRECT_URI" == *"staging"* ]] || [[ "$TIKTOK_REDIRECT_URI" == *"api-staging"* ]]; then
  echo "   ⚠️  Detected a staging URL." >&2
  echo "      The TikTok reviewer exercises the production OAuth flow," >&2
  echo "      so a staging callback returns 404 from the public internet." >&2
  echo "" >&2
fi

echo "   Fix:" >&2
echo "     export TIKTOK_REDIRECT_URI=\"$CANONICAL_REDIRECT_URI\"" >&2
echo "     # OR update the VPS production secret + redeploy + register the" >&2
echo "     # new URI with TikTok Developer Portal simultaneously." >&2
echo "" >&2
echo "   Reference: docs/TIKTOK-APP-REVIEW.md, §"Pre-submit checklist"." >&2
exit 2
