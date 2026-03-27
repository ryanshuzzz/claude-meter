#!/usr/bin/env bash
# claude-meter pre-flight usage check
# Returns 0 if headroom exists, 1 if blocked/near limit
# Output: one-line status suitable for agent consumption

set -euo pipefail

PORT="${CLAUDE_METER_PORT:-7735}"
STATUS_URL="http://127.0.0.1:${PORT}/status"

# If proxy isn't running, fail open
if ! response=$(curl -sf --max-time 2 "$STATUS_URL" 2>/dev/null); then
  echo "⚠️ claude-meter not reachable — proceeding without usage check"
  exit 0
fi

w5h_util=$(echo "$response" | jq -r '.windows["5h"].utilization // 0')
w5h_limit=$(echo "$response" | jq -r '.windows["5h"].limit // 0.25')
w7d_util=$(echo "$response" | jq -r '.windows["7d"].utilization // 0')
w7d_limit=$(echo "$response" | jq -r '.windows["7d"].limit // 0.25')
blocked=$(echo "$response" | jq -r '.blocked_requests_today // 0')

w5h_pct=$(echo "$response" | jq -r '.windows["5h"].pct_of_limit_used // 0' | cut -d. -f1)
w7d_pct=$(echo "$response" | jq -r '.windows["7d"].pct_of_limit_used // 0' | cut -d. -f1)

# Check if either window is at or above limit
w5h_blocked=$(awk "BEGIN {print ($w5h_util >= $w5h_limit) ? 1 : 0}")
w7d_blocked=$(awk "BEGIN {print ($w7d_util >= $w7d_limit) ? 1 : 0}")

if [ "$w5h_blocked" -eq 1 ] || [ "$w7d_blocked" -eq 1 ]; then
  w5h_reset=$(echo "$response" | jq -r '.windows["5h"].reset_at // "unknown"')
  w7d_reset=$(echo "$response" | jq -r '.windows["7d"].reset_at // "unknown"')
  echo "🚫 RATE LIMITED — 5h: ${w5h_pct}% used, 7d: ${w7d_pct}% used | Blocked today: ${blocked} | Resets: 5h=${w5h_reset}, 7d=${w7d_reset}"
  exit 1
fi

if [ "${w5h_pct:-0}" -ge 80 ] || [ "${w7d_pct:-0}" -ge 80 ]; then
  echo "⚠️ Usage high — 5h: ${w5h_pct}% of budget, 7d: ${w7d_pct}% of budget | Blocked today: ${blocked} — conserve tokens"
  exit 0
fi

echo "✅ Usage OK — 5h: ${w5h_pct}%, 7d: ${w7d_pct}% of budget | Blocked today: ${blocked}"
exit 0
