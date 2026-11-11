#!/usr/bin/env bash
# Merges the coverage profiles of the test shards into one and applies the coverage gate.
# Profiles are concatenated: `go tool cover` (and Sonar) add up identical blocks, so the total equals one single run.
# Args: the shard profiles. Env: COVERAGE_FILE (merged output) COVERAGE_MIN
set -euo pipefail

[ "$#" -gt 0 ] || { echo "::error::no coverage profiles to merge"; exit 1; }
out="${COVERAGE_FILE:-coverage.out}"
head -n 1 "$1" >"$out" # "mode: atomic"
for f in "$@"; do tail -n +2 "$f" >>"$out"; done
echo "merged $# profiles into $out"
bash "$(dirname "$0")/coverage-gate.sh"
