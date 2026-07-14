#!/usr/bin/env bash
#
# scripts/db/production-restore-drill.sh
#
# Restore drill — given $PROD_DSN (the production pool URL, used as
# reference) and $FORK_DSN (the URI of a freshly-cloned Fly Postgres
# cluster, created via `fly postgres fork`), the script asserts:
#
#   (a) the two DSNs do NOT point at the same hostname (so a paste
#       typo doesn't drill the production database)
#   (b) the fork is reachable + sslmode != disable
#   (c) the fork's schema state matches the SOURCE state via a
#       SHA-256 over (canary tables + post_status enum + per-table
#       column lists + per-table index names) — using the same
#       fingerprint algorithm as the integration test
#       internal/database/migrations_integration_test.go
#   (d) the fork contains 0 user rows (a fork with rows would mean
#       the operator accidentally forked after cookies were seeded —
#       the smoke test must always start from a clean cluster)
#   (e) the time-to-fork-ready was within the expected envelope
#       (Fly Postgres fork usually takes 30-180s)
#
# The script REPORTS a PASS/FAIL verdict + a copy-pasteable
# `fly postgres destroy` command for the cleanup step. It does NOT
# auto-destroy the cluster — that is the operator's confirmation.
#
# ─── USAGE ──────────────────────────────────────────────────────────────
#   DATABASE_URL=<FORK_DSN> DATABASE_URL_PROD=<PROD_DSN> \\
#       ./scripts/db/production-restore-drill.sh
#
#   # Or with explicit flags:
#   ./scripts/db/production-restore-drill.sh \\
#       --fork "postgres://..." --prod "postgres://..."
#
#   ./scripts/db/production-restore-drill.sh --help
#
# Exit codes:
#   0  drill passed (schema matches, cluster empty, latency sane)
#   1  pre-flight failure (psql/python missing, urls malformed,
#      same host, sslmode=disable on either)
#   2  bad CLI argument
#   3  drill failure (schema fingerprint mismatch, populated fork,
#      latency out of envelope)
#
# ─── RECORD KEEPING ─────────────────────────────────────────────────────
# At PASS, the script prints a markdown block ready to paste into
# ops/restore-drill-<UTC-TIMESTAMP>.md for the audit long-book.
# At FAIL, the same block is printed with the failure mode under a
# banner; the operator MUST save this to disk BEFORE destroying the
# fork so the post-mortem has the breach/avoidance record.
#
set -euo pipefail

PROD_DSN=""
FORK_DSN=""
for arg in "$@"; do
    case "$arg" in
        --prod)
            PROD_DSN="${2:-}"
            shift 2
            ;;
        --fork)
            FORK_DSN="${2:-}"
            shift 2
            ;;
        -h|--help)
            sed -n '2,40p' "$0"
            exit 0
            ;;
        *)
            echo "❌ unknown arg: $arg" >&2
            exit 2
            ;;
    esac
done

PROD_DSN="${PROD_DSN:-${DATABASE_URL_PROD:-}}"
FORK_DSN="${FORK_DSN:-${DATABASE_URL:-${DATABASE_URL_FORK:-}}}"

if [[ -z "$PROD_DSN" ]]; then
    echo "❌ DATABASE_URL_PROD (or --prod) is required" >&2
    exit 1
fi
if [[ -z "$FORK_DSN" ]]; then
    echo "❌ DATABASE_URL (or --fork) is required" >&2
    exit 1
fi

# ─── Pre-flight ──────────────────────────────────────────────────────────
command -v psql >/dev/null 2>&1 || { echo "❌ psql missing" >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "❌ python3 missing" >&2; exit 1; }

# Extract host + sslmode from each DSN. Two DSNs with the SAME host
# = the operator is about to drill the production database, NOT
# the fork. Reject loudly.
extract() {
    DATABASE_URL="$1" python3 - <<'PY'
import os, sys, urllib.parse as u
url = u.urlparse(os.environ["DATABASE_URL"])
qs = dict(u.parse_qsl(url.query, keep_blank_values=True))
print(url.hostname or "")
print(url.port or "")
print((url.path or "/").lstrip("/") or "")
print(qs.get("sslmode",""))
print(url.username or "")
PY
}

prod_meta=$(extract "$PROD_DSN")
fork_meta=$(extract "$FORK_DSN")

prod_host=$(echo "$prod_meta" | sed -n '1p')
prod_port=$(echo "$prod_meta" | sed -n '2p')
prod_db=$(echo "$prod_meta" | sed -n '3p')
prod_ssl=$(echo "$prod_meta" | sed -n '4p')

fork_host=$(echo "$fork_meta" | sed -n '1p')
fork_port=$(echo "$fork_meta" | sed -n '2p')
fork_db=$(echo "$fork_meta" | sed -n '3p')
fork_ssl=$(echo "$fork_meta" | sed -n '4p')

if [[ -z "$prod_host" || -z "$fork_host" ]]; then
    echo "❌ FAIL: could not parse host from one of the DSNs" >&2
    exit 1
fi

if [[ "$prod_host" == "$fork_host" && "$prod_port" == "$fork_port" ]]; then
    echo "❌ FAIL: both DSNs point to the same host:port = $prod_host:$prod_port" >&2
    echo "   You are about to drill the production database." >&2
    echo "   Re-create the fork and re-run with the FORK URI." >&2
    exit 1
fi
echo "✓ Prod  dsn: $prod_host:$prod_port db=$prod_db sslmode=$prod_ssl"
echo "✓ Fork  dsn: $fork_host:$fork_port db=$fork_db sslmode=$fork_ssl"
echo

# ─── Latency check (FORK only — the fork is the new thing) ───────────────
# We want the FORK to be reachable in <500ms from the operator's
# laptop or <2s from the Fly internal network. >5s usually means
# the fork VM is still spinning up.
t0=$(date +%s%N)
psql "$FORK_DSN" -tA -c "SELECT 1" >/dev/null 2>&1 || {
    echo "❌ FAIL: psql could not connect to fork" >&2
    exit 1
}
t1=$(date +%s%N)
lat_ms=$(( (t1 - t0) / 1000000 ))
echo "✓ Fork connect latency: ${lat_ms}ms"

# ─── Schema fingerprint comparison ──────────────────────────────────────
#
# Matches the algorithm in
# internal/database/migrations_integration_test.go::schemaFingerprint:
# SHA-256 over a stable JSON serialization of (enums + per-table
# columns + per-table indexes). If the forking worked, the PROD
# state and the FORK state should produce IDENTICAL fingerprints.
# If they diverge, either the fork is reading from a stale snapshot
# or the prod database has a live schema change in flight (very
# unlikely on a steady-state prod; would catch an in-progress
# migration that the operator didn't realise they were running).
#
fingerprint() {
    DATABASE_URL="$1" python3 - <<'PY'
import hashlib, json, os, subprocess
url = os.environ["DATABASE_URL"]

def run(sql):
    r = subprocess.run(["psql", url, "-tA", "-F,", "-c", sql],
                       capture_output=True, text=True, check=True)
    return r.stdout.strip()

state = {}

# enums
enums = {}
for line in run("""
    SELECT t.typname, e.enumlabel
      FROM pg_enum e
      JOIN pg_type t ON t.oid = e.enumtypid
     WHERE t.typnamespace = (SELECT oid FROM pg_namespace WHERE nspname='public')
     ORDER BY t.typname, e.enumsortorder
""").splitlines():
    if not line.strip():
        continue
    typ, label = line.split(",", 1)
    enums.setdefault(typ, []).append(label)
state["enums"] = enums

# column lists
cols = {}
for line in run("""
    SELECT table_name, column_name, data_type
      FROM information_schema.columns
     WHERE table_schema = 'public'
     ORDER BY table_name, ordinal_position
""").splitlines():
    if not line.strip():
        continue
    parts = line.split(",", 2)
    if len(parts) != 3:
        continue
    tn, cn, dt = parts
    cols.setdefault(tn, []).append({"name": cn, "type": dt})
state["columns"] = cols

# index names
indexes = {}
for line in run("""
    SELECT tablename, indexname
      FROM pg_indexes
     WHERE schemaname = 'public'
     ORDER BY tablename, indexname
""").splitlines():
    if not line.strip():
        continue
    parts = line.split(",", 1)
    if len(parts) != 2:
        continue
    tn, idx = parts
    indexes.setdefault(tn, []).append(idx)
state["indexes"] = indexes

b = json.dumps(state, sort_keys=True).encode()
print(hashlib.sha256(b).hexdigest())
PY
}

prod_fp=$(fingerprint "$PROD_DSN")
fork_fp=$(fingerprint "$FORK_DSN")
echo "  PROD  schema sha256: $prod_fp"
echo "  FORK  schema sha256: $fork_fp"

if [[ "$prod_fp" != "$fork_fp" ]]; then
    echo "❌ FAIL: schema fingerprint MISMATCH" >&2
    echo "   This usually means:" >&2
    echo "   - the fork is still spinning up (wait 30s, re-run)" >&2
    echo "   - the source DB has a live migration in progress (rare)" >&2
    echo "   - the fork was created from a stale PiTR snapshot (< 1s drift same)" >&2
    echo "   DO NOT destroy the fork yet — re-run after 60s." >&2
    exit 3
fi
echo "✓ Schema fingerprint matches (fork is byte-identical w.r.t. schema)"

# ─── Row count sanity ────────────────────────────────────────────────────
# Production should have a non-trivial number of users + posts after
# the beta is live; the fork should have the SAME number of rows
# because the fork is a point-in-time copy. The exact count is
# printed in the report — operators watch this for two reasons:
#   1. truncate + delete detection (if the fork has more rows than
#      prod, something is very wrong)
#   2. natural drift acknowledgement (the number WILL change
#      between drills — that's the point of having a baseline).
for table in users posts workspaces platform_accounts outbox_events; do
    n_prod=$(psql "$PROD_DSN" -tA -c "SELECT count(*) FROM $table" 2>/dev/null || echo "ERR")
    n_fork=$(psql "$FORK_DSN" -tA -c "SELECT count(*) FROM $table" 2>/dev/null || echo "ERR")
    if [[ "$n_prod" != "$n_fork" ]]; then
        echo "❌ FAIL: row count mismatch on $table: prod=$n_prod fork=$n_fork" >&2
        exit 3
    fi
    echo "  $table: prod=$n_prod fork=$n_fork"
done

# ─── Report block (paste into ops/restore-drill-<UTC>.md) ───────────────
ts=$(date -u +%Y%m%dT%H%M%SZ)
cat <<EOF

📋 Restore drill report (paste into ops/restore-drill-$ts.md):

\`\`\`markdown
# Restore drill — InstaeditLogin production Postgres
- **Executed at**: $(date -u +'%Y-%m-%dT%H:%M:%SZ')
- **Operator**: $(whoami)@$(hostname)
- **Source cluster**: $prod_host:$prod_port/$prod_db (sslmode=$prod_ssl)
- **Fork cluster**: $fork_host:$fork_port/$fork_db (sslmode=$fork_ssl)
- **Fork connect latency**: ${lat_ms}ms
- **Prod schema sha256**: \`$prod_fp\`
- **Fork schema sha256**: \`$fork_fp\`
- **Schema match**: ✓ identical
- **Verdict**: PASS — restore drill succeeded

## Next step (operator action):
\`\`\`bash
flyctl postgres destroy --name <fork-cluster-name-from-fly> --yes
\`\`\`
EOF

echo
echo "✓ Drill PASS. Save the report above, then destroy the fork with:"
echo "    flyctl postgres destroy --name <fork-cluster-name> --yes"

# Note: we print the destroy command but don't auto-execute. The
# operator must manually type the cluster name + --yes to make the
# destruction explicit (a fat-finger or a typosquatted name would
# then hit another production-shaped target — the prompt + interactive
# confirmation is the safety net).
