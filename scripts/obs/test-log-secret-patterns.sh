#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=log-secret-patterns.sh
source "$SCRIPT_DIR/log-secret-patterns.sh"

TMP_DIR="$(mktemp -d -t log-secret-patterns-test-XXXXXX)"
trap 'rm -rf "$TMP_DIR"' EXIT
BAD_LOG="$TMP_DIR/bad.log"
CLEAN_LOG="$TMP_DIR/clean.log"

cat >"$BAD_LOG" <<'EOF'
access_token=0123456789abcdef0123456789
re_abcdefghijklmnopqrstuvwxyz
AKIA1234567890ABCDEF
postgresql://user:secretpw@db.example.test/app
password=secret123
https://app.example.test/callback?csrf_token=0123456789abcdef0123456789abcdef
https://app.example.test/callback?token=abcdefghijklmnopqrstuvwxyz
synthetic ya29.ACCESS_TOKEN_SAMPLE_123456789
synthetic 1//REFRESH_TOKEN_SAMPLE_123456789
synthetic Authorization: Bearer bearer_sample_123456789
EOF

# Every canonical pattern must detect a matching synthetic value, while
# the regression fixture remains local and is never printed.
printf '%s\n' 'normal request completed' >"$CLEAN_LOG"

for i in "${!PATTERNS[@]}"; do
  pattern="${PATTERNS[$i]}"
  if ! grep -aPq "$pattern" "$BAD_LOG"; then
    printf 'pattern %s did not detect its synthetic secret\n' "${PATTERN_NAMES[$i]}" >&2
    exit 1
  fi
  if grep -aPq "$pattern" "$CLEAN_LOG"; then
    printf 'pattern %s matched a clean log\n' "${PATTERN_NAMES[$i]}" >&2
    exit 1
  fi
done

# The test output itself must never contain the synthetic secret material.
if grep -aEq 'ya29\.|1//|Bearer' "$CLEAN_LOG"; then
  echo 'clean fixture unexpectedly contains a credential pattern' >&2
  exit 1
fi

echo 'log secret pattern regression checks passed (matches were not printed)'
