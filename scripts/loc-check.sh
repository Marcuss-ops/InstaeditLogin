#!/usr/bin/env bash
#
# scripts/loc-check.sh — Blocking source-file size regression gate
#
# Fails when a tracked source file GROWS past the configured threshold
# relative to a base ref (default: origin/main). This is deliberately
# a DIFF-based gate: files that already exceed the threshold are listed
# but do not fail the check, so pre-existing large files don't block CI
# while any new size regression (a file crossing the threshold for the
# first time, or growing while already over it) hard-fails. Run with
# --against none for a strict full-tree check.
#
# Usage:
#   scripts/loc-check.sh [options]
#
# Options:
#   -t, --threshold <n>    Line count threshold (default: 800)
#   -a, --against <ref>    Base ref to diff against (default: origin/main).
#                          Use 'none' for a strict full-tree check.
#   -e, --extensions       Comma-separated list of extensions (default: go,ts,tsx)
#   -h, --help             Show this help text
#
# Exit codes:
#   0  no new size regression
#   1  at least one file grew past the threshold (or strict mode found
#      files above the threshold)
#   2  usage error

set -euo pipefail

# Defaults
THRESHOLD=800
AGAINST="origin/main"
EXTENSIONS="go,ts,tsx"

usage() {
  sed -n '2,27p' "$0" | sed 's/^# //; s/^#//'
  exit 0
}

# Parse arguments
while [[ $# -gt 0 ]]; do
  case "$1" in
    -t|--threshold)
      THRESHOLD="$2"
      shift 2
      ;;
    -a|--against)
      AGAINST="$2"
      shift 2
      ;;
    -e|--extensions)
      EXTENSIONS="$2"
      shift 2
      ;;
    -h|--help)
      usage
      ;;
    *)
      echo "Unknown option: $1" >&2
      exit 2
      ;;
  esac
done

# Build extension regex, e.g. \.go$|\.ts$|\.tsx$
EXT_REGEX=""
IFS=',' read -ra EXT_ARRAY <<< "$EXTENSIONS"
for ext in "${EXT_ARRAY[@]}"; do
  ext=${ext#"${ext%%[![:space:]]*}"}
  ext=${ext%"${ext##*[![:space:]]}"}
  [[ -z "$ext" ]] && continue
  EXT_REGEX+="\\.${ext}\$|"
done
EXT_REGEX="${EXT_REGEX%|}"

# Detect git root so the script can be invoked from anywhere
REPO_ROOT=""
if git rev-parse --show-toplevel >/dev/null 2>&1; then
  REPO_ROOT="$(git rev-parse --show-toplevel)"
else
  echo "❌ Not inside a git repository." >&2
  exit 2
fi
cd "$REPO_ROOT"

# Strict mode: check every tracked file against the threshold.
if [[ "$AGAINST" == "none" ]]; then
  REGRESSED=""
  OVER_COUNT=0
  while IFS= read -r file; do
    [[ -z "$file" ]] && continue
    [[ -f "$file" ]] || continue
    lines=$(wc -l < "$file")
    if [[ "$lines" -gt "$THRESHOLD" ]]; then
      REGRESSED+="| $file | $lines |\n"
      OVER_COUNT=$((OVER_COUNT + 1))
    fi
  done < <(git ls-files | grep -E "$EXT_REGEX" | sort)

  echo "# Source File Size Gate (strict — all tracked files)"
  echo
  echo "- Threshold: **$THRESHOLD lines**"
  echo "- Mode: strict full-tree check (--against none)"
  echo "- Files above threshold: **$OVER_COUNT**"
  echo
  echo "| File | Lines |"
  echo "|------|-------|"
  if [[ "$OVER_COUNT" -gt 0 ]]; then
    printf '%b\n' "$REGRESSED"
    echo
    echo "❌ $OVER_COUNT file(s) above $THRESHOLD lines. Refactor them below the threshold."
    exit 1
  fi
  echo "_All files are under the threshold. Great job!_"
  exit 0
fi

# Diff mode: compare the working tree against the base ref. A file
# "regresses" when it grows past the threshold while the base version
# was at or below it, or when it grows while already above it.
if ! git rev-parse --verify --quiet "$AGAINST" >/dev/null; then
  echo "❌ Base ref '$AGAINST' not found. Use '--against none' for a strict check or pass an existing ref." >&2
  exit 2
fi

REGRESSED=""
REGRESSED_COUNT=0
OVER_COUNT=0
TOTAL_SCANNED=0

while IFS= read -r file; do
  [[ -z "$file" ]] && continue
  [[ -f "$file" ]] || continue
  TOTAL_SCANNED=$((TOTAL_SCANNED + 1))

  current=$(wc -l < "$file")

  # Count the base version's lines (0 if the file is new).
  base=0
  if git cat-file -e "$AGAINST:$file" 2>/dev/null; then
    base=$(git show "$AGAINST:$file" | wc -l)
  fi

  # Track how many are over the threshold (informational in diff mode).
  if [[ "$current" -gt "$THRESHOLD" ]]; then
    OVER_COUNT=$((OVER_COUNT + 1))
  fi

  # Regression: crossed the threshold, or grew while already over it.
  if [[ "$current" -gt "$THRESHOLD" ]] && [[ "$current" -gt "$base" ]]; then
    REGRESSED+="| $file | $base → $current |\n"
    REGRESSED_COUNT=$((REGRESSED_COUNT + 1))
  fi
done < <(git diff --name-only "$AGAINST" | grep -E "$EXT_REGEX" | sort -u)

echo "# Source File Size Gate (diff vs $AGAINST)"
echo
echo "- Threshold: **$THRESHOLD lines**"
echo "- Base ref: **$AGAINST**"
echo "- Changed files scanned: **$TOTAL_SCANNED**"
echo "- Files over threshold (pre-existing + new): **$OVER_COUNT**"
echo "- New size regressions: **$REGRESSED_COUNT**"
echo
echo "| File | Base → Current lines |"
echo "|------|----------------------|"
if [[ "$REGRESSED_COUNT" -gt 0 ]]; then
  printf '%b\n' "$REGRESSED"
  echo
  echo "❌ $REGRESSED_COUNT file(s) grew past the $THRESHOLD-line threshold. Refactor or revert."
  exit 1
fi
echo "_No new size regressions. Great job!_"

# Informational note when pre-existing files are over the threshold but
# did not grow (kept for context without failing the gate).
if [[ "$OVER_COUNT" -gt 0 ]]; then
  echo
  echo "ℹ️  $OVER_COUNT file(s) are already above the threshold but did NOT grow in this diff — not blocking. See scripts/loc-report.sh for the full backlog."
fi

exit 0
