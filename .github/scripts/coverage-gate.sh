#!/usr/bin/env bash
# Coverage gate: drop excluded files from the profile, fail below the minimum.
# Env: COVERAGE_FILE (default coverage.out) COVERAGE_MIN (default 95)
set -euo pipefail

file="${COVERAGE_FILE:-coverage.out}"
min="${COVERAGE_MIN:-95}"
filtered="$(mktemp)"
trap 'rm -f "$filtered"' EXIT

# Excluded (ERE, matched against profile lines "module/path/file.go:..."):
#  - _gen.go, .pb.go, mock_*.go : generated code, nothing hand-written to test
#  - /cmd/ : process entrypoints (flag parsing, signal wiring, os.Exit); the
#            testable logic lives in internal/ packages
exclude='(_gen\.go|\.pb\.go|/mock_[^/]*\.go|/cmd/[^:]*):'

grep -Ev "$exclude" "$file" > "$filtered"

total="$(go tool cover -func="$filtered" | awk '/^total:/ {gsub("%","",$3); print $3}')"
echo "total coverage (after exclusions): ${total}% (min ${min}%)"
awk -v t="$total" -v m="$min" 'BEGIN { exit (t + 0 >= m + 0) ? 0 : 1 }' || {
  echo "::error::coverage ${total}% is below ${min}%"
  exit 1
}
