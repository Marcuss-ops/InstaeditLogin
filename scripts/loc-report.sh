#!/usr/bin/env bash
#
# scripts/loc-report.sh — Non-blocking source-file length report
#
# Scans tracked source files, reports any file whose line count is above
# the configured threshold, and writes a Markdown report. Always exits 0
# so it can be used in CI without blocking the build.
#
# Usage:
#   scripts/loc-report.sh [options]
#
# Options:
#   -t, --threshold <n>    Line count threshold (default: 800)
#   -e, --extensions       Comma-separated list of extensions (default: go,ts,tsx)
#   -o, --output <file>    Write Markdown report to file (default: stdout only)
#   -h, --help             Show this help text

set -euo pipefail

# Defaults
THRESHOLD=800
EXTENSIONS="go,ts,tsx"
OUTPUT=""

usage() {
  sed -n '2,12p' "$0" | sed 's/^# //; s/^#//'
  exit 0
}

# Parse arguments
while [[ $# -gt 0 ]]; do
  case "$1" in
    -t|--threshold)
      THRESHOLD="$2"
      shift 2
      ;;
    -e|--extensions)
      EXTENSIONS="$2"
      shift 2
      ;;
    -o|--output)
      OUTPUT="$2"
      shift 2
      ;;
    -h|--help)
      usage
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage
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
  echo "⚠️  Not inside a git repository; falling back to current directory." >&2
  REPO_ROOT="."
fi
cd "$REPO_ROOT"

# Collect files and counts
REPORT=""
OVER_COUNT=0
TOTAL_COUNT=0

while IFS= read -r file; do
  [[ -z "$file" ]] && continue
  if [[ ! -f "$file" ]]; then
    continue
  fi
  TOTAL_COUNT=$((TOTAL_COUNT + 1))
  lines=$(wc -l < "$file")
  if [[ "$lines" -gt "$THRESHOLD" ]]; then
    REPORT+="| $file | $lines |\n"
    OVER_COUNT=$((OVER_COUNT + 1))
  fi
done < <(git ls-files | grep -E "$EXT_REGEX" | sort)

# Compose Markdown report
cat <<EOF
# Source File Length Report

- Threshold: **$THRESHOLD lines**
- Extensions: **$EXTENSIONS**
- Files above threshold: **$OVER_COUNT**

| File | Lines |
|------|-------|
EOF

if [[ "$OVER_COUNT" -gt 0 ]]; then
  printf '%b\n' "$REPORT"
else
  echo "_No files exceed the threshold. Great job!_"
fi

if [[ -n "$OUTPUT" ]]; then
  {
    cat <<EOF
# Source File Length Report

- Threshold: **$THRESHOLD lines**
- Extensions: **$EXTENSIONS**
- Files above threshold: **$OVER_COUNT**

| File | Lines |
|------|-------|
EOF
    if [[ "$OVER_COUNT" -gt 0 ]]; then
      printf '%b\n' "$REPORT"
    else
      echo "_No files exceed the threshold. Great job!_"
    fi
  } > "$OUTPUT"
  echo "Report written to: $OUTPUT" >&2
fi

# Always exit 0 — this is an informational, non-blocking check.
exit 0
