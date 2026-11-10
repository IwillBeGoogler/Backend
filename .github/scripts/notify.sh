#!/usr/bin/env bash
# One Telegram message per CI or CD run. A Telegram problem only warns: it never fails a PR or a deploy.
#
# Env:  BOT_TOKEN CHAT_ID [TOPIC_ID]      Telegram bot settings (missing = skip with a warning)
#       MODE                              ci | cd
#       RESULTS                           caller's toJSON(needs); every job except "meta" is shown as a stage
#       ENVIRONMENT [TAG]                 shown for cd
#       REPO BRANCH ACTOR TITLE           what ran and who started it
#       RUN_URL COMMIT_URL                buttons under the message
#       GH_TOKEN RUN_ID                   optional: duration and the log of failed jobs (needs actions: read)
#       HEALTH_URL                        optional, cd only: checked after a successful deploy
#       DRY_RUN=1                         print the message instead of sending it
#
# Message:
#   ✅ Deployed to production · abc1234 · 4m 12s
#   fix: the commit title
#
#   Backend · main · by actor
#   Build ✅  Scan ✅  Push ✅  Deploy ✅
#   🟢 healthz 200
#
#   [View run] [Commit]            (+ [Re-run] when something failed; a failure also adds the error lines)
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

# --- duration (run start -> now) ----------------------------------------------
duration=""
if [ -n "${GH_TOKEN:-}" ] && [ -n "${RUN_ID:-}" ]; then
  started=$(gh api "repos/${REPO}/actions/runs/${RUN_ID}" --jq .run_started_at 2>/dev/null || true)
  if [ -n "$started" ]; then
    secs=$(( $(date -u +%s) - $(date -u -d "$started" +%s) ))
    duration=" · $((secs / 60))m $((secs % 60))s"
  fi
fi

# --- headline ---------------------------------------------------------------
if [ "$MODE" = cd ]; then
  what="Deploy to ${ENVIRONMENT}"
  done_text="Deployed to ${ENVIRONMENT}"
else
  what="CI"
  done_text="CI passed"
fi
tag=""
[ -z "${TAG:-}" ] || tag=" · <code>${TAG}</code>"

if [ -n "$failed" ]; then
  headline="❌ <b>${what} failed</b> (${failed})${tag}${duration}"
else
  headline="✅ <b>${done_text}</b>${tag}${duration}"
fi

# --- why it failed: failed job, failed step, last error lines of its log -------
errors=""
if [ -n "$failed" ] && [ -n "${GH_TOKEN:-}" ] && [ -n "${RUN_ID:-}" ]; then
  jobs=$(gh api "repos/${REPO}/actions/runs/${RUN_ID}/jobs" --jq '
    .jobs[] | select(.conclusion == "failure")
    | "\(.id)\t\(.name)\t\([.steps[] | select(.conclusion == "failure") | .name] | join(", "))"' 2>/dev/null || true)
  while IFS=$'\t' read -r id name step; do
    [ -n "$id" ] || continue
    log=$(gh api "repos/${REPO}/actions/jobs/${id}/logs" 2>/dev/null \
      | sed -E 's/^[0-9T:.-]+Z //' | grep -iE 'error|failed|FAIL' | grep -v '##\[' | tail -n 6 | cut -c1-160 || true)
    errors+=$'\n\n'"<b>$(html <<<"$name")</b> → $(html <<<"$step")"
    [ -z "$log" ] || errors+=$'\n'"<pre>$(html <<<"$log")</pre>"
  done <<<"$jobs"
  errors=$(cut -c1-1800 <<<"$errors") # Telegram limit: 4096 chars per message
fi

# --- health check after a good deploy -----------------------------------------
health=""
if [ "$MODE" = cd ] && [ -z "$failed" ] && [ -n "${HEALTH_URL:-}" ]; then
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$HEALTH_URL" || true)
  if [ "$code" = 200 ]; then health=$'\n'"🟢 healthz 200"; else health=$'\n'"🔴 healthz ${code:-000}"; fi
fi

# --- message ----------------------------------------------------------------
repo_name=${REPO#*/}
title=$(head -n1 <<<"${TITLE:-}" | html)

text=$(cat <<MSG
${headline}
${title}

${repo_name^} · <code>$(html <<<"$BRANCH")</code> · by $(html <<<"$ACTOR")
${stages}${health}${errors}
MSG
)

# Buttons: View run, Commit, and Re-run (the run page) when something failed.
markup=$(jq -nc --arg run "${RUN_URL}" --arg commit "${COMMIT_URL:-}" --arg failed "$failed" '
  {inline_keyboard: [
    [{text: "View run", url: $run}]
    + (if $commit != "" then [{text: "Commit", url: $commit}] else [] end)
    + (if $failed != "" then [{text: "Re-run", url: $run}] else [] end)
  ]}')

if [ "${DRY_RUN:-0}" = 1 ]; then
  printf '%s\n%s\n' "$text" "$markup"
  exit 0
fi

# --- send -------------------------------------------------------------------
if [ -z "${BOT_TOKEN:-}" ] || [ -z "${CHAT_ID:-}" ]; then
  echo "::warning::Telegram not configured"
  exit 0
fi

args=(-d chat_id="$CHAT_ID" -d parse_mode=HTML -d disable_web_page_preview=true)
[ -z "${TOPIC_ID:-}" ] || args+=(-d message_thread_id="$TOPIC_ID")
# A passing CI is silent; failures and deploys make a sound.
if [ "$MODE" = ci ] && [ -z "$failed" ]; then args+=(-d disable_notification=true); fi

curl -fsS --retry 3 --retry-delay 2 --max-time 30 \
  "https://api.telegram.org/bot${BOT_TOKEN}/sendMessage" "${args[@]}" \
  --data-urlencode "text=${text}" --data-urlencode "reply_markup=${markup}" >/dev/null \
  || echo "::warning::Telegram sendMessage failed"
