#!/usr/bin/env bash
# scripts/ops/nvidia_publish_e2e_smoke.sh
#
# End-to-end smoke test for the NVIDIA metadata publish flow.
# Exercises the complete InstaEditor → YouTube publish pipeline:
#
#   1. Magic-link login (or reuse existing session)
#   2. POST /api/v1/media/presign → get upload URL
#   3. PUT thumbnail to presigned URL (MinIO/S3)
#   4. POST /api/v1/media/{asset_id}/complete → mark asset ready
#   5. PATCH /api/v1/youtube/editor-sessions/by-project/{project_id}
#      → attach thumbnail to session
#   6. POST /api/v1/youtube/editor-sessions/by-project/{project_id}/publish
#      → publish with full NVIDIA metadata (title, description, tags,
#        translations en/es/pt-BR, default_language, privacy_status)
#   7. Verify response shape: status, public_url, video_id, privacy_status,
#      actual_privacy, youtube_sync_status
#
# Requirements:
#   - A Velox project ID with a YouTube video already in "editing" state
#   - Valid session cookie (obtain via magic-link or paste from browser)
#   - The project must have a YouTube OAuth connection with upload scope
#
# Usage:
#   # Full flow with auto-login (dev environment with magic-link token):
#   VELOX_PROJECT_ID=vp-12345 ./scripts/ops/nvidia_publish_e2e_smoke.sh
#
#   # With an existing session cookie (skip login):
#   SESSION_COOKIE="your-session-jwt" VELOX_PROJECT_ID=vp-12345 \
#     ./scripts/ops/nvidia_publish_e2e_smoke.sh
#
#   # Against staging:
#   BASE_URL=https://staging.instaedit.org VELOX_PROJECT_ID=vp-12345 \
#     ./scripts/ops/nvidia_publish_e2e_smoke.sh
#
#   # Against production with privacy=unlisted (safe test):
#   BASE_URL=https://api.instaedit.org VELOX_PROJECT_ID=vp-12345 \
#     PRIVACY=unlisted ./scripts/ops/nvidia_publish_e2e_smoke.sh
#
# Environment variables:
#   VELOX_PROJECT_ID    (required) Velox project ID for the editor session
#   BASE_URL            API base URL (default: http://localhost:8080)
#   SESSION_COOKIE      Skip login, use this JWT as the session cookie
#   CSRF_TOKEN          CSRF token (auto-extracted if not provided)
#   PRIVACY             Privacy status for publish (default: unlisted)
#   TEST_EMAIL          Email for magic-link login (default: auto-generated)
#   SKIP_S3_UPLOAD      Set to 1 to skip the actual S3 PUT (dry-run mode)
#
# Exit codes:
#   0  all assertions passed
#   1  one or more assertions FAILed
#   2  missing required tools (curl, jq)
#   3  missing required env vars

set -euo pipefail

# ─── Config ────────────────────────────────────────────────────────────
BASE_URL="${BASE_URL:-http://localhost:8080}"
VELOX_PROJECT_ID="${VELOX_PROJECT_ID:-}"
SESSION_COOKIE="${SESSION_COOKIE:-}"
CSRF_TOKEN="${CSRF_TOKEN:-}"
PRIVACY="${PRIVACY:-unlisted}"
SKIP_S3_UPLOAD="${SKIP_S3_UPLOAD:-0}"

# ─── Pre-flight: tools ─────────────────────────────────────────────────
for tool in curl jq; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "❌ missing required tool: $tool" >&2
    exit 2
  }
done

# ─── Pre-flight: VELOX_PROJECT_ID ──────────────────────────────────────
if [ -z "$VELOX_PROJECT_ID" ]; then
  echo "❌ VELOX_PROJECT_ID is required. Set it to the Velox project ID." >&2
  echo "   Example: VELOX_PROJECT_ID=vp-12345 $0" >&2
  exit 3
fi

# ─── Tmpdir ────────────────────────────────────────────────────────────
TMP_DIR=$(mktemp -d -t nvidia-publish-e2e-XXXXXX)
chmod 700 "$TMP_DIR"
COOKIE_JAR="$TMP_DIR/cookies.txt"
trap 'rm -rf "$TMP_DIR"' EXIT

# State counters + colour
PASS=0; FAIL=0; WARN=0
if [ -t 1 ]; then
  G=$'\033[32m'; R=$'\033[31m'; Y=$'\033[33m'; B=$'\033[34m'; N=$'\033[0m'
else
  G=""; R=""; Y=""; B=""; N=""
fi
pass() { PASS=$((PASS+1)); printf '  %s✓ PASS%s %s\n' "$G" "$N" "$1"; }
fail() { FAIL=$((FAIL+1)); printf '  %s✗ FAIL%s %s\n' "$R" "$N" "$1"; }
warn() { WARN=$((WARN+1)); printf '  %s! WARN%s %s\n' "$Y" "$N" "$1"; }
step() { printf '\n%s══ %s ══%s\n' "$B" "$1" "$N"; }

# ─── Generate thumbnail bytes (minimal valid JPEG) ─────────────────────
THUMBNAIL_FILE="$TMP_DIR/thumbnail.jpg"
# Smallest valid JPEG (1×1 pixel, ~631 bytes)
printf '\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00\xff\xdb\x00C\x00\x08\x06\x06\x07\x06\x05\x08\x07\x07\x07\t\t\x08\n\x0c\x14\r\x0c\x0b\x0b\x0c\x19\x12\x13\x0f\x14\x1d\x1a\x1f\x1e\x1d\x1a\x1c\x1c $.\x27 "\x1c\x1c(7),01444\x1f\x27\x27\x1c#1=82<.342\xff\xdb\x00C\x01\t\t\t\x0c\x0b\x0c\x18\r\r\x182!\x1c!22222222222222222222222222222222222222222222222222\xff\xc0\x00\x11\x08\x00\x01\x00\x01\x03\x01\x22\x00\x02\x11\x01\x03\x11\x01\xff\xc4\x00\x1f\x00\x00\x01\x05\x01\x01\x01\x01\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x01\x02\x03\x04\x05\x06\x07\x08\t\n\x0b\xff\xc4\x00\xb5\x10\x00\x02\x01\x03\x03\x02\x04\x03\x05\x05\x04\x04\x00\x00\x01}\x01\x02\x03\x00\x04\x11\x05\x12!1A\x06\x13Qa\x07"q\x142\x81\x91\xa1\x08#B\xb1\xc1\x15R\xd1\xf0$3br\x82\t\n\x16\x17\x18\x19\x1a%&\x27()*456789:CDEFGHIJSTUVWXYZcdefghijstuvwxyz\x83\x84\x85\x86\x87\x88\x89\x8a\x92\x93\x94\x95\x96\x97\x98\x99\x9a\xa2\xa3\xa4\xa5\xa6\xa7\xa8\xa9\xaa\xb2\xb3\xb4\xb5\xb6\xb7\xb8\xb9\xba\xc2\xc3\xc4\xc5\xc6\xc7\xc8\xc9\xca\xd2\xd3\xd4\xd5\xd6\xd7\xd8\xd9\xda\xe1\xe2\xe3\xe4\xe5\xe6\xe7\xe8\xe9\xea\xf1\xf2\xf3\xf4\xf5\xf6\xf7\xf8\xf9\xfa\xff\xc4\x00\x1f\x01\x00\x03\x01\x01\x01\x01\x01\x01\x01\x01\x01\x00\x00\x00\x00\x00\x00\x01\x02\x03\x04\x05\x06\x07\x08\t\n\x0b\xff\xc4\x00\xb5\x11\x00\x02\x01\x02\x04\x04\x03\x04\x07\x05\x04\x04\x00\x01\x02w\x00\x01\x02\x03\x11\x04\x05!1\x06\x12AQ\x07aq\x13"2\x81\x08\x14B\x91\xa1\xb1\xc1\t#3R\xf0\x15br\xd1\n\x16$4\xe1%\xf1\x17\x18\x19\x1a&\x27()*56789:CDEFGHIJSTUVWXYZcdefghijstuvwxyz\x82\x83\x84\x85\x86\x87\x88\x89\x8a\x92\x93\x94\x95\x96\x97\x98\x99\x9a\xa2\xa3\xa4\xa5\xa6\xa7\xa8\xa9\xaa\xb2\xb3\xb4\xb5\xb6\xb7\xb8\xb9\xba\xc2\xc3\xc4\xc5\xc6\xc7\xc8\xc9\xca\xd2\xd3\xd4\xd5\xd6\xd7\xd8\xd9\xda\xe2\xe3\xe4\xe5\xe6\xe7\xe8\xe9\xea\xf2\xf3\xf4\xf5\xf6\xf7\xf8\xf9\xfa\xff\xda\x00\x0c\x03\x01\x00\x02\x11\x03\x11\x00?\x00\xf7\xf0\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\xff\xd9' > "$THUMBNAIL_FILE"
THUMBNAIL_SIZE=$(wc -c < "$THUMBNAIL_FILE")

echo "NVIDIA Metadata Publish E2E Smoke Test"
echo "  BASE_URL=$BASE_URL"
echo "  VELOX_PROJECT_ID=$VELOX_PROJECT_ID"
echo "  PRIVACY=$PRIVACY"
echo "  Thumbnail: $THUMBNAIL_SIZE bytes"
echo ""

# ═══════════════════════════════════════════════════════════════════════
# STEP 1: Authentication
# ═══════════════════════════════════════════════════════════════════════
step "Step 1: Authentication"

if [ -n "$SESSION_COOKIE" ]; then
  echo "$SESSION_COOKIE" > "$COOKIE_JAR"
  pass "Using provided SESSION_COOKIE"
else
  TEST_EMAIL="${TEST_EMAIL:-smoke-nvidia-$(date -u +%Y%m%dT%H%M%SZ)@instaedit-test.org}"
  echo "  No SESSION_COOKIE provided — attempting magic-link login with: $TEST_EMAIL"

  HTTP=$(curl -sS -o "$TMP_DIR/start.json" -w '%{http_code}' \
    -X POST "$BASE_URL/api/v1/auth/magic-link/start" \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"$TEST_EMAIL\"}" 2>/dev/null || echo 000)

  if [ "$HTTP" != "200" ]; then
    fail "Magic-link start: HTTP $HTTP (expected 200)"
    echo "  → set SESSION_COOKIE= to skip login"
    exit 1
  fi

  TOKEN=$(jq -r '.magic_link_token // empty' "$TMP_DIR/start.json")
  if [ -z "$TOKEN" ]; then
    fail "No .magic_link_token in response — production email path, cannot auto-login"
    echo "  → obtain a session JWT from your browser's cookies and set SESSION_COOKIE="
    exit 1
  fi

  HTTP=$(curl -sS -o /dev/null -D "$TMP_DIR/verify.headers" -w '%{http_code}' -c "$COOKIE_JAR" \
    -X POST "$BASE_URL/api/v1/auth/magic-link/verify" \
    -H "Content-Type: application/json" \
    -d "{\"token\":\"$TOKEN\"}" 2>/dev/null || echo 000)

  if [ "$HTTP" = "204" ]; then
    pass "Magic-link verify: 204 + session cookie stored"
  else
    fail "Magic-link verify: HTTP $HTTP (expected 204)"
    exit 1
  fi
fi

# Extract CSRF token from cookies if not provided
if [ -z "$CSRF_TOKEN" ]; then
  CSRF_TOKEN=$(grep 'csrf_token' "$COOKIE_JAR" 2>/dev/null | awk '{print $NF}' || echo "")
  if [ -z "$CSRF_TOKEN" ]; then
    # Try extracting from a test request
    HTTP=$(curl -sS -o /dev/null -D "$TMP_DIR/csrf.headers" -w '%{http_code}' -b "$COOKIE_JAR" \
      "$BASE_URL/api/v1/auth/csrf-token" 2>/dev/null || echo 000)
    CSRF_TOKEN=$(grep -i 'csrf_token' "$TMP_DIR/csrf.headers" 2>/dev/null | head -1 | sed -n 's/.*csrf_token=\([^;]*\).*/\1/p' || echo "")
  fi
  [ -n "$CSRF_TOKEN" ] && pass "CSRF token extracted" || warn "Could not extract CSRF token — requests may fail"
fi

# ═══════════════════════════════════════════════════════════════════════
# STEP 2: Media Presign
# ═══════════════════════════════════════════════════════════════════════
step "Step 2: Media presign (POST /api/v1/media/presign)"

# Compute SHA-256 of the thumbnail
THUMBNAIL_SHA=$(sha256sum "$THUMBNAIL_FILE" | awk '{print $1}')
echo "  SHA-256: $THUMBNAIL_SHA"

PRESIGN_BODY=$(jq -n \
  --arg filename "nvidia-e2e-thumbnail-$(date -u +%Y%m%dT%H%M%SZ).jpg" \
  --arg content_type "image/jpeg" \
  --arg size "$THUMBNAIL_SIZE" \
  --arg sha "$THUMBNAIL_SHA" \
  '{filename: $filename, content_type: $content_type, size: ($size | tonumber), sha256: $sha}')

HTTP=$(curl -sS -o "$TMP_DIR/presign.json" -w '%{http_code}' -b "$COOKIE_JAR" \
  -X POST "$BASE_URL/api/v1/media/presign" \
  -H "Content-Type: application/json" \
  ${CSRF_TOKEN:+-H "X-CSRF-Token: $CSRF_TOKEN"} \
  -d "$PRESIGN_BODY" 2>/dev/null || echo 000)

if [ "$HTTP" != "200" ]; then
  fail "Media presign: HTTP $HTTP (expected 200)"
  cat "$TMP_DIR/presign.json" 2>/dev/null
  exit 1
fi

PRESIGN_URL=$(jq -r '.url // .upload_url // empty' "$TMP_DIR/presign.json")
ASSET_ID=$(jq -r '.asset_id // .id // empty' "$TMP_DIR/presign.json")
UPLOAD_KEY=$(jq -r '.key // empty' "$TMP_DIR/presign.json")

if [ -z "$PRESIGN_URL" ]; then
  fail "Media presign: response missing upload URL"
  cat "$TMP_DIR/presign.json"
  exit 1
fi
pass "Media presign: 200 + upload_url (asset_id=$ASSET_ID, key=$UPLOAD_KEY)"

# ═══════════════════════════════════════════════════════════════════════
# STEP 3: Upload thumbnail to presigned URL
# ═══════════════════════════════════════════════════════════════════════
step "Step 3: Upload thumbnail to presigned URL"

if [ "$SKIP_S3_UPLOAD" = "1" ]; then
  warn "SKIP_S3_UPLOAD=1 — skipping actual S3 PUT (dry-run mode)"
else
  HTTP=$(curl -sS -o "$TMP_DIR/upload_resp.txt" -w '%{http_code}' \
    -X PUT "$PRESIGN_URL" \
    -H "Content-Type: image/jpeg" \
    --data-binary "@$THUMBNAIL_FILE" 2>/dev/null || echo 000)

  if [ "$HTTP" = "200" ] || [ "$HTTP" = "204" ]; then
    pass "S3 upload: HTTP $HTTP (thumbnail uploaded successfully)"
  else
    fail "S3 upload: HTTP $HTTP (expected 200 or 204)"
    head -c 500 "$TMP_DIR/upload_resp.txt" 2>/dev/null
    # Continue anyway — /complete may still work if the presign was valid
  fi
fi

# ═══════════════════════════════════════════════════════════════════════
# STEP 4: Complete media
# ═══════════════════════════════════════════════════════════════════════
step "Step 4: Complete media (POST /api/v1/media/{id}/complete)"

if [ -z "$ASSET_ID" ]; then
  fail "No asset_id from presign — cannot complete"
  exit 1
fi

HTTP=$(curl -sS -o "$TMP_DIR/complete.json" -w '%{http_code}' -b "$COOKIE_JAR" \
  -X POST "$BASE_URL/api/v1/media/$ASSET_ID/complete" \
  -H "Content-Type: application/json" \
  ${CSRF_TOKEN:+-H "X-CSRF-Token: $CSRF_TOKEN"} \
  -d "{\"sha256\":\"$THUMBNAIL_SHA\"}" 2>/dev/null || echo 000)

if [ "$HTTP" = "200" ]; then
  COMPLETE_STATUS=$(jq -r '.status // "ready"' "$TMP_DIR/complete.json" 2>/dev/null)
  pass "Media complete: HTTP 200 + status=$COMPLETE_STATUS"
else
  fail "Media complete: HTTP $HTTP (expected 200)"
  cat "$TMP_DIR/complete.json" 2>/dev/null
  exit 1
fi

# ═══════════════════════════════════════════════════════════════════════
# STEP 5: Attach thumbnail to editor session
# ═══════════════════════════════════════════════════════════════════════
step "Step 5: Attach thumbnail (PATCH /api/v1/youtube/editor-sessions/by-project/{id})"

ATTACH_BODY=$(jq -n --arg mid "$ASSET_ID" '{thumbnail_media_id: $mid}')

HTTP=$(curl -sS -o "$TMP_DIR/attach.json" -w '%{http_code}' -b "$COOKIE_JAR" \
  -X PATCH "$BASE_URL/api/v1/youtube/editor-sessions/by-project/$VELOX_PROJECT_ID" \
  -H "Content-Type: application/json" \
  ${CSRF_TOKEN:+-H "X-CSRF-Token: $CSRF_TOKEN"} \
  -d "$ATTACH_BODY" 2>/dev/null || echo 000)

if [ "$HTTP" = "200" ]; then
  SESSION_STATUS=$(jq -r '.status // empty' "$TMP_DIR/attach.json" 2>/dev/null)
  ATTACHED_MEDIA=$(jq -r '.thumbnail_media_id // empty' "$TMP_DIR/attach.json" 2>/dev/null)
  pass "Attach thumbnail: HTTP 200 + status=$SESSION_STATUS + media_id=$ATTACHED_MEDIA"
else
  fail "Attach thumbnail: HTTP $HTTP (expected 200)"
  cat "$TMP_DIR/attach.json" 2>/dev/null
  exit 1
fi

# ═══════════════════════════════════════════════════════════════════════
# STEP 6: Publish with NVIDIA metadata
# ═══════════════════════════════════════════════════════════════════════
step "Step 6: Publish with NVIDIA metadata"

PUBLISH_BODY=$(cat <<'PUBEOF'
{
  "title": "Come automatizzare la pubblicazione YouTube nel 2026",
  "description": "In questo video vediamo come creare, modificare e pubblicare contenuti YouTube attraverso un flusso automatizzato con InstaEdit e NVIDIA AI.",
  "privacy_status": "PRIVACY_PLACEHOLDER",
  "tags": [
    "youtube automation",
    "video editing",
    "instaedit",
    "content creation"
  ],
  "default_language": "it",
  "default_audio_language": "it",
  "translations": {
    "en": {
      "title": "How to Automate YouTube Publishing in 2026",
      "description": "This video explains how to create, edit and publish YouTube content through an automated workflow with InstaEdit and NVIDIA AI."
    },
    "es": {
      "title": "Cómo automatizar la publicación en YouTube en 2026",
      "description": "Este video explica cómo crear, editar y publicar contenido de YouTube mediante un flujo automatizado con InstaEdit y NVIDIA AI."
    },
    "pt-BR": {
      "title": "Como automatizar publicações no YouTube em 2026",
      "description": "Este vídeo mostra como criar, editar e publicar conteúdo no YouTube por meio de um fluxo automatizado com InstaEdit e NVIDIA AI."
    }
  }
}
PUBEOF
)
PUBLISH_BODY="${PUBLISH_BODY/PRIVACY_PLACEHOLDER/$PRIVACY}"

HTTP=$(curl -sS -o "$TMP_DIR/publish.json" -w '%{http_code}' -b "$COOKIE_JAR" \
  -X POST "$BASE_URL/api/v1/youtube/editor-sessions/by-project/$VELOX_PROJECT_ID/publish" \
  -H "Content-Type: application/json" \
  ${CSRF_TOKEN:+-H "X-CSRF-Token: $CSRF_TOKEN"} \
  -d "$PUBLISH_BODY" 2>/dev/null || echo 000)

if [ "$HTTP" != "200" ]; then
  fail "Publish: HTTP $HTTP (expected 200)"
  echo "  Response body:"
  cat "$TMP_DIR/publish.json" 2>/dev/null | jq . 2>/dev/null || cat "$TMP_DIR/publish.json"
  exit 1
fi

# ─── Response assertions ─────────────────────────────────────────────
echo ""
echo "  Publish response:"
cat "$TMP_DIR/publish.json" | jq . 2>/dev/null || cat "$TMP_DIR/publish.json"

PUB_STATUS=$(jq -r '.status // empty' "$TMP_DIR/publish.json")
PUB_URL=$(jq -r '.public_url // empty' "$TMP_DIR/publish.json")
PUB_VIDEO_ID=$(jq -r '.video_id // empty' "$TMP_DIR/publish.json")
PUB_PRIVACY=$(jq -r '.privacy_status // empty' "$TMP_DIR/publish.json")
PUB_ACTUAL_PRIVACY=$(jq -r '.actual_privacy // empty' "$TMP_DIR/publish.json")
PUB_SYNC=$(jq -r '.youtube_sync_status // empty' "$TMP_DIR/publish.json")

if [ "$PUB_STATUS" = "published" ]; then
  pass "response.status = 'published'"
else
  fail "response.status: expected 'published', got '$PUB_STATUS'"
fi

if [ -n "$PUB_URL" ] && echo "$PUB_URL" | grep -q "youtube.com/watch"; then
  pass "response.public_url: $PUB_URL"
else
  fail "response.public_url: missing or invalid YouTube URL: '$PUB_URL'"
fi

if [ -n "$PUB_VIDEO_ID" ]; then
  pass "response.video_id: $PUB_VIDEO_ID"
else
  fail "response.video_id: empty"
fi

if [ "$PUB_PRIVACY" = "$PRIVACY" ]; then
  pass "response.privacy_status = '$PRIVACY' (matches requested)"
elif [ "$PUB_ACTUAL_PRIVACY" = "$PRIVACY" ]; then
  pass "response.actual_privacy = '$PRIVACY' (matches requested)"
else
  warn "response.privacy_status='$PUB_PRIVACY' actual='$PUB_ACTUAL_PRIVACY' requested='$PRIVACY' — may indicate drift"
fi

case "$PUB_SYNC" in
  confirmed) pass "response.youtube_sync_status = 'confirmed'" ;;
  pending)  warn "response.youtube_sync_status = 'pending' — YouTube read-back not yet confirmed" ;;
  drift)    warn "response.youtube_sync_status = 'drift' — privacy mismatch detected!" ;;
  *)        fail "response.youtube_sync_status: unexpected value '$PUB_SYNC'" ;;
esac

# ═══════════════════════════════════════════════════════════════════════
# STEP 7: Verify on YouTube Studio (manual check)
# ═══════════════════════════════════════════════════════════════════════
step "Step 7: Manual YouTube Studio verification"

if [ -n "$PUB_URL" ]; then
  echo ""
  echo "  ${B}► Open this URL in your browser to verify on YouTube Studio:${N}"
  echo "    https://studio.youtube.com/video/$PUB_VIDEO_ID/edit"
  echo ""
  echo "  Manual checks (open the video in YouTube Studio):"
  echo "    □ Thumbnail is the custom one uploaded in Step 3"
  echo "    □ Title (Italian): 'Come automatizzare la pubblicazione YouTube nel 2026'"
  echo "    □ Description (Italian): starts with 'In questo video vediamo come creare...'"
  echo "    □ Tags: youtube automation, video editing, instaedit, content creation"
  echo "    □ Video language: Italian (it)"
  echo "    □ Audio language: Italian (it)"
  echo "    □ Localization EN: 'How to Automate YouTube Publishing in 2026'"
  echo "    □ Localization ES: 'Cómo automatizar la publicación en YouTube en 2026'"
  echo "    □ Localization PT-BR: 'Como automatizar publicações no YouTube em 2026'"
  echo "    □ Privacy: $PRIVACY"
  echo "    □ No truncated fields (title ≤ 100 chars, description ≤ 5000)"
  echo ""
fi

# ═══════════════════════════════════════════════════════════════════════
# VERDICT
# ═══════════════════════════════════════════════════════════════════════
echo ""
echo "════════════════════════════════════════════════════════════════"
printf "  Verdict: %d passed, %d failed, %d warnings\n" "$PASS" "$FAIL" "$WARN"
echo "════════════════════════════════════════════════════════════════"

if [ "$FAIL" -gt 0 ]; then
  echo ""
  echo "  Troubleshooting:"
  echo "  - Check that VELOX_PROJECT_ID=$VELOX_PROJECT_ID exists and has a YouTube video in 'editing' state"
  echo "  - Ensure the YouTube channel has OAuth with youtube.upload scope"
  echo "  - Verify the Velox project's platform_account is active"
  echo "  - Check server logs: docker compose logs api worker"
  exit 1
fi

exit 0
