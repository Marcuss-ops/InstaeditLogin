#!/usr/bin/env bash
# Verify that InstaEdit never reintroduces a Velox-owned Groups/Channels catalog.
#
# This is a static, read-only architecture guard. Groups, channels, account
# membership, and publishing metadata belong to InstaEdit; the only allowed
# cross-system relationship is the project-scoped editor bridge.
#
# Runtime source is scanned while tests, documentation, and this guard itself
# are excluded. Tests and docs may mention the forbidden names to pin or
# describe the invariant without becoming production behavior.
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$ROOT_DIR"

fail() {
  printf 'Velox catalog boundary check: %s\n' "$*" >&2
  exit 1
}

for root in pkg internal cmd web/src scripts; do
  [[ -d "$root" ]] || fail "required source root is missing: $root"
done

# These symbols are the negative requirements from the data-ownership
# decision. Singular/plural spellings are included so a trivial rename cannot
# bypass the guard. The companion veloxcontract scope/interface tests protect
# the allowed project/render contract at the type level.
FORBIDDEN='SyncGroupsToVelox|SyncGroupToVelox|SyncChannelsFromVelox|SyncChannelFromVelox|MirrorGroupMemberships|MirrorGroupMembership|MirrorGroupsToVelox|MirrorChannelsToVelox'

# Run grep through a checked temporary stderr file. grep exits 1 for "no
# matches" (success for this negative check), but any other status is a real
# scan failure and must fail closed instead of being swallowed.
scan_stderr=$(mktemp)
find_output=""
cleanup() {
  rm -f "$scan_stderr"
  [[ -z "$find_output" ]] || rm -f "$find_output"
}
trap cleanup EXIT
if violations="$(
  grep -RInE "$FORBIDDEN" \
    --include='*.go' \
    --include='*.ts' \
    --include='*.tsx' \
    --include='*.js' \
    --include='*.mjs' \
    --include='*.sh' \
    --exclude='*_test.go' \
    --exclude='*.test.ts' \
    --exclude='*.test.tsx' \
    --exclude='*.spec.ts' \
    --exclude='*.spec.tsx' \
    --exclude-dir='__tests__' \
    --exclude='verify-no-velox-catalog-sync.sh' \
    pkg internal cmd web/src scripts 2>"$scan_stderr"
)"; then
  :
elif [[ "$?" -eq 1 ]]; then
  violations=""
else
  cat "$scan_stderr" >&2
  fail "unable to scan runtime source"
fi

if [[ -s "$scan_stderr" ]]; then
  cat "$scan_stderr" >&2
  fail "unable to scan runtime source"
fi

if [[ -n "$violations" ]]; then
  printf '%s\n' "$violations" >&2
  fail "forbidden Groups/Channels synchronization symbol found in runtime source"
fi

# A Velox control client must not be introduced into the canonical Groups or
# Channels handlers. This path-level check is deliberately narrow: editor
# launch/session code may legitimately use Velox, while catalog handlers may
# only use InstaEdit repositories and services.
find_output=$(mktemp)
if ! find pkg/api internal web/src -type f \( \
    -iname '*group*.go' -o -iname '*channel*.go' -o \
    -iname '*group*.ts' -o -iname '*group*.tsx' -o \
    -iname '*channel*.ts' -o -iname '*channel*.tsx' \
  \) \
  ! -path '*/__tests__/*' \
  ! -name '*_test.go' \
  ! -name '*.test.ts' \
  ! -name '*.test.tsx' \
  ! -name '*.spec.ts' \
  ! -name '*.spec.tsx' \
  ! -path '*/__tests__/*' \
  -print0 >"$find_output" 2>"$scan_stderr"; then
  cat "$scan_stderr" >&2
  fail "unable to enumerate Groups/Channels source files"
fi
if [[ -s "$scan_stderr" ]]; then
  cat "$scan_stderr" >&2
  fail "unable to enumerate Groups/Channels source files"
fi

mapfile -d '' catalog_files <"$find_output"
if ((${#catalog_files[@]} > 0)); then
  if client_violations="$(
    grep -nE 'VELOX_CONTROL_URL|VELOX_CONTROL_JWT_SECRET|VELOX_API_TOKEN|veloxclient|VeloxBFF|/api/v1/velox/|integrations/velox' \
      --exclude='*.test.ts' \
      --exclude='*.test.tsx' \
      --exclude='*.spec.ts' \
      --exclude='*.spec.tsx' \
      "${catalog_files[@]}" 2>"$scan_stderr"
  )"; then
    :
  elif [[ "$?" -eq 1 ]]; then
    client_violations=""
  else
    cat "$scan_stderr" >&2
    fail "unable to inspect Groups/Channels source files"
  fi
  if [[ -s "$scan_stderr" ]]; then
    cat "$scan_stderr" >&2
    fail "unable to inspect Groups/Channels source files"
  fi
  if [[ -n "$client_violations" ]]; then
    printf '%s\n' "$client_violations" >&2
    fail "Velox client/control reference found in Groups/Channels runtime source"
  fi
fi

echo "Velox catalog boundary check: PASS (no Groups/Channels sync or catalog client references)"
