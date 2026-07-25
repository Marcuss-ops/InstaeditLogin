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
#   1 = gh CLI not installed or not authenticated as Marcuss-ops, OR unknown flag
#   2 = repo detection failed (gh repo view errored) OR wrong owner
#   3 = `gh secret list` failed (PAT lacks scope; print UI fallback)
#   4 = RESERVED (was partial-delete refusal, superseded by soft-proceed in present[] mode)
#   5 = a `gh secret delete` call failed (network / 403 / 404), OR operator
#       typed something other than "yes" at the confirmation prompt
#   6 = --apply invoked without an interactive TTY (refuse + suggest --ui-fallback)
#
# CI / monitoring note: as of this revision, emit-codes are {0,1,2,3,5,6}.
# A probe previously wired to exit 4 (partial-delete refusal,
# superseded by soft-proceed) should be updated to wire exit 5.
# The current version does NOT emit exit 4 from any code path; reserved
# to keep the numbering stable for scripts relying on the prior layout.
#
# CAVEAT for exit 5: that code covers TWO semantically distinct
# outcomes and you may want to split them in your alerting:
#   - "deletion failure": one or more `gh secret delete` calls
#     failed mid-loop (network glitch / 403 mid-run / API outage) →
#     page-worthy. The failure-tagged stderr lines start with `❌`.
#   - "operator cancellation": the operator typed something other
#     than `yes` at the confirmation prompt → deliberate abort,
#     NOT a failure → NOT page-worthy. The cancellation stderr line
#     is exactly `Aborted by operator. No secrets were modified.`.
# A grep for the cancellation tell (`Aborted by operator`) on
# stderr is a reliable split-key if you want to keep alerting on
# (a) only, without smearing (b) into the failure channel.
set -euo pipefail

# Trap any exit path so temp files don't leak on shared hosts / CI
# containers. Single-source-of-truth: cleanup is centralized here; no
# per-branch `rm -f /tmp/list_out` calls are emitted in the script
# body.
trap 'rm -f /tmp/list_out /tmp/list_err /tmp/del_err 2>/dev/null || true' EXIT

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
      exit 3
    fi

    listed=$(gh secret list --repo "$REPO" 2>/dev/null | awk 'NR>1 {print $1}')

    echo
    echo "─── Named target secrets registered on $REPO ───"
    found=0
    for s in "${SECRETS[@]}"; do
      if echo "$listed" | grep -qx "$s"; then
        echo "  ✓ registered: $s"
        found=$((found+1))
      else
        echo "  · not registered: $s"
      fi
    done

    echo
    echo "─── Wildcard: any other Fly-tagged secrets (verify + add to list before --apply) ───"
    fly_other=$(echo "$listed" | grep -i fly || true)
    if [[ -z "$fly_other" ]]; then
      echo "  (none beyond the ${#SECRETS[@]} named targets)"
    else
      while IFS= read -r name; do
        echo "  ⚠ found (not in named targets): $name"
      done <<<"$fly_other"
    fi

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
      exit 3
    fi

    absent=0
    for s in "${SECRETS[@]}"; do
      if awk 'NR>1 {print $1}' /tmp/list_out | grep -qx "$s"; then
        echo "  ❌ still registered: $s"
        absent=$((absent+1))
      else
        echo "  ✓ absent: $s"
      fi
    done
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
      if grep -qi '403' /tmp/list_err 2>/dev/null; then
        echo "    PAT lacks secrets:write scope. Run: $0 --ui-fallback" >&2
      fi
      exit 3
    fi

    echo "─── Pre-delete presence check ───"
    missing=()
    present=()
    for s in "${SECRETS[@]}"; do
      if awk 'NR>1 {print $1}' /tmp/list_out | grep -qx "$s"; then
        echo "  ✓ registered: $s"
        present+=("$s")
      else
        echo "  · not registered (will skip): $s"
        missing+=("$s")
      fi
    done

    if [[ ${#present[@]} -eq 0 ]]; then
      echo
      echo "─── Nothing to delete: all ${#SECRETS[@]} target secrets already absent ───"
      exit 0
    fi

    if [[ ${#missing[@]} -gt 0 ]]; then
      echo
      echo "── Note: ${#missing[@]} of ${#SECRETS[@]} target secrets already absent (likely deleted in a prior partial run). ──"
      echo "── Proceeding to delete only the present names; no-refusal but logged for transparency. ──"
    fi

    # Step 2: explicit operator confirmation. Script is only safe to
    # proceed when stdin is a real TTY (otherwise `read -rp` would
    # deadlock indefinitely in CI / non-interactive SSH). Refuse with
    # exit 6 (no fallback to default-yes; the operator must rerun from
    # a real terminal).
    if [[ ! -t 0 ]]; then
      echo "❌ --apply requires an interactive terminal (stdin is not a TTY)." >&2
      echo "   Re-run from a real shell, or use --ui-fallback." >&2
      exit 6
    fi

    echo
    read -rp "Confirm: delete [${present[*]}] on $REPO? Type 'yes' to continue: " confirm
    if [[ "$confirm" != "yes" ]]; then
      echo "Aborted by operator. No secrets were modified." >&2
      exit 5
    fi

    # Step 3: delete only the names currently present (idempotent on
    # re-runs after partial-failure).
    echo
    echo "─── Deleting ───"
    failures=()
    for s in "${present[@]}"; do
      echo "  • $s ..."
      if gh secret delete "$s" --repo "$REPO" >/dev/null 2>/tmp/del_err; then
        echo "    ✓ deleted"
      else
        err=$(cat /tmp/del_err)
        echo "    ❌ failed: $err" >&2
        failures+=("$s")
      fi
    done

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
