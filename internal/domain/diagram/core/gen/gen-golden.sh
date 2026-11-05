#!/bin/sh
# Regenerates testdata/golden.json from a TypeScript core checkout (not part of this project).
#   TS_CORE_DIR=/path/to/core ./gen-golden.sh      (TSX=/path/to/tsx to override the runner; default: tsx from PATH)
set -e
here="$(cd "$(dirname "$0")" && pwd)"
: "${TS_CORE_DIR:?set TS_CORE_DIR to the TypeScript core folder (holds dsl/, exporters.ts, layout.ts, lint.ts, catalog.ts)}"
tsx="${TSX:-tsx}"
exec "$tsx" "$here/../testdata/gen-golden.ts"
