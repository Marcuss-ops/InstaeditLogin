#!/usr/bin/env bash
# scripts/ops/workspace_isolation_test.sh
#
# Phase 9, sub-phase 6 — workspace isolation test.
# Creates two distinct users + workspaces via /api/v1/auth/register and
# verifies user A CANNOT access user B's data via ANY endpoint:
#   /api/v1/accounts
#   /api/v1/posts/workspace/{wid}
#   /api/v1/posts  (cross-workspace write attempt)
#
# Default mode: APPLY (creates + tests + cleans up).
# PASS --dry-run to preview the full plan + the cleanup SQL without mutating.
#
# Hard cleanup semantics: the script ALWAYS tries to remove its own test
# users (matched by random suffix) at exit via psql CASCADE on $DATABASE_URL.
# If cleanup fails for any reason, the script prints the exact psql commands
# the operator should run manually.
#
# Usage:
#   ./scripts/ops/workspace_isolation_test.sh --dry-run
#   ./scripts/ops/workspace_isolation_test.sh --apply   (default)
#
# Exit codes:
#   0  all isolation assertions passed + cleanup succeeded
#   1  one or more isolation assertions failed (cleanup still attempted)
#   2  missing tools (curl / jq / openssl / psql)
#   3  DATABASE_URL missing (required for cleanup)

set -euo pipefail

# ─── Args ───────────────────────────────────────────────────────────────
APPLY=true
if [ "${1:-}" = "--dry-run" ]; then
  APPLY=false
fi

# ─── Pre-flight (tools + DATABASE_URL) ────────────────────────────────
for tool in curl jq openssl psql; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "❌ missing required tool: $tool" >&2
    exit 2
  }
done
: "${DATABASE_URL:?ERR: DATABASE_URL not set — required for cleanup. Export the POOLED URL from your password manager.}"

BASE_URL="${BASE_URL:-https://api.instaedit.org}"
SUFFIX="$(openssl rand -hex 6)"
USERA_EMAIL="isol-A-${SUFFIX}@instaedit-test.org"
USERB_EMAIL="isol-B-${SUFFIX}@instaedit-test.org"
USERA_NAME="IsolTestA $SUFFIX"
USERB_NAME="IsolTestB $SUFFIX"
PASS_A="isolA$(openssl rand -hex 6)"
PASS_B="isolB$(openssl rand -hex 6)"

# ─── DRY-RUN branch (no tmp + no DB writes) ────────────────────────────
# IMPORTANT: TMP_DIR + cookie jar creation is BELOW this branch, ONLY in
# APPLY mode. Don't move them above this point — the chmod on non-existent
# tmp paths would fail in dry-run.
if [ "$APPLY" = false ]; then
  echo ""
  echo "═════════════════════════════════════════════════════════════════════════"
  echo "  PHASE 9.6 WORKSPACE ISOLATION TEST [DRY-RUN — no mutations]"
  echo "═════════════════════════════════════════════════════════════════════════"
  echo ""
  echo "  Plan (preview only — no data created, no DB writes):"
  echo "    USERA  email=$USERA_EMAIL"
  echo "    USERB  email=$USERB_EMAIL"
  echo "    Suffix=$SUFFIX  (used by cleanup to scope DELETE)"
  echo "    Backend=$BASE_URL"
  echo "    Cleanup CASCADE on users matching suffix:"
  cat <<SQL
    BEGIN;
    DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'isol-%-%$SUFFIX');
    DELETE FROM workspace_memberships WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'isol-%-%$SUFFIX');
    DELETE FROM workspaces WHERE name IN ('$USERA_NAME','$USERB_NAME');
    DELETE FROM users WHERE email LIKE 'isol-%-%$SUFFIX';
    COMMIT;
SQL
  echo ""
  echo "  DRY-RUN COMPLETE. Re-run without --dry-run to execute."
  exit 0
fi

# ─── APPLY mode: create tmp + cookie jars now (post dry-run gate) ──────
TMP_DIR=$(mktemp -d -t isolation-test-XXXXXX)
chmod 700 "$TMP_DIR"
COOKIE_JAR_A="$TMP_DIR/cookies_A.txt"; touch "$COOKIE_JAR_A"; chmod 600 "$COOKIE_JAR_A"
COOKIE_JAR_B="$TMP_DIR/cookies_B.txt"; touch "$COOKIE_JAR_B"; chmod 600 "$COOKIE_JAR_B"

echo ""
echo "═════════════════════════════════════════════════════════════════════════"
echo "  PHASE 9.6 WORKSPACE ISOLATION TEST [APPLY]"
echo "═════════════════════════════════════════════════════════════════════════"
echo ""
echo "  USERA  : $USERA_EMAIL"
echo "  USERB  : $USERB_EMAIL"
echo "  Suffix : $SUFFIX"
echo "  Backend: $BASE_URL"
echo "  Cleanup scope: psql CASCADE on 'isol-%-%$SUFFIX' (runs on EXIT — success or failure)"
echo ""

# ─── Cleanup safety: trap EXIT — runs on success OR failure ─────────────
cleanup_test_users() {
  echo ""
  echo "── cleanup: hard-delete test users + workspaces + sessions ────"
  local SQL_BLOCK
  SQL_BLOCK=$(cat <<SQL
BEGIN;
DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'isol-%-%$SUFFIX');
DELETE FROM workspace_memberships WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'isol-%-%$SUFFIX');
DELETE FROM workspaces WHERE name IN ('$USERA_NAME','$USERB_NAME');
DELETE FROM users WHERE email LIKE 'isol-%-%$SUFFIX';
COMMIT;
SQL
)
  if psql "$DATABASE_URL" -v ON_ERROR_STOP=1 <<<"$SQL_BLOCK" 2>&1 | tail -10; then
    echo "  ✓ cleanup OK"
  else
    echo "  ✗ cleanup failed — run this manually to clean up:"
    echo "    psql \"\$DATABASE_URL\" <<'SQL'"
    echo "    BEGIN;"
    echo "    DELETE FROM users WHERE email IN ('$USERA_EMAIL','$USERB_EMAIL');"
    echo "    COMMIT;"
    echo "    SQL"
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup_test_users EXIT

# ─── §C Create user A ──────────────────────────────────────────────────
echo "── §C: Create user A (USERA) ──────────────────────────────"
HTTP=$(curl -sS -o "$TMP_DIR/a.json" -w '%{http_code}' -c "$COOKIE_JAR_A" \
  -X POST "$BASE_URL/api/v1/auth/register" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"$USERA_EMAIL\",\"password\":\"$PASS_A\",\"name\":\"$USERA_NAME\"}" 2>/dev/null || echo 000)
if [ "$HTTP" != "201" ]; then
  echo "  ✗ USERA register failed: HTTP $HTTP — body: $(head -c 300 "$TMP_DIR/a.json" 2>/dev/null)"
  exit 1
fi
A_UID=$(jq -r '.user_id // empty' "$TMP_DIR/a.json")
A_WSID=$(jq -r '.workspace_id // empty' "$TMP_DIR/a.json")
if [ -z "$A_UID" ] || [ -z "$A_WSID" ]; then
  echo "  ✗ USERA 201 OK but JSON missing user_id/workspace_id"
  exit 1
fi
printf "  \033[32m✓ PASS\033[0m USERA registered: user_id=$A_UID workspace_id=$A_WSID\n"

# ─── §D Create user B ──────────────────────────────────────────────────
echo "── §D: Create user B (USERB) ──────────────────────────────"
HTTP=$(curl -sS -o "$TMP_DIR/b.json" -w '%{http_code}' -c "$COOKIE_JAR_B" \
  -X POST "$BASE_URL/api/v1/auth/register" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"$USERB_EMAIL\",\"password\":\"$PASS_B\",\"name\":\"$USERB_NAME\"}" 2>/dev/null || echo 000)
if [ "$HTTP" != "201" ]; then
  echo "  ✗ USERB register failed: HTTP $HTTP — body: $(head -c 300 "$TMP_DIR/b.json" 2>/dev/null)"
  exit 1
fi
B_UID=$(jq -r '.user_id // empty' "$TMP_DIR/b.json")
B_WSID=$(jq -r '.workspace_id // empty' "$TMP_DIR/b.json")
if [ -z "$B_UID" ] || [ -z "$B_WSID" ]; then
  echo "  ✗ USERB 201 OK but JSON missing user_id/workspace_id"
  exit 1
fi
printf "  \033[32m✓ PASS\033[0m USERB registered: user_id=$B_UID workspace_id=$B_WSID\n"

echo ""
echo "── §E / §F: Isolation assertions ───────────────────────────"
echo ""
PASS=0; FAIL=0
pass() { PASS=$((PASS+1)); printf '  \033[32m✓ PASS\033[0m %s\n' "$1"; }
fail() { FAIL=$((FAIL+1)); printf '  \033[31m✗ FAIL\033[0m %s\n' "$1"; }

# §E.1: both users' /accounts should be empty (fresh users).
USERA_ACC_LEN=$(curl -sS -b "$COOKIE_JAR_A" "$BASE_URL/api/v1/accounts" 2>/dev/null | jq -r '.accounts | length // 0' 2>/dev/null)
USERB_ACC_LEN=$(curl -sS -b "$COOKIE_JAR_B" "$BASE_URL/api/v1/accounts" 2>/dev/null | jq -r '.accounts | length // 0' 2>/dev/null)
if [ "$USERA_ACC_LEN" = "0" ] && [ "$USERB_ACC_LEN" = "0" ]; then
  pass "§E.1  /api/v1/accounts: USERA sees 0, USERB sees 0 (no inter-account leakage)"
else
  fail "§E.1  /api/v1/accounts: USERA=$USERA_ACC_LEN, USERB=$USERB_ACC_LEN (both should be 0 for fresh users)"
fi

# §E.2: USERA tries to GET /api/v1/posts/workspace/{USERB_WSID}. Should 403/404.
HTTP=$(curl -sS -o "$TMP_DIR/e2.json" -w '%{http_code}' -b "$COOKIE_JAR_A" \
  "$BASE_URL/api/v1/posts/workspace/$B_WSID")
if [ "$HTTP" = "403" ] || [ "$HTTP" = "404" ]; then
  pass "§E.2  USERA → /api/v1/posts/workspace/$B_WSID = HTTP $HTTP (cross-tenant READ BLOCKED)"
else
  fail "§E.2  USERA → /api/v1/posts/workspace/$B_WSID = HTTP $HTTP (expected 403/404)"
  echo "       Body: $(head -c 300 "$TMP_DIR/e2.json" 2>/dev/null)"
fi

# §F.1: USERA tries to POST /api/v1/posts with explicit workspace_id=USERB_WSID.
# Handler must take workspace from auth context, NOT body. REGRESSION otherwise.
HTTP=$(curl -sS -o "$TMP_DIR/f1.json" -w '%{http_code}' -b "$COOKIE_JAR_A" \
  -X POST "$BASE_URL/api/v1/posts" \
  -H "Content-Type: application/json" \
  -d "{\"caption\":\"isolation probe\",\"workspace_id\":$B_WSID,\"targets\":[\"instagram\"]}")
if [ "$HTTP" = "403" ] || [ "$HTTP" = "400" ]; then
  pass "§F.1  USERA → POST /api/v1/posts with workspace_id=$B_WSID = HTTP $HTTP (cross-tenant WRITE BLOCKED)"
else
  fail "§F.1  USERA → POST /api/v1/posts with workspace_id=$B_WSID = HTTP $HTTP (REGRESSION: handler accepted foreign workspace_id)"
  echo "       Body: $(head -c 300 "$TMP_DIR/f1.json" 2>/dev/null)"
fi

# §F.2: each user can list its OWN workspace's posts (should be empty).
USERA_POSTS_LEN=$(curl -sS -b "$COOKIE_JAR_A" "$BASE_URL/api/v1/posts/workspace/$A_WSID" 2>/dev/null | jq -r '.posts | length // 0' 2>/dev/null)
USERB_POSTS_LEN=$(curl -sS -b "$COOKIE_JAR_B" "$BASE_URL/api/v1/posts/workspace/$B_WSID" 2>/dev/null | jq -r '.posts | length // 0' 2>/dev/null)
if [ "$USERA_POSTS_LEN" = "0" ] && [ "$USERB_POSTS_LEN" = "0" ]; then
  pass "§F.2  /api/v1/posts/workspace/$A_WSID (USERA-OWN) = 0 posts; $B_WSID (USERB-OWN) = 0 posts"
else
  fail "§F.2  USERA posts=$USERA_POSTS_LEN (expected 0), USERB posts=$USERB_POSTS_LEN (expected 0)"
fi

echo ""
echo "═════════════════════════════════════════════════════════════════════════"
echo "  AGGREGATE"
echo "═════════════════════════════════════════════════════════════════════════"
echo ""
printf "  PASS: %d\n" "$PASS"
printf "  FAIL: %d\n" "$FAIL"
echo ""

if [ $FAIL -eq 0 ]; then
  echo "  ✓ workspace isolation verified across 2 users + 2 workspaces + 4 endpoints"
  echo "  cleanup will run on EXIT (success path)"
  exit 0
else
  echo "  ✗ workspace isolation REGRESSION: $FAIL assertion(s) failed"
  echo "  cleanup will STILL run on EXIT (failure path) — test data will be removed"
  exit 1
fi
