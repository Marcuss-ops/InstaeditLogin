#!/usr/bin/env bash
# Negative pattern guard: error classification must be typed, never textual.
#
# Contract (docs/error-classification.md): producers wrap typed sentinels;
# classifiers use errors.Is / errors.As. Message text is never consulted —
# provider-controlled strings must not be able to flip behavior, and codes
# derived from prose break the moment a message is reworded.
#
# This guard is a RATCHET, not a full remediation: the baseline below pins
# every string-matched error classifier that exists today. Any NEW occurrence
# (new line in an already-pinned file, or any occurrence in an unpinned file)
# fails `make verify`, so the typed classification stays the norm. Removing
# lines from the baseline is encouraged — that is the ratchet tightening.
#
# Tests are excluded (they may pin legacy string behavior to prove it is gone).
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$ROOT_DIR"

GUARD_NAME="error-classification guard"
fail() {
  printf '%s: %s\n' "$GUARD_NAME" "$*" >&2
  exit 1
}

PATTERN='strings\.(Contains|HasPrefix|HasSuffix)\([^)]*\.Error\(\)'

# Baseline: per-file pin of allowed (legacy) match counts, sorted by path.
# Format: "<path>:<count>". One entry per file with pre-existing matches.
BASELINE=$(cat <<'EOF'
cmd/yttest/main.go:3
internal/credentials/vault_lazy_reencrypt.go:1
internal/credentials/vault_lookup.go:1
internal/repository/thumbnail_project_domain_repo.go:2
pkg/api/accounts_write_handlers.go:7
pkg/api/drive_batch_helpers.go:1
pkg/api/posts_create.go:1
pkg/api/auth_oauth_callback.go:1
EOF
)

scan_stderr=$(mktemp)
trap 'rm -f "$scan_stderr"' EXIT

if raw="$(grep -RIonE "$PATTERN" \
    --include='*.go' \
    --exclude='*_test.go' \
    pkg internal cmd 2>"$scan_stderr")"; then
  :
elif [[ $? -eq 1 ]]; then
  raw=""
else
  cat "$scan_stderr" >&2
  fail "unable to scan runtime source"
fi
if [[ -s "$scan_stderr" ]]; then
  cat "$scan_stderr" >&2
  fail "unable to scan runtime source"
fi

# Group occurrences (NOT lines — a line may contain several classifiers)
# per file and compare against the baseline.
violations=""
new_baseline=""
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  file="${line%%:*}"
  count_for_file=$(printf '%s\n' "$raw" | grep -c "^${file//./\\.}:" || true)
  expected=$(printf '%s\n' "$BASELINE" | grep -F "${file}:" | head -1 | cut -d: -f2)
  if [[ -z "$expected" ]]; then
    violations+="unpinned file: ${file}:${line#*:}"$'\n'
  elif (( count_for_file > expected )); then
    violations+="count grew (${expected} → ${count_for_file}): ${file}"$'\n'
  fi
done <<< "$(printf '%s\n' "$raw" | cut -d: -f1 | sort -u)"

# Ratchet check: baseline entries whose file no longer matches at all (or
# matches fewer times than pinned) must be pruned. This keeps the baseline
# honest when classifiers are remediated.
while IFS= read -r entry; do
  [[ -z "$entry" ]] && continue
  file="${entry%:*}"
  expected="${entry##*:}"
  actual=0
  if [[ -n "$raw" ]]; then
    actual=$(printf '%s\n' "$raw" | grep -c "^${file//./\\.}:" || true)
  fi
  if (( actual < expected )); then
    violations+="baseline is stale (${expected} pinned, ${actual} present): ${file} — prune the entry"$'\n'
  fi
done <<< "$BASELINE"

if [[ -n "$violations" ]]; then
  printf '%s' "$violations" >&2
  fail "new string-matched error classifier found — wrap a typed sentinel and classify with errors.Is/errors.As (docs/error-classification.md)"
fi

echo "Error classification guard: PASS (typed-only; $(printf '%s\n' "$BASELINE" | grep -c ':') baseline entries pinned)"
