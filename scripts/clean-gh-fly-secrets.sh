#!/usr/bin/env bash
# scripts/clean-gh-fly-secrets.sh — remove the orphan hosted-platform
# secrets that were registered on the GitHub repo back when the
# `flyctl` deploy pipeline was the production surface. Post-cutover
# the runtime is VPS + Docker Compose only; the 3 secrets below are
# dead-but-still-registered on github.com and SHOULD be cleaned up
# alongside the rest of the cutover chain.
#
# Single responsibility: orchestrate `gh secret delete` for the 3
# defined names on the canonical repo (Marcuss-ops/InstaeditLogin),
# with a `--list-only` audit default so the script is safe to run
# for inspection purposes.
#
# Why a separate script vs. inlining in a workflow?
#   - Secrets deletion is an operator-side action, not a CI job.
#   - The `secrets:write` scope is a privilege-grant path; we never
#     want it baked into a workflow that runs on PRs.
#   - The script is single-shot intent-driven, easier to audit than a
#     reusable GitHub Action.
#
# Usage:
#   scripts/clean-gh-fly-secrets.sh            # default: list-only (no deletes)
#   scripts/clean-gh-fly-secrets.sh --apply    # actually delete the 3 secrets
#   scripts/clean-gh-fly-secrets.sh --check    # assert NONE of the 3 are registered
#   scripts/clean-gh-fly-secrets.sh --ui-fallback
#       # print manual Settings → Secrets and variables → Actions steps
#
# Manual UI fallback (when the gh PAT lacks `secrets:write` scope):
#   1. Open https://github.com/Marcuss-ops/InstaeditLogin/settings/secrets/actions
#   2. For each of FLY_API_TOKEN, FLY_ACCESS_TOKEN, FLY_APP_NAME:
#      click "Update" → ⋮ → "Remove secret" → confirm.
#   3. Re-open the URL; the 3 names should be absent.
#
# Exit codes:
#   0 = clean state (default list-only print, --check success, or --apply succeeded)
#   1 = gh CLI not installed or not authenticated as Marcuss-ops
#   2 = repo detection failed (gh repo view errored)
#   3 = `gh secret list` failed (PAT lacks scope; print UI fallback)
#   4 = one or more target secrets were not registered (and --apply was used → refuse)
#   5 = a `gh secret delete` call failed (network / 403 / 404)
set -euo pipefail

REPO="Marcuss-ops/InstaeditLogin"
SECRETS=(
  FLY_API_TOKEN
  FLY_ACCESS_TOKEN
  FLY_APP_NAME
)

mode="list-only"
if [[ "${1:-}" == "--apply" ]]; then
  mode="apply"
elif [[ "${1:-}" == "--check" ]]; then
  mode="check"
elif [[ "${1:-}" == "--ui-fallback" ]]; then
  mode="ui-fallback"
elif [[ $# -gt 0 ]]; then
  echo "usage: $0 [--list-only|--apply|--check|--ui-fallback]" >&2
  exit 1
fi

# ─── Pre-flight: gh CLI installed + authenticated ────────────────────────
if ! command -v gh >/dev/null 2>&1; then
  echo "❌ gh CLI not installed. Install from https://cli.github.com or run --ui-fallback." >&2
  exit 1
fi

if ! gh auth status >/dev/null 2>&1; then
  echo "❌ gh CLI not authenticated. Run: gh auth login  (or --ui-fallback)." >&2
  exit 1
fi

# ─── Repo detection (assert the local checkout matches the target repo) ──
detected=$(gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null || true)
if [[ -z "$detected" ]]; then
  echo "❌ gh repo view failed; could not determine target. Re-run gh auth login + verify network." >&2
  exit 2
fi

if [[ "$detected" != "$REPO" ]]; then
  echo "❌ local checkout maps to '$detected', expected '$REPO'." >&2
  echo "   Refusing to operate on an unexpected repo. Override only if you really mean it:" >&2
  echo "   gh secret delete FLY_API_TOKEN -R $detected ..." >&2
  exit 2
fi

# ─── Mode handlers ───────────────────────────────────────────────────────
case "$mode" in
  ui-fallback)
    cat <<EOF
── Manual UI fallback ─────────────────────────────────────────────
1. Open this URL in your browser:
     https://github.com/$REPO/settings/secrets/actions

2. For each of the 3 secrets below:
     FLY_API_TOKEN
     FLY_ACCESS_TOKEN
     FLY_APP_NAME
   click "Update" → ⋮ → "Remove secret" → confirm.

3. Re-open the URL; the 3 names should be absent.

Why a fallback? The PAT behind \`gh\` lacks the \`secrets:write\` scope
(or repo admin scope). Grant it via:
  gh auth refresh -s repo
and re-run this script with --apply.
EOF
    exit 0
    ;;

  list-only)
    echo "─── gh secret list --repo $REPO ───"
    if ! gh secret list --repo "$REPO" 2>/tmp/list_err; then
      err=$(cat /tmp/list_err)
      echo "❌ gh secret list failed: $err" >&2
      if grep -qi '403' /tmp/list_err; then
        echo "   PAT likely lacks secrets:write scope. Run --ui-fallback." >&2
      fi
      rm -f /tmp/list_err
      exit 3
    fi
    rm -f /tmp/list_err

    echo
    echo "─── Fly-side secrets registered on $REPO ───"
    found=0
    for s in "${SECRETS[@]}"; do
      if gh secret list --repo "$REPO" 2>/dev/null | awk 'NR>1 {print $1}' | grep -qx "$s"; then
        echo "  ✓ registered: $s"
        found=$((found+1))
      else
        echo "  · not registered: $s"
      fi
    done
    echo
    echo "─── Result ───"
    echo "  $found of ${#SECRETS[@]} target secrets are still on github.com."
    echo "  To delete them, run: $0 --apply"
    echo "  If gh lacks secrets scope, run: $0 --ui-fallback"
    exit 0
    ;;

  check)
    echo "─── Asserting absence of ${#SECRETS[@]} target secrets on $REPO ───"
    if ! gh secret list --repo "$REPO" >/tmp/list_out 2>/tmp/list_err; then
      err=$(cat /tmp/list_err)
      echo "❌ gh secret list failed: $err" >&2
      rm -f /tmp/list_out /tmp/list_err
      exit 3
    fi
    rm -f /tmp/list_err

    absent=0
    for s in "${SECRETS[@]}"; do
      if awk 'NR>1 {print $1}' /tmp/list_out | grep -qx "$s"; then
        echo "  ❌ still registered: $s"
        absent=$((absent+1))
      else
        echo "  ✓ absent: $s"
      fi
    done
    rm -f /tmp/list_out
    if [[ "$absent" -gt 0 ]]; then
      echo
      echo "─── Result: $absent of ${#SECRETS[@]} target secrets are still on github.com ───"
      echo "    Run: $0 --apply"
      exit 1
    else
      echo
      echo "─── Result: clean (all 3 are absent on $REPO) ───"
      exit 0
    fi
    ;;

  apply)
    echo "─── $0 --apply on repo $REPO ───"
    echo "    About to DELETE: ${SECRETS[*]}"
    echo

    # Step 1: read-only probe — confirm each secret exists before deleting.
    if ! gh secret list --repo "$REPO" >/tmp/list_out 2>/tmp/list_err; then
      err=$(cat /tmp/list_err)
      echo "❌ gh secret list failed: $err" >&2
      rm -f /tmp/list_out /tmp/list_err
      if grep -qi '403' /tmp/list_err 2>/dev/null; then
        echo "    PAT lacks secrets:write scope. Run: $0 --ui-fallback" >&2
      fi
      exit 3
    fi
    rm -f /tmp/list_err

    echo "─── Pre-delete presence check ───"
    missing=()
    for s in "${SECRETS[@]}"; do
      if awk 'NR>1 {print $1}' /tmp/list_out | grep -qx "$s"; then
        echo "  ✓ registered: $s"
      else
        echo "  · not registered (skip): $s"
        missing+=("$s")
      fi
    done

    if [[ ${#SECRETS[@]} -le ${#missing[@]} ]]; then
      echo
      echo "─── Refusing: all 3 secrets are absent; nothing to delete ───"
      rm -f /tmp/list_out
      exit 0
    fi

    if [[ ${#missing[@]} -gt 0 ]]; then
      echo
      echo "❌ Some secrets are missing on github.com (refusing partial delete): ${missing[*]}" >&2
      rm -f /tmp/list_out
      exit 4
    fi

    # Step 2: explicit operator confirmation (script is non-interactive
    # otherwise; the confirmation is the only barrier before deletion).
    echo
    read -rp "Confirm: delete ${SECRETS[*]} on $REPO? Type 'yes' to continue: " confirm
    if [[ "$confirm" != "yes" ]]; then
      echo "Aborted by operator. No secrets were modified." >&2
      rm -f /tmp/list_out
      exit 5
    fi

    # Step 3: delete each.
    echo
    echo "─── Deleting ───"
    failures=()
    for s in "${SECRETS[@]}"; do
      echo "  • $s ..."
      if gh secret delete "$s" --repo "$REPO" >/dev/null 2>/tmp/del_err; then
        echo "    ✓ deleted"
      else
        err=$(cat /tmp/del_err)
        echo "    ❌ failed: $err" >&2
        failures+=("$s")
      fi
    done
    rm -f /tmp/del_err /tmp/list_out

    # Step 4: re-list to verify.
    echo
    echo "─── Verify (post-delete re-list) ───"
    if ! gh secret list --repo "$REPO" 2>/dev/null | head -1; then
      echo "(verify list failed; the delete calls above decided the truth)"
    fi
    any_still_present=0
    listed=$(gh secret list --repo "$REPO" 2>/dev/null | awk 'NR>1 {print $1}')
    for s in "${SECRETS[@]}"; do
      if echo "$listed" | grep -qx "$s"; then
        echo "    ❌ still registered: $s"
        any_still_present=1
      fi
    done
    if [[ "$any_still_present" -eq 0 ]]; then
      echo "    ✓ none of the 3 secrets reappear"
    fi

    if [[ ${#failures[@]} -gt 0 ]]; then
      echo
      echo "❌ ${#failures[@]} deletion(s) failed: ${failures[*]}" >&2
      exit 5
    fi
    echo
    echo "─── Done: all 3 secrets deleted on $REPO ───"
    exit 0
    ;;
esac
