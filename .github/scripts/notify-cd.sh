#!/usr/bin/env bash
# CD result to Telegram. Missing bot settings only warn: a Telegram problem must never mark a good deploy as failed.
# Env: see notify-lib.sh, plus ENVIRONMENT TAG BRANCH ACTOR TITLE [HEALTH_URL]
#
#   🚀 Deployed to production
#
#   📦 Repo: Backend
#   🌿 Branch: main
#   🏷 Tag: abc1234
#   👤 By: actor
#   ⏱ Time: 4m 12s
#   📝 Commit: the title
#
#   ✅ Build
#   ✅ Deploy
#
#   🟢 healthz 200
#
#   [View run] [Commit] [Re-run on failure]
set -euo pipefail
# shellcheck source=notify-lib.sh
. "$(dirname "$0")/notify-lib.sh"

failed=$(failed_jobs)
if [ -n "$failed" ]; then headline="🔥 <b>Deploy to ${ENVIRONMENT} failed</b> (${failed})"; else headline="🚀 <b>Deployed to ${ENVIRONMENT}</b>"; fi

# healthz after a good deploy
health=""
if [ -z "$failed" ] && [ -n "${HEALTH_URL:-}" ]; then
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$HEALTH_URL" || true)
  if [ "$code" = 200 ]; then health="🟢 healthz 200"; else health="🔴 healthz ${code:-000}"; fi
fi

duration=$(run_duration)
tag_line=""; [ -z "${TAG:-}" ] || tag_line="🏷 Tag: <code>${TAG}</code>"
time_line=""; [ -z "$duration" ] || time_line="⏱ Time: ${duration}"
# optional lines are dropped when empty (grep) so the message has no holes
info=$(printf '%s\n' \
  "📦 Repo: <b>${REPO#*/}</b>" \
  "🌿 Branch: <code>$(html <<<"$BRANCH")</code>" \
  "$tag_line" \
  "👤 By: <b>$(html <<<"$ACTOR")</b>" \
  "$time_line" \
  "📝 Commit: <i>$(head -n1 <<<"${TITLE:-}" | html)</i>" | grep -v '^$')

text=$(cat <<MSG
${headline}

${info}

$(stage_lines)
${health:+
$health}$(failure_details)
MSG
)

send "$text" "$(buttons "$failed")"
