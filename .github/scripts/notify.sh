#!/usr/bin/env bash
# One Telegram message per CI or CD run. A Telegram problem only warns: it never fails a PR or a deploy.
#
# Env:  BOT_TOKEN CHAT_ID [TOPIC_ID]      Telegram bot settings (missing = skip with a warning)
#       MODE                              ci | cd
#       RESULTS                           caller's toJSON(needs); every job except "meta" is shown as a stage
#       ENVIRONMENT [TAG]                 shown for cd
#       REPO BRANCH ACTOR TITLE RUN_URL   what ran and who started it
#       DRY_RUN=1                         print the message instead of sending it
#
# Message:
#   ✅ Deployed to production · abc1234
#   fix: the commit title
#
#   Backend · main · by actor
#   Build ✅  Scan ✅  Push ✅  Deploy ✅
#
#   View run
set -euo pipefail

html() { sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g'; }

# --- stages -----------------------------------------------------------------
RESULTS=${RESULTS:-'{}'}

# "Build ✅  Scan ❌  Deploy ⏭"
stages=$(jq -r '
  to_entries[] | select(.key != "meta")
  | (.key | (.[:1] | ascii_upcase) + .[1:]) + " " + ({success: "✅", failure: "❌", cancelled: "⏹"}[.value.result] // "⏭")
' <<<"$RESULTS" | paste -sd '|' - | sed 's/|/  /g')

failed=$(jq -r '[to_entries[] | select(.value.result == "failure") | .key] | join(", ")' <<<"$RESULTS")

# --- headline ---------------------------------------------------------------
if [ "$MODE" = cd ]; then
  what="Deploy to ${ENVIRONMENT}"
  done_text="Deployed to ${ENVIRONMENT}"
else
  what="CI"
  done_text="CI passed"
fi
suffix=""
[ -z "${TAG:-}" ] || suffix=" · <code>${TAG}</code>"

if [ -n "$failed" ]; then
  headline="❌ <b>${what} failed</b> (${failed})${suffix}"
else
  headline="✅ <b>${done_text}</b>${suffix}"
fi

# --- message ----------------------------------------------------------------
repo_name=${REPO#*/}
title=$(head -n1 <<<"${TITLE:-}" | html)

text=$(cat <<MSG
${headline}
${title}

${repo_name^} · <code>$(html <<<"$BRANCH")</code> · by $(html <<<"$ACTOR")
${stages}

<a href="${RUN_URL}">View run</a>
MSG
)

if [ "${DRY_RUN:-0}" = 1 ]; then
  printf '%s\n' "$text"
  exit 0
fi

# --- send -------------------------------------------------------------------
if [ -z "${BOT_TOKEN:-}" ] || [ -z "${CHAT_ID:-}" ]; then
  echo "::warning::Telegram not configured"
  exit 0
fi

args=(-d chat_id="$CHAT_ID" -d parse_mode=HTML -d disable_web_page_preview=true)
[ -z "${TOPIC_ID:-}" ] || args+=(-d message_thread_id="$TOPIC_ID")

curl -fsS --retry 3 --retry-delay 2 --max-time 30 \
  "https://api.telegram.org/bot${BOT_TOKEN}/sendMessage" "${args[@]}" --data-urlencode "text=${text}" >/dev/null \
  || echo "::warning::Telegram sendMessage failed"
