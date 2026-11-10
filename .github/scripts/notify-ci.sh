#!/usr/bin/env bash
# CI result to Telegram. A passing CI is sent silently, a failure makes a sound.
# Env: see notify-lib.sh, plus BRANCH ACTOR TITLE
#
#   ✅ CI passed
#
#   📦 Repo: Backend
#   🌿 Branch: feat/x
#   👤 By: actor
#   📝 Commit: the title
#
#   ✅ Lint
#   ❌ Test
#
#   [View run] [Commit] [Re-run]
set -euo pipefail
# shellcheck source=notify-lib.sh
. "$(dirname "$0")/notify-lib.sh"

failed=$(failed_jobs)
if [ -n "$failed" ]; then headline="❌ <b>CI failed</b> (${failed})"; else headline="✅ <b>CI passed</b>"; fi

text=$(cat <<MSG
${headline}

📦 Repo: <b>${REPO#*/}</b>
🌿 Branch: <code>$(html <<<"$BRANCH")</code>
👤 By: <b>$(html <<<"$ACTOR")</b>
📝 Commit: <i>$(head -n1 <<<"${TITLE:-}" | html)</i>

$(stage_lines)
$(failure_details)
MSG
)

send "$text" "$(buttons "$failed")" "$([ -z "$failed" ] && echo silent || true)"
