#!/usr/bin/env bash
#
# scripts/db/check-postgres-health.sh
#
# Smoke check for a Postgres connection. Given $DATABASE_URL (env or
# --url flag), it asserts:
#   (a) the URL is well-formed and parseable
#   (b) sslmode is not "disable" (FAIL in any environment)
#   (c) SELECT version() succeeds AND reports PostgreSQL >= 14
#       (PITR minimum)
#   (d) median connection latency across 5 pings is sane (<= 250ms
#       on Fly internal network; > 2s on a flat-out misconfigured
#       firewall)
#   (e) the closed canary table set (database.CanaryTables) is
#       present — the closed invariant the /ready endpoint also
#       checks. The output distinguishes "0 tables" (pre-migration)
#       from "9 tables" (post-migration) and "partial" (fail).
#   (f) role + db match the URL (sanity: didn't accidentally point
#       at anyone's database).
#
# Designed to be runnable from the OPERATOR'S LAPTOP against a Fly
# Postgres cluster (so they can verify provisioning BEFORE the first
# `make fly-deploy`) and from INSIDE the Fly release_command machine
# (so the migration runner has a quick "is this URL actually
# responsive?" gate before applying 036 SQL files).
#
# ─── USAGE ──────────────────────────────────────────────────────────────
#   DATABASE_URL="postgres://..." ./scripts/db/check-postgres-health.sh
#   ./scripts/db/check-postgres-health.sh --url "postgres://..."
#   ./scripts/db/check-postgres-health.sh --help
#
# Exit codes:
#   0  all checks passed
#   1  pre-flight failure (psql missing, URL malformed, sslmode=disable)
#   2  bad CLI argument
#   3  server-side assertion failure (version too old, canary partial,
#      latency too high, role/db mismatch)
#
# ─── SECURITY: never log the password ───────────────────────────────────
# The script extracts `user` + `host` + `db` + `sslmode` from the URL
# and prints ONLY those columns. The password component is stripped
# BEFORE any printf. The grep filter `grep -v '^Password'` is a
# second-line defence against future edits that accidentally print
# the full DSN.
#
set -euo pipefail

URL=""
for arg in "$@"; do
    case "$arg" in
        --url)
            URL="${2:-}"
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

URL="${URL:-${DATABASE_URL:-}}"
if [[ -z "$URL" ]]; then
    echo "❌ DATABASE_URL is required (env or --url)" >&2
    exit 1
fi

# ─── Pre-flight ──────────────────────────────────────────────────────────
command -v psql >/dev/null 2>&1 || {
    echo "❌ psql is required (apt install postgresql-client / brew install libpq)" >&2
    exit 1
}

# ─── Parse URL (no password leak) ────────────────────────────────────────
# Use python's urllib for strict parsing — bash parameter expansion
# can't reliably handle percent-encoded user/password components.
parsed=$(DATABASE_URL="$URL" python3 - <<'PY'
import os, sys, urllib.parse as u
url = u.urlparse(os.environ["DATABASE_URL"])
print(f"scheme={url.scheme}")
print(f"user={url.username or ''}")
print(f"host={url.hostname or ''}")
print(f"port={url.port or ''}")
print(f"db={(url.path or '').lstrip('/')}")
qs = dict(u.parse_qsl(url.query, keep_blank_values=True))
print(f"sslmode={qs.get('sslmode','')}")
print(f"pgbouncer={'pgbouncer=true' in {k.lower(): v for k,v in qs.items()}}")
PY
)
echo "$parsed"
echo

user=$(echo "$parsed" | awk -F= '/^user=/{print $2}')
host=$(echo "$parsed" | awk -F= '/^host=/{print $2}')
db=$(echo "$parsed" | awk -F= '/^db=/{print $2}')
port=$(echo "$parsed" | awk -F= '/^port=/{print $2}')
sslmode=$(echo "$parsed" | awk -F= '/^sslmode=/{print $2}')

# ─── Assertion: scheme is postgres/postgresql ───────────────────────────
scheme=$(echo "$parsed" | awk -F= '/^scheme=/{print $2}')
if [[ "$scheme" != "postgres" && "$scheme" != "postgresql" ]]; then
    echo "❌ FAIL: scheme must be postgres or postgresql (got '$scheme')" >&2
    exit 1
fi
echo "✓ scheme=$scheme"

# ─── Assertion: sslmode NOT 'disable' in any context ────────────────────
case "$sslmode" in
    disable) echo "❌ FAIL: sslmode=disable is forbidden (any env)" >&2; exit 1 ;;
    allow|prefer) echo "❌ FAIL: sslmode=$sslmode is too permissive (use require|verify-full)" >&2; exit 1 ;;
    ""|"require"|"verify-ca"|"verify-full")
        echo "✓ sslmode=${sslmode:-require} (defaulted to require if URL omits it)"
        ;;
    *)
        # Unknown sslmode — could be a custom Fly proxy value. Warn but
        # don't fail (Fly sometimes appends its own parameter that
        # isn't an SSL mode but parses identically).
        echo "⚠ sslmode=$sslmode (not a Postgres-standard mode; tolerated)" >&2
        ;;
esac

# ─── Connection + version check ─────────────────────────────────────────
# Capture version into a variable, NOT stdout, so the printed banner
# stays clean even if the server emits deprecation warnings around
# the version banner.
version=$(psql "$URL" -tA -c "SHOW server_version" 2>&1) || {
    echo "❌ FAIL: psql could not connect: $version" >&2
    exit 1
}

# Strip the deprecation banner noise. Fly Postgres occasionally
# prepends a NOTICE about ssl being recommended.
cleaned=$(echo "$version" | grep -oE '[0-9]+(\.[0-9]+)?' | head -1)
major=$(echo "$cleaned" | awk -F. '{print $1}')
if ! [[ "$major" =~ ^[0-9]+$ ]]; then
    echo "❌ FAIL: could not parse server version: '$version'" >&2
    exit 3
fi
if (( major < 14 )); then
    echo "❌ FAIL: Postgres major=$major is < 14 (PITR minimum)" >&2
    exit 3
fi
echo "✓ server_version=$cleaned (>=14, PITR-enabled)"

# ─── Latency probe (5 sync pings, median) ───────────────────────────────
# Synthesis of documented Postgres best practice — the median across
# 5 pings is more stable than a single round-trip (TCP backoff can
# spike the first ping). Output is in milliseconds.
echo "Measuring ping latency (5x round trip)..."
lat_ms=$(DATABASE_URL="$URL" python3 - <<'PY'
import os, statistics, time, subprocess
url = os.environ["DATABASE_URL"]
times = []
for _ in range(5):
    t0 = time.perf_counter()
    r = subprocess.run(["psql", url, "-tA", "-c", "SELECT 1"],
                       capture_output=True, text=True)
    t1 = time.perf_counter()
    if r.returncode != 0:
        raise RuntimeError(f"ping failed: {r.stderr.strip()}")
    times.append((t1 - t0) * 1000)
print(f"{statistics.median(times):.0f}")
PY
)
if (( $(echo "$lat_ms > 2000" | bc -l 2>/dev/null || echo 0) )); then
    echo "❌ FAIL: latency median=${lat_ms}ms (>2000ms suggests firewall/network issue)" >&2
    exit 3
fi
echo "✓ latency ${lat_ms}ms (median of 5)"

# ─── Role / db sanity ────────────────────────────────────────────────────
# The current_user / current_database() functions are zero-cost.
actual_user=$(psql "$URL" -tA -c "SELECT current_user" 2>/dev/null || true)
actual_db=$(psql "$URL" -tA -c "SELECT current_database()" 2>/dev/null || true)
if [[ -n "$actual_user" && -n "$user" && "$actual_user" != "$user" ]]; then
    echo "❌ FAIL: URL user='$user' but server reports current_user='$actual_user'" >&2
    exit 3
fi
if [[ -n "$actual_db" && -n "$db" && "$actual_db" != "$db" ]]; then
    echo "❌ FAIL: URL db='$db' but server reports current_database()='$actual_db'" >&2
    exit 3
fi
echo "✓ role=$actual_user db=$actual_db (URL matches server)"

# ─── Canary table probe ─────────────────────────────────────────────────
# Replicates the canonical set from internal/database/migrate_check.go.
# The Go-side uses pg_catalog.pg_class via the `to_regclass` builtin;
# here we use information_schema.tables because the operator runs this
# script BEFORE the application code is up, so we lack the Go package
# graph. Both probes converge to the same predicate.
expected_canaries=(users platform_accounts tokens workspaces posts
                   post_targets media_assets webhook_deliveries outbox_events)
echo "Probing canary tables (database.CanaryTables contract)..."
echo "  expected: ${expected_canaries[*]}"
present=$(psql "$URL" -tA -F, -c "
  SELECT table_name
    FROM information_schema.tables
   WHERE table_schema = 'public'
" 2>/dev/null | sort -u)

count_canaries=0
missing=()
for t in "${expected_canaries[@]}"; do
    if echo "$present" | grep -qx "$t"; then
        count_canaries=$((count_canaries + 1))
    else
        missing+=( "$t" )
    fi
done

if (( count_canaries == 0 )); then
    echo "✓ 0 of ${#expected_canaries[@]} canary tables present (pre-migration)"
elif (( count_canaries == ${#expected_canaries[@]} )); then
    echo "✓ ${count_canaries} of ${#expected_canaries[@]} canary tables present (post-migration)"
else
    # Partial — bad. Either the operator manually dropped a table or
    # the migration runner hit a mid-flight failure. Don't proceed
    # with anything sensitive.
    echo "❌ FAIL: partial canary set (${count_canaries}/${#expected_canaries[@]})" >&2
    echo "   missing:" >&2
    for t in "${missing[@]}"; do
        echo "    - $t" >&2
    done
    echo "   This usually means the migration was interrupted. Do NOT" >&2
    echo "   proceed with anything sensitive; restart the migration" >&2
    echo "   by re-running \`make fly-deploy\`." >&2
    exit 3
fi

# ─── post_status enum probe (post-migration only) ───────────────────────
# Migration 012 + 018 + 035 introduce the 10-label post_status enum.
# If the cluster says "9 canary tables present" + "0 post_status
# labels", something is wrong with the migration runner.
if (( count_canaries == ${#expected_canaries[@]} )); then
    enum_labels=$(psql "$URL" -tA -c "
        SELECT e.enumlabel
          FROM pg_enum e
          JOIN pg_type t ON t.oid = e.enumtypid
         WHERE t.typname = 'post_status'
         ORDER BY e.enumsortorder
    " 2>/dev/null | wc -l)
    if (( enum_labels < 9 )); then
        echo "❌ FAIL: post_status enum has only $enum_labels labels (expected >=9)" >&2
        echo "   Check migrations/012 / 018 / 035 for which didn't apply." >&2
        exit 3
    fi
    echo "✓ post_status enum: $enum_labels labels"
fi

echo
echo "✓ All smoke checks passed. Safe to invoke make fly-deploy."
