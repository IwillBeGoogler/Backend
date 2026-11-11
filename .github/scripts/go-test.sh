#!/usr/bin/env bash
# Verified module download, then go test with a coverage profile.
# Env: TEST_FLAGS (several flags, word-split on purpose) COVERAGE_FILE PACKAGES
#      SHARD SHARDS (optional): run only the packages of shard SHARD out of SHARDS (1-based, by position in `go list`);
#      without them every package runs and the coverage gate is applied here. With shards the gate runs once on the
#      merged profile (go-merge-coverage.sh).
set -euo pipefail

go mod download
go mod verify

if [ -n "${SHARDS:-}" ]; then
  pkgs=$(go list "$PACKAGES" | awk -v n="$SHARDS" -v i="$SHARD" '(NR - 1) % n == i - 1')
  echo "shard $SHARD/$SHARDS: $(wc -w <<<"$pkgs") packages"
  # shellcheck disable=SC2086 # TEST_FLAGS holds several flags, pkgs several packages
  go test $TEST_FLAGS -coverprofile="$COVERAGE_FILE" $pkgs
  exit 0
fi

# shellcheck disable=SC2086 # TEST_FLAGS holds several flags
go test $TEST_FLAGS -coverprofile="$COVERAGE_FILE" "$PACKAGES"
bash "$(dirname "$0")/coverage-gate.sh"
