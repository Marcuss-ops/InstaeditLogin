#!/usr/bin/env bash
# HISTORICAL ARCHIVE — NON-OPERATIONAL
#
# This file is retained for audit/history only. DO NOT EXECUTE it or use it
# as an operational Fly destruction path. The canonical runtime is the VPS +
# Docker Compose stack; any future infrastructure action requires a separately
# reviewed operator procedure.
printf '%s\n' 'Archived historical material; destroy-fly-app.sh is non-operational.' >&2
exit 1
# docs/archive/legacy-fly/destroy-fly-app.sh — destroy all Fly.io resources tied
# to the Instaedit-login app instance. The VPS + Docker Compose stack
# is now the canonical runtime; api.instaedit.org already responds
# from the VPS. This script zaps the Fly side, in the user-listed
# order:
#   1. MACHINES (api vm, worker vm, any other VM in the app)
#   2. APP      instaedit-login
#   3. POSTGRES instaedit-production  (separate Fly app)
#   4. CERT     api.instaedit.org     (Fly ACME; Caddy now holds
#                                     the real cert on the VPS)
#   5. SECRETS  anything left in Fly Vault after app destroy
#   6. VOLUMES  anything still attached to the org
#
# Single responsibility: orchestrating flyctl-level destruction of
# 6 resource categories in idempotent order, with a curl-based
# safety gate (api.instaedit.org must return 200 BEFORE any
# destructive exec runs) and a TTY-guarded master confirmation.
#
# Why a separate script vs. inline shell + dashboard clicks?
#   - Idempotent on partial-failure re-runs (already-gone resources
#     are detected + skipped, never error).
#   - Curl safety gate pinned in the script (operator can't forget).
#   - Operator gets an audit trail at /tmp/fly_destroy_<ts>.log with
#     a SHA-256 hash for chain-of-custody when --apply succeeds.
#   - Single TTY confirmation gate prevents mid-run abandonment.
#   - Tigris (t3.storage.dev) is intentionally OUT-OF-SCOPE.
#
# Historical command surface (retained for audit context only; do not invoke):
#   former modes: default audit, --dry-run, --audit, --apply, --ui-fallback
# The archived file exits before reaching this historical implementation.
#
# Pre-requisites (run BEFORE this script):
#   1. DNS A-record for api.instaedit.org points at the VPS (NOT Fly).
#   2. VPS Caddy block + docker compose are answering 200 on /api/v1/health.
#   3. THIS script's --audit reports the safety gate = PASS.
#   4. python3 installed (used for JSON parsing of flyctl --json outputs).
#
# Exit codes:
#   0 = clean (dry-run print, --audit completed, --apply succeeded)
#   1 = safety gate failed (api.instaedit.org not 200) — do NOT proceed
#   2 = flyctl not installed, not authed, or python3 missing
#   3 = resource detection failed (malformed flyctl --json output)
#   4 = operator cancelled at the master confirm prompt
#   5 = one or more destructive steps failed — see apply log
#   6 = --apply invoked without an interactive TTY (refuse + suggest --ui-fallback)
#   7 = disambiguation failure (jq missing OR ambiguous attached_to) — see audit-log step 0
#   8 = backup transfer failure (mc cp non-zero OR Tigris unreachable) — see audit-log step 0
set -euo pipefail
trap 'rm -f /tmp/fly_apps.json /tmp/fly_machines.json /tmp/fly_certs.json /tmp/fly_secrets.json /tmp/fly_volumes.json /tmp/fly_postgres.json 2>/dev/null || true' EXIT

APP="instaedit-login"
POSTGRES="instaedit-production"
HEALTHCHECK_URL="https://api.instaedit.org/api/v1/health"

# ─── Mode parsing ────────────────────────────────────────────────────────
# ─── --ui-fallback short-circuit (must work without flyctl/python3/curl) ──
# This block happens BEFORE the tool pre-flight + safety gate so an
# operator reading the manual steps on a clean laptop (no flyctl CLI)
# still gets accurate guidance.
if [[ "${1:-}" == "--ui-fallback" ]]; then
  cat <<EOF
── Manual Fly Dashboard destruction ─────────────────────────────
Safety gate FIRST: \`curl https://api.instaedit.org/api/v1/health\` must return 200 BEFORE
you click Destroy on anything below. VPS already owns the canonical
hostname; this zaps the Fly side without affecting post-cutover runtime.

(Tigris OUT-OF-SCOPE: do NOT touch t3.storage.dev — it's a separate
 S3-compatible service; deleting it would break media uploads.)

1. https://fly.io/apps/$APP
   • Settings → "Delete App" (Danger Zone) → type '$APP' → confirm
   • Cascades app-scoped machines + certs + secrets + volumes.

2. https://fly.io/apps/$POSTGRES
   • Settings → "Destroy Cluster" → type the cluster name → confirm
   • Postgres is an independent Fly app; the app destroy above does
     NOT touch it.

3. https://fly.io/apps/$APP/certificates  (after step 1)
   • Verify no Let's Encrypt cert for api.instaedit.org still listed.
     If present: Actions → Remove. (Caddy owns the real cert now.)

4. https://fly.io/orgs/<YOUR-ORG>/secrets  (after step 1)
   • If any secrets bearing the $APP prefix linger, remove them.
     (App destroy should cascade-clear; this is paranoid cleanup.)

5. https://fly.io/orgs/<YOUR-ORG>/volumes  (after step 1)
   • Verify no volumes bound to the deleted app or postgres cluster.
     If present: Actions → Destroy.

6. https://fly.io/dashboard
   • Confirm $APP + $POSTGRES no longer appear in 'Your Apps' / 'Your Databases'.

(Generated by: \`$(basename "$0") --ui-fallback\`)
EOF
  exit 0
fi

# ─── Mode parsing ────────────────────────────────────────────────────────
# Default mode = --audit: when an operator runs no-arg, they want to
# see what's actually on Fly right now, not an abstract verb list.
mode="audit"
case "${1:-}" in
  "")               mode="audit" ;;
  "--dry-run")      mode="dry-run" ;;
  "--audit")        mode="audit" ;;
  "--apply")        mode="apply" ;;
  "--ui-fallback")  mode="ui-fallback" ;;
  *)
    echo "usage: $0 [--audit|--dry-run|--apply|--ui-fallback]  (default: --audit)" >&2
    exit 1
    ;;
esac

# ─── Tool pre-flight (only for modes that actually talk to flyctl) ──────
# --ui-fallback already short-circuited above. The remaining modes all
# invoke flyctl at least once.
if ! command -v flyctl >/dev/null 2>&1; then
  echo "❌ flyctl CLI not installed. Install from https://fly.io/docs/hacks/links/install-flyctl/  (or run --ui-fallback)." >&2
  exit 2
fi

if ! flyctl auth whoami >/dev/null 2>&1; then
  echo "❌ flyctl not authenticated. Run: flyctl auth login  (or --ui-fallback)." >&2
  exit 2
fi

if ! command -v python3 >/dev/null 2>&1; then
  echo "❌ python3 not installed. Required for JSON parsing of flyctl --json outputs." >&2
  exit 2
fi

# ─── Safety gate: api.instaedit.org must return 200 before --apply ──────
status=$(curl -sL -m 5 -o /dev/null -w "%{http_code}" "$HEALTHCHECK_URL" 2>/dev/null || echo "000")
if [[ "$status" == "200" ]]; then
  echo "✓ Safety gate: $HEALTHCHECK_URL returned 200"
  safety_gate_pass=1
else
  echo "❌ Safety gate: $HEALTHCHECK_URL returned $status (expected 200)"
  echo "    VPS DNS cutover is NOT yet green; would leave production offline." >&2
  echo "    Refusing all destruction (audit/--ui-fallback still allowed)." >&2
  safety_gate_pass=0
  if [[ "$mode" == "apply" ]]; then exit 1; fi
fi

# Always-on Tigris bypass acknowledgement
echo "Note: Tigris (t3.storage.dev) is OUT-OF-SCOPE — external S3 service, untouched by Fly app destruction."
echo

# ─── Helpers (read flyctl --json + emit resource IDs on stdout) ──────────
# Each helper is silenced against set -e so a missing app or empty list
# produces an empty stdout (no rows), which the caller's while-read loop
# happily skips.

fly_app_present() {
  flyctl apps list --json 2>/dev/null > /tmp/fly_apps.json || true
  if ! [[ -s /tmp/fly_apps.json ]]; then return 1; fi
  python3 -c '
import json, sys
try:
  data = json.load(open("/tmp/fly_apps.json"))
except Exception:
  sys.exit(1)
sys.exit(0 if any(a.get("Name") == "'"$APP"'" for a in (data or [])) else 1)
'
}

fly_postgres_present() {
  flyctl postgres list --json 2>/dev/null > /tmp/fly_postgres.json || true
  if ! [[ -s /tmp/fly_postgres.json ]]; then return 1; fi
  python3 -c '
import json, sys
try:
  data = json.load(open("/tmp/fly_postgres.json"))
except Exception:
  sys.exit(1)
sys.exit(0 if any(a.get("Name") == "'"$POSTGRES"'" for a in (data or [])) else 1)
'
}

fly_machines_list() {
  flyctl machines list --app "$APP" --json 2>/dev/null > /tmp/fly_machines.json || true
  python3 -c '
import json, sys
try:
  data = json.load(open("/tmp/fly_machines.json"))
except Exception:
  sys.exit(0)
for m in (data or []):
  if "id" in m: print(m["id"])
' 2>/dev/null || true
}

fly_certs_list() {
  # Certs are org-level key-value. App-scoped listing via --app may not
  # return everything if the cert was registered at the org layer first;
  # cross-reference both lists.
  flyctl certs list --json 2>/dev/null > /tmp/fly_certs.json || true
  flyctl certs list --app "$APP" --json 2>/dev/null >> /tmp/fly_certs.json || true
  python3 -c '
import json, sys
seen = set()
out = []
try:
  txt = open("/tmp/fly_certs.json").read().strip()
  # Two concatenated JSON arrays -> union them naively.
  import re
  chunks = re.findall(r"\[.*?\]", txt, re.DOTALL)
  for chunk in chunks:
    try:
      arr = json.loads(chunk)
    except Exception:
      continue
    for c in (arr or []):
      host = c.get("hostname") or c.get("HostName") or c.get("name")
      if host and host not in seen:
        seen.add(host); out.append(host)
except Exception:
  pass
for h in out: print(h)
' 2>/dev/null || true
}

fly_secrets_list() {
  flyctl secrets list --app "$APP" --json 2>/dev/null > /tmp/fly_secrets.json || true
  python3 -c '
import json, sys
try:
  data = json.load(open("/tmp/fly_secrets.json"))
except Exception:
  sys.exit(0)
items = data.get("secrets", data) if isinstance(data, dict) else data
for s in (items or []):
  if "name" in s: print(s["name"])
' 2>/dev/null || true
}

fly_volumes_list() {
  flyctl volumes list --json 2>/dev/null > /tmp/fly_volumes.json || true
  python3 -c '
import json, sys
try:
  data = json.load(open("/tmp/fly_volumes.json"))
except Exception:
  sys.exit(0)
for v in (data or []):
  for k in ("id", "Id", "Name"):
    if k in v and v[k]:
      print(v[k])
      break
' 2>/dev/null || true
}

# ─── Mode: --audit (read-only inventory) ─────────────────────────────────
if [[ "$mode" == "audit" ]]; then
  echo "─── AUDIT — Fly resources bound to the cutover ───"
  echo "Safety gate: $([[ $safety_gate_pass -eq 1 ]] && echo PASS || echo FAIL)"
  echo
  echo "App $APP:"
  if fly_app_present; then echo "  ✓ present"; else echo "  · absent"; fi
  echo
  echo "Postgres $POSTGRES:"
  if fly_postgres_present; then echo "  ✓ present"; else echo "  · absent"; fi
  echo
  m_ids=$(fly_machines_list)
  if [[ -n "$m_ids" ]]; then
    echo "Machines (app $APP):"
    while IFS= read -r id; do echo "  ✓ $id"; done <<<"$m_ids"
  else
    echo "Machines: (empty)"
  fi
  echo
  c_hosts=$(fly_certs_list)
  if [[ -n "$c_hosts" ]]; then
    echo "Certs (org + app $APP):"
    while IFS= read -r h; do echo "  ✓ $h"; done <<<"$c_hosts"
  else
    echo "Certs: (none)"
  fi
  echo
  s_names=$(fly_secrets_list)
  if [[ -n "$s_names" ]]; then
    echo "Secrets (Fly Vault for $APP):"
    while IFS= read -r n; do echo "  ✓ $n"; done <<<"$s_names"
  else
    echo "Secrets: (none)"
  fi
  echo
  v_ids=$(fly_volumes_list)
  if [[ -n "$v_ids" ]]; then
    echo "Volumes (org-level):"
    while IFS= read -r id; do echo "  ✓ $id"; done <<<"$v_ids"
  else
    echo "Volumes: (none)"
  fi
  echo
  if [[ $safety_gate_pass -eq 1 ]]; then
    echo "Audit gate: PASS."
    echo "If inventory is correct, run: $0 --apply"
  else
    echo "Audit gate: FAIL — fix DNS/VPS first; --apply will refuse."
  fi
  exit 0
fi

# ─── Mode: --dry-run (default; print the destruction plan) ────────────────
if [[ "$mode" == "dry-run" ]]; then
  echo "─── DRY-RUN: destruction plan (in user-listed order) ───"
  echo "Safety gate: $([[ $safety_gate_pass -eq 1 ]] && echo PASS || echo FAIL)"
  echo "Tigris bypass: explicit out-of-scope (already noted above)"
  echo
  echo "Order:"
  echo "  1) machines (one flyctl machines destroy <id> per machine)"
  echo "  2) app $APP (flyctl apps destroy --app $APP --yes)"
  echo "  3) postgres $POSTGRES (flyctl postgres destroy --name $POSTGRES --yes)"
  echo "  4) certs (flyctl certs delete <hostname> --app $APP for each)"
  echo "  5) secrets (flyctl secrets unset <name> --app $APP for each)"
  echo "  6) volumes (flyctl volumes delete <id> for each)"
  echo
  echo "Re-running after a partial run skips already-gone resources silently."
  echo "Run '$0 --audit' for the current inventory; '$0 --apply' to execute."
  exit 0
fi

# ─── Mode: --apply — actual destruction ───────────────────────────────────
if [[ "$mode" == "apply" ]]; then
  if [[ $safety_gate_pass -ne 1 ]]; then
    echo "❌ Safety gate must PASS before --apply. Refusing." >&2
    exit 1
  fi
  if [[ ! -t 0 ]]; then
    echo "❌ --apply requires an interactive terminal (stdin is not a TTY)." >&2
    echo "   Re-run from a real shell, or use --ui-fallback." >&2
    exit 6
  fi

  apply_log="/tmp/fly_destroy_$(date -u +%Y%m%dT%H%M%SZ).log"
  echo "Audit log: $apply_log"
  echo

  # Capture current inventory for the confirm prompt
  app_present=0; fly_app_present && app_present=1 || true
  pg_present=0; fly_postgres_present && pg_present=1 || true
  m_count=$(fly_machines_list | wc -l | tr -d ' ')
  c_count=$(fly_certs_list | wc -l | tr -d ' ')
  s_count=$(fly_secrets_list | wc -l | tr -d ' ')
  v_count=$(fly_volumes_list | wc -l | tr -d ' ')

  echo "Detected on Fly:"
  echo "  app $APP:                $([[ $app_present -eq 1 ]] && echo present || echo absent)"
  echo "  postgres $POSTGRES:      $([[ $pg_present -eq 1 ]] && echo present || echo absent)"
  echo "  machines:                $m_count"
  echo "  certs (org + app):       $c_count"
  echo "  secrets (Fly Vault):     $s_count"
  echo "  volumes (org-level):     $v_count"
  echo
  echo "Order (per user instruction):"
  echo "  1. machines   2. app   3. postgres   4. cert   5. secrets   6. volumes"
  echo
  read -rp "Confirm: destroy the above on Fly? Type 'yes' to continue: " confirm
  if [[ "$confirm" != "yes" ]]; then
    echo "Aborted by operator. No resources were modified." >&2
    exit 4
  fi

  # Wrap destructive exec via tee for audit trail. The whole block writes
  # to BOTH the audit log AND stderr (so operator sees the steps live).
  failures=0
  {
    step_no=0
    declare -a failed_steps=()

    step() {
      local label="$1"; shift
      step_no=$((step_no+1))
      echo "── Step $step_no: $label ──"
    }

    # ─── Step 0 — Tigris disambiguation + conditional backup ───
    # Integrated into --apply so the operator has a single-shot workflow.
    # Mirrors docs/archive/legacy-fly/FLY-DESTROY-RUNBOOK.md §2-§3 (jq disambiguation + mc
    # version enable + Path A local mirror); uses the bare-value jq
    # recursive-descent so $ATTACHED is a clean app id (no field name,
    # no surrounding quotes). Bypasses the step() helper so the existing
    # 6 step numbers stay untouched.
    #
    # New exit codes:
    #   7 = disambiguation failure (jq missing OR ambiguous attached_to)
    #   8 = backup transfer failure (mc cp non-zero)
    #
    # mc-missing path is graceful (warn + skip) — matches runbook §1
    # caveat "mc only required if §3 path is taken".
    echo "── Step 0: TIGRIS BUCKET (disambiguate; backup if Fly-attached) ──"
    if ! command -v jq >/dev/null 2>&1; then
      echo "  ❌ jq missing — cannot safely disambiguate Tigris." >&2
      failed_steps+=(0); exit 7
    fi
    flyctl storage list --app "$APP" --json > /tmp/fly-storage.json 2>/dev/null || true
    ATTACHED=$(jq -r '.[]? | (.attached_to // .AttachedTo // .app_id) // ""' \
                /tmp/fly-storage.json 2>/dev/null | head -1 || true)
    [[ -z "$ATTACHED" && -s /tmp/fly-storage.json ]] \
      && ATTACHED=$(jq -r '.. | objects | to_entries[] | select(.key|test("^(attached_to|AttachedTo|app_id)$")) | .value' \
                    /tmp/fly-storage.json 2>/dev/null | head -1 || true)
    if [[ "$ATTACHED" == *"$APP"* ]]; then
      echo "  · Fly-attached detected: ${ATTACHED}"
        if ! command -v mc >/dev/null 2>&1; then
          echo "  ⚠️  mc missing — SKIPPING §3 backup (Tigris contents at risk)" >&2
          echo "     install mc: brew install minio/stable/mc (or apt install mc)" >&2
        elif ! mc alias list 2>/dev/null | grep -q '^tigris '; then
          echo "  ⚠️  tigris alias not configured — SKIPPING §3 backup" >&2
          echo "     (silent backup loss would be worse; run docs/archive/legacy-fly/FLY-DESTROY-RUNBOOK.md §3.1 first)" >&2
        else
          echo "  · running §3 backup (mc version enable + Path A)"
          mc version enable tigris/instaedit-prod-media 2>/dev/null || true
          SNAP_DIR="/tmp/tigris-snapshot-$(date -u +%Y%m%dT%H%M%SZ)"
          mkdir -p "$SNAP_DIR"
          if mc_err=$(mc cp --recursive tigris/instaedit-prod-media/ "${SNAP_DIR}/" 2>&1); then
            echo "  ✓ Path A local mirror: ${SNAP_DIR} ($(du -sh "${SNAP_DIR}" | cut -f1))"
          else
            echo "  ❌ mc cp failed: ${mc_err}" >&2
            echo "     refusing destroy with incomplete backup." >&2
            failed_steps+=(0); exit 8
          fi
        fi
    elif [[ -z "$ATTACHED" ]]; then
      echo "  · standalone Tigris (or no bucket) — no backup needed"
    else
      echo "  ❌ ambiguous attached_to='${ATTACHED}' — refusing destruction." >&2
      failed_steps+=(0); exit 7
    fi

    # Step 1 — machines
    step "MACHINES (api + worker + any others)"
    if [[ $app_present -eq 1 ]]; then
      m_ids=$(fly_machines_list)
      if [[ -z "$m_ids" ]]; then
        echo "  · no machines detected; skipping"
      else
        while IFS= read -r id; do
          [[ -z "$id" ]] && continue
          echo "  • $id: destroying"
          if flyctl machines destroy "$id" --app "$APP" >/dev/null 2>&1; then
            echo "    ✓ deleted"
          else
            echo "    ❌ failed (run --audit to see error chain)"
            failures=$((failures+1)); failed_steps+=("machine:$id")
          fi
        done <<<"$m_ids"
      fi
    else
      echo "  · app $APP absent; skipping (no orphan machines to find)"
    fi

    # Step 2 — app
    step "APP $APP"
    if [[ $app_present -eq 1 ]]; then
      echo "  • destroying $APP"
      if flyctl apps destroy --app "$APP" --yes >/dev/null 2>&1; then
        echo "    ✓ deleted"
        app_present=0
      else
        echo "    ❌ failed"
        failures=$((failures+1)); failed_steps+=("app:$APP")
      fi
    else
      echo "  · app $APP already absent; skipping"
    fi

    # Step 3 — postgres
    step "POSTGRES $POSTGRES"
    if [[ $pg_present -eq 1 ]]; then
      echo "  • destroying $POSTGRES"
      if flyctl postgres destroy --name "$POSTGRES" --yes >/dev/null 2>&1; then
        echo "    ✓ deleted"
        pg_present=0
      else
        echo "    ❌ failed"
        failures=$((failures+1)); failed_steps+=("postgres:$POSTGRES")
      fi
    else
      echo "  · postgres $POSTGRES already absent; skipping"
    fi

    # Step 4 — certs
    step "CERTS (Fly-managed for $APP)"
    c_hosts=$(fly_certs_list)
    if [[ -z "$c_hosts" ]]; then
      echo "  · no certs detected; skipping"
    else
      while IFS= read -r h; do
        [[ -z "$h" ]] && continue
        echo "  • removing $h"
        # Try modern API first, fall back to legacy.
        if flyctl certs delete "$h" --app "$APP" >/dev/null 2>&1 \
            || flyctl certs remove "$h" --app "$APP" >/dev/null 2>&1; then
          echo "    ✓ removed"
        else
          echo "    ❌ failed"
          failures=$((failures+1)); failed_steps+=("cert:$h")
        fi
      done <<<"$c_hosts"
    fi

    # Step 5 — secrets
    step "SECRETS (Fly Vault for $APP)"
    s_names=$(fly_secrets_list)
    if [[ -z "$s_names" ]]; then
      echo "  · no secrets detected; skipping"
    else
      while IFS= read -r n; do
        [[ -z "$n" ]] && continue
        echo "  • unsetting $n"
        if flyctl secrets unset "$n" --app "$APP" >/dev/null 2>&1; then
          echo "    ✓ unset"
        else
          echo "    ❌ failed"
          failures=$((failures+1)); failed_steps+=("secret:$n")
        fi
      done <<<"$s_names"
    fi

    # Step 6 — volumes
    step "VOLUMES (org-level)"
    v_ids=$(fly_volumes_list)
    if [[ -z "$v_ids" ]]; then
      echo "  · no volumes detected; skipping"
    else
      while IFS= read -r id; do
        [[ -z "$id" ]] && continue
        echo "  • deleting $id"
        if flyctl volumes delete "$id" >/dev/null 2>&1 \
            || flyctl volumes destroy "$id" >/dev/null 2>&1; then
          echo "    ✓ deleted"
        else
          echo "    ❌ failed"
          failures=$((failures+1)); failed_steps+=("volume:$id")
        fi
      done <<<"$v_ids"
    fi

    # Final: re-verify safety gate still PASS post-destruction.
    echo "── Post-destruction safety gate ──"
    sleep 2  # let any DNS / cache settle
    final_status=$(curl -sL -m 5 -o /dev/null -w "%{http_code}" "$HEALTHCHECK_URL" 2>/dev/null || echo "000")
    if [[ "$final_status" == "200" ]]; then
      echo "  ✓ $HEALTHCHECK_URL still 200 — VPS-canonical is unaffected by Fly destruction."
    else
      echo "  ❌ $HEALTHCHECK_URL returned $final_status (expected 200) — INVESTIGATE."
      failures=$((failures+1)); failed_steps+=("post-gate:HEALTHCHECK")
    fi

    echo
    if [[ $failures -eq 0 ]]; then
      echo "── DONE: all 6 steps clean (no failures). ──"
    else
      echo "── DONE with $failures failure(s): ${failed_steps[*]} ──"
      echo "    Re-run '$0 --apply' to retry already-present resources."
    fi
  } 2>&1 | tee "$apply_log" >&2

  # Always disclose the audit-log path + SHA-256 chain-of-custody,
  # regardless of failure count. On partial-failure runs, the operator
  # still needs to find the partial log on disk.
  #
  # Defensive: capture sha256sum into a variable first, then print with
  # a `${hash:-<unavailable>}` fallback. On Alpine/slim/macOS environments
  # without coreutils sha256sum, the substitution would otherwise leave
  # the SHA row torn on the terminal.
  echo
  echo "Audit log:    $apply_log"
  hash=$(sha256sum "$apply_log" 2>/dev/null | awk '{print $1}')
  echo "SHA-256:      ${hash:-<unavailable>}"

  if [[ $failures -eq 0 ]]; then
    exit 0
  else
    exit 5
  fi
fi
