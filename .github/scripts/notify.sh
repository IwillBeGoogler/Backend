#!/usr/bin/env bash
# Telegram message for a CI or CD run: runs notify-ci.sh or notify-cd.sh next to it (shared helpers: notify-lib.sh).
# Env: MODE (ci | cd), the rest see the two scripts.
set -euo pipefail

case "${MODE:-}" in
  ci | cd) exec bash "$(dirname "$0")/notify-$MODE.sh" ;;
  *) echo "::error::mode must be ci or cd, got: ${MODE:-}"; exit 1 ;;
esac
