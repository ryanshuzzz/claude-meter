# claude-meter Rate Limiter — One-Shot Deployment

Run this on each OpenClaw machine. Copy-paste the whole thing.

## Full Setup (one-shot)

```bash
#!/usr/bin/env bash
set -euo pipefail

# --- Prerequisites check ---
for cmd in go jq curl; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "❌ Missing required tool: $cmd"; exit 1; }
done

CURRENT_USER="$(whoami)"

echo "=== 1. Clone & build ==="
cd ~/.openclaw/workspace
if [ -d "claude-meter" ]; then
  cd claude-meter
  git fetch origin && git checkout senor-dev && git pull origin senor-dev
else
  git clone git@github.com:ryanshuzzz/claude-meter.git
  cd claude-meter
  git checkout senor-dev
fi
go build -o claude-meter ./cmd/claude-meter
sudo cp claude-meter /usr/local/bin/claude-meter
echo " ✅ Built and installed"

echo "=== 2. Create systemd service ==="
sudo tee /etc/systemd/system/claude-meter.service > /dev/null << UNIT
[Unit]
Description=claude-meter rate-limited proxy
After=network.target

[Service]
Type=simple
User=${CURRENT_USER}
ExecStart=/usr/local/bin/claude-meter start --plan-tier max_20x --instance-share 0.25
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
UNIT

sudo systemctl daemon-reload
sudo systemctl enable --now claude-meter
sleep 2
echo " ✅ systemd service created and started"

echo "=== 3. Verify proxy ==="
curl -sf http://127.0.0.1:7735/health && echo " ✅ Proxy running" || { echo " ❌ Proxy failed to start"; sudo journalctl -u claude-meter --no-pager -n 10; exit 1; }

echo "=== 4. Wire OpenClaw to proxy ==="
OCJSON="$HOME/.openclaw/openclaw.json"
cp "$OCJSON" "$OCJSON.pre-claude-meter"
# Safely merge baseUrl into existing config without clobbering anything
jq '
  .models //= {} |
  .models.mode = "merge" |
  .models.providers //= {} |
  .models.providers.anthropic //= {} |
  .models.providers.anthropic.baseUrl = "http://127.0.0.1:7735" |
  .models.providers.anthropic.models //= []
' "$OCJSON.pre-claude-meter" > "$OCJSON"
echo " ✅ OpenClaw pointed at claude-meter proxy"

echo "=== 5. Patch AGENTS.md ==="
AGENTS="$HOME/.openclaw/workspace/AGENTS.md"
if ! grep -q "claude-meter" "$AGENTS" 2>/dev/null; then
  cat >> "$AGENTS" << 'PATCH'

## 📊 Rate Limiter (claude-meter)

This instance runs behind a claude-meter proxy that enforces a 25% share of the shared Anthropic account budget.

**Pre-flight check (MANDATORY):** Run `bash claude-meter/scripts/check-usage.sh` BEFORE processing EVERY user message. This is not optional — treat it like reading the message itself. Do it first, every single time.

**Footer (MANDATORY):** Append the output of the usage check as the LAST LINE of EVERY response. Format: just paste the raw output line from the script. This gives the user visibility into account utilization at all times.

- **✅ Usage OK** → proceed normally
- **⚠️ Usage high (>80%)** → conserve tokens: skip non-essential work, use shorter prompts, avoid spawning multiple sub-agents, prefer cached context
- **🚫 RATE LIMITED** → STOP. Tell the user you're rate limited, show the reset time, and suggest waiting. Do NOT retry in a loop.

**On "usage" or "check usage" requests:** Run `curl -s http://127.0.0.1:7735/status | jq .` and present the full status in a readable format — both windows, headroom, reset times, and blocked count.

**Be token-conscious always:** You share this account with 3 other instances. Prefer cache-friendly prompts, batch work, and avoid unnecessary retries.
PATCH
  echo " ✅ AGENTS.md patched"
else
  echo " ⏭️  AGENTS.md already has claude-meter section"
fi

echo "=== 6. Patch HEARTBEAT.md ==="
HEARTBEAT="$HOME/.openclaw/workspace/HEARTBEAT.md"
if ! grep -q "claude-meter" "$HEARTBEAT" 2>/dev/null; then
  cat >> "$HEARTBEAT" << 'PATCH'

## claude-meter Health
- Run `systemctl is-active claude-meter` — if not active, run `sudo systemctl restart claude-meter` and notify the user
PATCH
  echo " ✅ HEARTBEAT.md patched"
else
  echo " ⏭️  HEARTBEAT.md already has claude-meter section"
fi

echo "=== 7. Restart OpenClaw ==="
openclaw gateway restart &
sleep 5

echo ""
echo "=========================================="
echo " claude-meter deployed successfully! 🎉"
echo "=========================================="
echo ""
echo "  Proxy:    http://127.0.0.1:7735"
echo "  Status:   claude-meter status"
echo "  Hook:     bash claude-meter/scripts/check-usage.sh"
echo ""
bash ~/.openclaw/workspace/claude-meter/scripts/check-usage.sh
```

## What this does

1. Checks prerequisites (`go`, `jq`, `curl`)
2. Clones claude-meter repo (or updates if already there)
3. Builds the Go binary and installs to `/usr/local/bin/`
4. Creates systemd service with current user (auto-start on boot, auto-restart on crash)
5. Points OpenClaw's Anthropic provider at the local proxy (`http://127.0.0.1:7735`)
6. Patches AGENTS.md with mandatory per-message usage check + footer
7. Patches HEARTBEAT.md with health monitoring
8. Restarts OpenClaw to pick up the new config

## Manual Usage Check

```bash
# Agent hook (one-liner, for every message)
bash claude-meter/scripts/check-usage.sh

# Full JSON
curl -s http://127.0.0.1:7735/status | jq .

# Pretty CLI
claude-meter status
```

## Adjusting Shares

Edit the systemd service ExecStart line:
- 2 instances: `--instance-share 0.50`
- 3 instances: `--instance-share 0.33`
- 4 instances: `--instance-share 0.25` (default)

```bash
sudo systemctl edit claude-meter
# Override ExecStart with new --instance-share value
sudo systemctl restart claude-meter
```

## How It Works

1. **Before each request:** Proxy checks last-known utilization from Anthropic headers. If ≥ 25%, returns HTTP 429.
2. **After each response:** Proxy extracts updated utilization from response headers.
3. **Fail open:** Stale state or errors → requests pass through. Never permanently blocks.
4. **No persistence:** In-memory only. On restart, allows all requests until first response.
5. **Auto-restart:** systemd handles crash recovery and boot start.
