#!/usr/bin/env bash
#
# scripts/set-fly-secrets.sh — Idempotent, dry-run-by-default Fly.io secrets
# push for the `instaedit-login` app. Thin bash wrapper around
# scripts/_parse_envfile.py (the security-boundary parser) and
# `flyctl secrets set --app X --stage -` (the canonical --stage pipe).
#
# WHY A SEPARATE PYTHON MODULE (not a heredoc here):
# The parser is the security boundary of the deploy: a bug here could
# push a malformed value (panic at boot), a shell-expanded value
# (silently mutate a secret), or skip a disabled-provider check (push
# a banned secret). It is now a sibling file with regression coverage
# in scripts/test_parse_envfile.py. This bash script is just I/O glue.
#
# WHY `flyctl secrets set --stage -` (not `secrets import --stage`):
# `secrets import` does NOT support --stage (always triggers a deploy).
# The canonical alternative is `secrets set --stage -`, which reads
# KEY=VALUE from stdin and banks the secrets without a rolling restart.
# The next `fly deploy` attaches them to instances.
#
# WHY NO `set -a; source "$ENV_FILE"`:
# That approach interprets shell metacharacters in values. The python
# parser reads the file as bytes and emits literal KEY=VAL — no shell
# expansion of `$VAR`, `$(cmd)`, or backticks.
#
# ─── REDACTED PREVIEW WARNING ───────────────────────────────────────────
# This script prints a redacted preview table to stderr in BOTH modes
# (first 3 + last 3 chars + length per key). Do NOT redirect stderr to
# a file (`2>preview.log`): the preview persists on disk forever, and
# while redacted, it's PII-adjacent (e.g. `FRONTEND_URL` host).
#
# ─── USAGE ──────────────────────────────────────────────────────────────
#   ./scripts/set-fly-secrets.sh --env-file .env.production         # dry-run
#   ./scripts/set-fly-secrets.sh --env-file .env.production --apply # push
#
# After --apply, verify with:
#   make fly-secrets-verify
#   # or: ./scripts/verify-fly-secrets.sh
#
# Exit codes:
#   0  dry-run OK, or --apply succeeded
#   1  pre-flight failure (no flyctl, no python3, not authed, file missing)
#   2  bad CLI argument
#   3  validation failure (delegated to the python parser, propagated)
#   4  flyctl push failed

set -euo pipefail

APP_NAME="instaedit-login"

# Sibling-file pattern: resolve our own directory so the python module,
# the required-keys list, and the disabled-prefixes list are all
# locatable regardless of the cwd the operator runs from.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ─── Parse args ──────────────────────────────────────────────────────────
ENV_FILE=""
APPLY=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --env-file)
      ENV_FILE="$2"
      shift 2
      ;;
    --apply)
      APPLY=true
      shift
      ;;
    -h|--help)
      sed -n '2,40p' "$0"
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      echo "Run with --help for usage." >&2
      exit 2
      ;;
  esac
done

# ─── Pre-flight ──────────────────────────────────────────────────────────
command -v flyctl >/dev/null 2>&1 || {
  echo "❌ flyctl not installed. See https://fly.io/docs/hands-on/install-flyctl/" >&2
  exit 1
}
command -v python3 >/dev/null 2>&1 || {
  echo "❌ python3 not installed (required by _parse_envfile.py for safe .env parsing)." >&2
  exit 1
}
flyctl auth whoami >/dev/null 2>&1 || {
  echo "❌ Not authenticated with Fly.io. Run: flyctl auth login" >&2
  exit 1
}

if [[ -z "$ENV_FILE" ]]; then
  echo "❌ --env-file is required (e.g., --env-file .env.production)" >&2
  exit 1
fi
if [[ ! -f "$ENV_FILE" ]]; then
  echo "❌ Env file not found: $ENV_FILE" >&2
  exit 1
fi
# Verify the python parser + the shared lists are present (gives a
# clear error if the operator only checked out this file alone).
for sibling in "_parse_envfile.py" "required-fly-secrets.txt" "disabled-fly-secrets-prefixes.txt"; do
  if [[ ! -f "$SCRIPT_DIR/$sibling" ]]; then
    echo "❌ Missing sibling file: $SCRIPT_DIR/$sibling" >&2
    echo "   This script needs the full scripts/ directory from the repo." >&2
    exit 1
  fi
done

# ─── Dispatch ────────────────────────────────────────────────────────────
# Apply mode: python emits KEY=VAL on stdout → pipe to flyctl.
# Dry-run mode: python emits NOTHING on stdout (preview on stderr only);
# the `2>&1 >/dev/null` order matters: stderr first → terminal, then
# stdout → /dev/null. (The reverse `>/dev/null 2>&1` would also send
# stderr to /dev/null, which is NOT what we want — the operator must
# still see the preview.)
if [[ "$APPLY" == "true" ]]; then
  python3 "$SCRIPT_DIR/_parse_envfile.py" \
    "$ENV_FILE" "apply" "$APP_NAME" "$SCRIPT_DIR" \
    | flyctl secrets set --app "$APP_NAME" --stage -
  echo
  echo "✓ Secrets staged on $APP_NAME (no restart triggered)."
  echo "Next: make fly-secrets-verify && make fly-deploy"
else
  python3 "$SCRIPT_DIR/_parse_envfile.py" \
    "$ENV_FILE" "dry-run" "$APP_NAME" "$SCRIPT_DIR" 2>&1 >/dev/null
  echo "DRY-RUN: no secrets pushed. Re-run with --apply to push."
  echo
  echo "Verify after --apply: make fly-secrets-verify"
fi
