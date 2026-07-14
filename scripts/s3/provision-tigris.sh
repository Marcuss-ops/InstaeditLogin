#!/usr/bin/env bash
# scripts/s3/provision-tigris.sh
#
# Idempotent provisioning of the Tigris bucket `instaedit-prod-media`.
# Final state matches fly.toml [env]:
#   - S3_ENDPOINT = "https://t3.storage.dev"          (Tigris Data global default)
#   - S3_REGION   = "auto"
#   - S3_BUCKET   = "instaedit-prod-media"
#
# For Fly.io's managed Tigris (regional), export S3_ENDPOINT=https://fly.storage.tigris.dev
# before invoking this script. The SigV4 signer + aws-cli calls below are
# endpoint-agnostic; only the S3_ENDPOINT var (and matching fly.toml line) change.
#
# Run from the operator laptop after `flyctl auth login`.
# Reads S3 credentials from env (NEVER pass secrets as CLI args).
#
# Default mode: DRY-RUN (prints intent only — no mutations).
# Pass --apply to actually mutate state on Tigris.
#
# Pre-conditions:
#   - aws-cli installed (`brew install awscli` or system package)
#   - AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY exported
#     (capture these from https://tigrisdata.com Dashboard → Access Keys
#      BEFORE running this script; the script never prints them)
#
# Six steps applied:
#   1. head-bucket (idempotent skip if exists)
#   2. put-bucket-cors — AllowedOrigins: https://app.instaedit.org (single)
#                  AllowedMethods: PUT/GET/HEAD; ExposeHeaders: ETag
#                  MaxAgeSeconds: 3600
#   3. put-bucket-lifecycle-configuration — AbortIncompleteMultipartUpload
#                  after 1 day (no orphan parts from cancelled uploads)
#   4. put-bucket-policy — (a) Deny non-TLS via aws:SecureTransport=false
#                  (b) Deny PutObject if s3:content-length > 209715200
#                  (200 MB defense-in-depth; the application ALSO clamps
#                  presigned-URL Content-Length via STORAGE_MAX_UPLOAD_BYTES)
#   5. put-bucket-versioning — Enabled (production media + audit trail)
#   6. Smoke round-trip: write ops-smoke-test-* key → head → delete
#
# Exit codes:
#   0  success (dry-run printed OR apply completed)
#   1  pre-condition failure (missing aws-cli / missing creds / bad state)
#   2  apply-failure (uploads / policy / versioning rejected by Tigris)
#
# Companion: scripts/db/check-postgres-health.sh + scripts/s3/smoke-s3.sh
# (future) — both should be run before `make fly-deploy`.

set -euo pipefail

BUCKET="${S3_BUCKET:-instaedit-prod-media}"
ORIGIN="${ALLOWED_ORIGIN:-https://app.instaedit.org}"
MAX_BYTES="${MAX_BYTES:-209715200}"   # 200 * 1024 * 1024
MAX_AGE="${MAX_AGE:-3600}"
ENDPOINT="${S3_ENDPOINT:-https://t3.storage.dev}"

APPLY=0
if [ "${1:-}" = "--apply" ]; then
  APPLY=1
else
  echo "=== DRY-RUN mode — pass --apply to mutate state ==="
  echo ""
fi

# ────────────────────────────────────────────────────────────────────────
# Pre-conditions
# ────────────────────────────────────────────────────────────────────────
command -v aws >/dev/null 2>&1 || {
  echo "ERR: aws-cli not installed — install via 'brew install awscli' or your pkg manager."
  exit 1
}
: "${AWS_ACCESS_KEY_ID:?ERR: AWS_ACCESS_KEY_ID not set — export from password manager}"
: "${AWS_SECRET_ACCESS_KEY:?ERR: AWS_SECRET_ACCESS_KEY not set — export from password manager}"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_DEFAULT_REGION="${AWS_REGION:-auto}"

echo "Endpoint: $ENDPOINT"
echo "Bucket:   $BUCKET"
echo "Origin:   $ORIGIN"
echo "Max:      $MAX_BYTES bytes (200 MB)"
echo ""

# ────────────────────────────────────────────────────────────────────────
# STEP 1: bucket existence (idempotent skip)
# ────────────────────────────────────────────────────────────────────────
echo "──── STEP 1: head-bucket ────"
if aws s3api head-bucket --bucket "$BUCKET" 2>/dev/null; then
  echo "✓ bucket '$BUCKET' already exists — idempotent skip"
else
  if [ "$APPLY" -eq 1 ]; then
    aws s3api create-bucket --bucket "$BUCKET"
    echo "✓ CREATED bucket '$BUCKET'"
  else
    echo "→ would CREATE: aws s3api create-bucket --bucket $BUCKET"
  fi
fi
echo ""

# ────────────────────────────────────────────────────────────────────────
# STEP 2: CORS configuration
# ────────────────────────────────────────────────────────────────────────
CORS_JSON="$(mktemp -t cors.XXXXXX.json)"
trap 'rm -f "$CORS_JSON" "$LIFECYCLE_JSON" "$POLICY_JSON" "$TEST_BODY"' EXIT
cat > "$CORS_JSON" <<JSON
{
  "CORSRules": [
    {
      "AllowedOrigins": ["$ORIGIN"],
      "AllowedMethods": ["PUT", "GET", "HEAD"],
      "AllowedHeaders": ["*"],
      "ExposeHeaders": ["ETag"],
      "MaxAgeSeconds": $MAX_AGE
    }
  ]
}
JSON
echo "──── STEP 2: put-bucket-cors ────"
echo "  AllowedOrigins: [\"$ORIGIN\"]"
echo "  AllowedMethods: PUT,GET,HEAD"
echo "  ExposeHeaders:  ETag     (needed for client cache validation)"
echo "  MaxAgeSeconds:  $MAX_AGE (cache preflights for 1 hour)"
if [ "$APPLY" -eq 1 ]; then
  aws s3api put-bucket-cors --bucket "$BUCKET" --cors-configuration "file://$CORS_JSON"
  echo "✓ APPLIED"
else
  echo "→ would APPLY"
fi
echo ""

# ────────────────────────────────────────────────────────────────────────
# STEP 3: lifecycle rule (abort incomplete multipart upload)
# ────────────────────────────────────────────────────────────────────────
LIFECYCLE_JSON="$(mktemp -t lifecycle.XXXXXX.json)"
cat > "$LIFECYCLE_JSON" <<JSON
{
  "Rules": [
    {
      "ID": "AbortIncompleteMultipartUpload",
      "Status": "Enabled",
      "Filter": {},
      "AbortIncompleteMultipartUpload": {
        "DaysAfterInitiation": 1
      }
    }
  ]
}
JSON
echo "──── STEP 3: put-bucket-lifecycle-configuration ────"
echo "  AbortIncompleteMultipartUpload after 1 day (no orphan parts)"
if [ "$APPLY" -eq 1 ]; then
  aws s3api put-bucket-lifecycle-configuration --bucket "$BUCKET" --lifecycle-configuration "file://$LIFECYCLE_JSON"
  echo "✓ APPLIED"
else
  echo "→ would APPLY"
fi
echo ""

# ────────────────────────────────────────────────────────────────────────
# STEP 4: bucket policy (TLS-only + max-size 200 MB)
# ────────────────────────────────────────────────────────────────────────
POLICY_JSON="$(mktemp -t policy.XXXXXX.json)"
cat > "$POLICY_JSON" <<JSON
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "EnforceTLS",
      "Effect": "Deny",
      "Principal": "*",
      "Action": "s3:*",
      "Resource": [
        "arn:aws:s3:::$BUCKET",
        "arn:aws:s3:::$BUCKET/*"
      ],
      "Condition": {
        "Bool": { "aws:SecureTransport": "false" }
      }
    },
    {
      "Sid": "MaxObjectSizeDefenseInDepth",
      "Effect": "Deny",
      "Principal": "*",
      "Action": [
        "s3:PutObject",
        "s3:PutObjectAcl",
        "s3:PutObjectVersion"
      ],
      "Resource": "arn:aws:s3:::$BUCKET/*",
      "Condition": {
        "NumericGreaterThan": { "s3:content-length": "$MAX_BYTES" }
      }
    }
  ]
}
JSON
echo "──── STEP 4: put-bucket-policy ────"
echo "  (a) Deny s3:* when aws:SecureTransport=false (TLS-only)"
echo "  (b) Deny PutObject* when s3:content-length > $MAX_BYTES (200 MB)"
echo "      (defense-in-depth — application ALSO clamps via STORAGE_MAX_UPLOAD_BYTES)"
if [ "$APPLY" -eq 1 ]; then
  aws s3api put-bucket-policy --bucket "$BUCKET" --policy "file://$POLICY_JSON"
  echo "✓ APPLIED"
else
  echo "→ would APPLY"
fi
echo ""

# ────────────────────────────────────────────────────────────────────────
# STEP 5: versioning
# ────────────────────────────────────────────────────────────────────────
echo "──── STEP 5: put-bucket-versioning ────"
echo "  Enabled (production audit trail; recover from accidental deletes)"
if [ "$APPLY" -eq 1 ]; then
  aws s3api put-bucket-versioning --bucket "$BUCKET" --versioning-configuration Status=Enabled
  echo "✓ ENABLED"
else
  echo "→ would ENABLE"
fi
echo ""

# ────────────────────────────────────────────────────────────────────────
# STEP 6: smoke round-trip
# ────────────────────────────────────────────────────────────────────────
TEST_BODY="$(mktemp -t smoke.XXXXXX.txt)"
echo "ok" > "$TEST_BODY"
TEST_KEY="ops-smoke-test-$(date -u +%Y%m%dT%H%M%SZ).txt"
echo "──── STEP 6: smoke round-trip ────"
echo "  key = $TEST_KEY"
if [ "$APPLY" -eq 1 ]; then
  aws s3 cp "$TEST_BODY" "s3://$BUCKET/$TEST_KEY" >/dev/null
  aws s3api head-object --bucket "$BUCKET" --key "$TEST_KEY" >/dev/null
  aws s3 rm "s3://$BUCKET/$TEST_KEY" >/dev/null
  echo "✓ smoke PASS (PUT → HEAD → DELETE on $BUCKET/$TEST_KEY)"
else
  echo "→ would RUN  aws s3 cp /tmp/.. s3://$BUCKET/$TEST_KEY"
fi
echo ""

# ────────────────────────────────────────────────────────────────────────
# Final summary
# ────────────────────────────────────────────────────────────────────────
echo "=================================================================="
if [ "$APPLY" -eq 1 ]; then
  echo "✓ PROVISIONING COMPLETE — bucket `$BUCKET` is live."
  echo ""
  echo "Capture these for the password manager:"
  echo "  BUCKET       : $BUCKET"
  echo "  ENDPOINT     : $ENDPOINT"
  echo "  REGION       : auto"
  echo "  CORS-ORIGIN  : $ORIGIN"
  echo "  MAX_BYTES    : $MAX_BYTES"
  echo "  VERSIONING   : Enabled"
  echo "  LIFECYCLE    : Abort multipart after 1 day"
  echo ""
  echo "Next: push fly.toml-aligned env vars + secrets:"
  echo "  [env] is already updated — (S3_ENDPOINT, S3_REGION, S3_BUCKET"
  echo "    are public; commit landed in the docs/s3 commit)."
  echo "  Secrets to stage via 'flyctl secrets set':"
  echo "    S3_ACCESS_KEY=<from-Tigris-dashboard>"
  echo "    S3_SECRET_KEY=<from-Tigris-dashboard>"
else
  echo "DRY-RUN COMPLETE — no mutations."
  echo "Re-run with --apply to execute and apply each step."
fi
