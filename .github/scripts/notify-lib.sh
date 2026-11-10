#!/usr/bin/env bash
# Shared helpers of notify-ci.sh and notify-cd.sh (sourced, not run).
# A Telegram problem only warns: it never fails a PR or a deploy.
# Env used here: RESULTS BOT_TOKEN CHAT_ID [TOPIC_ID] REPO RUN_URL COMMIT_URL [GH_TOKEN RUN_ID] DRY_RUN
set -euo pipefail

html() { sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g'; }

RESULTS=${RESULTS:-'{}'}

# Names of the jobs that failed, "lint, test".
failed_jobs() { jq -r '[to_entries[] | select(.value.result == "failure") | .key] | join(", ")' <<<"$RESULTS"; }

# One line per stage (every job except "meta"): "✅ Lint".
stage_lines() {
  jq -r '
    to_entries[] | select(.key != "meta")
    | ({success: "✅", failure: "❌", cancelled: "⏹"}[.value.result] // "⏭") + " " + (.key | (.[:1] | ascii_upcase) + .[1:])
  ' <<<"$RESULTS"
}

# "4m 12s" since the run started; empty without GH_TOKEN/RUN_ID.
run_duration() {
  [ -n "${GH_TOKEN:-}" ] && [ -n "${RUN_ID:-}" ] || return 0
  local started secs
  started=$(gh api "repos/${REPO}/actions/runs/${RUN_ID}" --jq .run_started_at 2>/dev/null || true)
  [ -n "$started" ] || return 0
  secs=$(( $(date -u +%s) - $(date -u -d "$started" +%s) ))
  echo "$((secs / 60))m $((secs % 60))s"
}

# Failed job, failed step and the last error lines of its log (Telegram allows 4096 chars, we keep it short).
failure_details() {
  [ -n "${GH_TOKEN:-}" ] && [ -n "${RUN_ID:-}" ] || return 0
  local jobs id name step log out=""
  jobs=$(gh api "repos/${REPO}/actions/runs/${RUN_ID}/jobs" --jq '
    .jobs[] | select(.conclusion == "failure")
    | "\(.id)\t\(.name)\t\([.steps[] | select(.conclusion == "failure") | .name] | join(", "))"' 2>/dev/null || true)
  while IFS=$'\t' read -r id name step; do
    [ -n "$id" ] || continue
    log=$(gh api "repos/${REPO}/actions/jobs/${id}/logs" 2>/dev/null \
      | sed -E 's/^[0-9T:.-]+Z //' | grep -iE 'error|failed|FAIL' | grep -v '##\[' | tail -n 6 | cut -c1-160 || true)
    out+=$'\n'"🧯 <b>$(html <<<"$name")</b> → $(html <<<"$step")"
    [ -z "$log" ] || out+=$'\n'"<pre>$(html <<<"$log")</pre>"
  done <<<"$jobs"
  cut -c1-1800 <<<"$out"
}

# Buttons under the message: View run, Commit, and Re-run (the run page) when $1 (failed jobs) is not empty.
buttons() {
  jq -nc --arg run "$RUN_URL" --arg commit "${COMMIT_URL:-}" --arg failed "${1:-}" '
    {inline_keyboard: [
      [{text: "View run", url: $run}]
      + (if $commit != "" then [{text: "Commit", url: $commit}] else [] end)
      + (if $failed != "" then [{text: "Re-run", url: $run}] else [] end)
    ]}'
}

# send TEXT BUTTONS [silent]: prints instead with DRY_RUN=1, skips without bot settings.
send() {
  local text=$1 markup=$2 silent=${3:-}
  if [ "${DRY_RUN:-0}" = 1 ]; then printf '%s\n%s\n' "$text" "$markup"; return 0; fi
  if [ -z "${BOT_TOKEN:-}" ] || [ -z "${CHAT_ID:-}" ]; then echo "::warning::Telegram not configured"; return 0; fi
  local args=(-d chat_id="$CHAT_ID" -d parse_mode=HTML -d disable_web_page_preview=true)
  [ -z "${TOPIC_ID:-}" ] || args+=(-d message_thread_id="$TOPIC_ID")
  [ -z "$silent" ] || args+=(-d disable_notification=true)
  curl -fsS --retry 3 --retry-delay 2 --max-time 30 \
    "https://api.telegram.org/bot${BOT_TOKEN}/sendMessage" "${args[@]}" \
    --data-urlencode "text=${text}" --data-urlencode "reply_markup=${markup}" >/dev/null \
    || echo "::warning::Telegram sendMessage failed"
}
