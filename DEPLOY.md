# claude-meter Rate Limiter — Deployment Guide

## Overview

Each OpenClaw instance runs its own `claude-meter` proxy that enforces a 25% share of your Anthropic account budget. No cross-machine coordination needed — Anthropic's utilization headers are the source of truth.

```
OpenClaw → claude-meter (port 7735) → api.anthropic.com
```

## Quick Setup

### 1. Build

```bash
cd ~/.openclaw/workspace
git clone git@github.com:ryanshuzzz/claude-meter.git
cd claude-meter && git checkout senor-dev
go build -o claude-meter ./cmd/claude-meter
sudo mv claude-meter /usr/local/bin/
```

### 2. (Optional) Config file

Create `~/.claude-meter/config.yaml`:

```yaml
rate_limits:
  enabled: true
  instance_share: 0.25
  windows:
    5h:
      enabled: true
      hard_limit: 0.25
      warn_threshold: 0.20
    7d:
      enabled: true
      hard_limit: 0.25
      warn_threshold: 0.20
  stale_after_seconds: 300
```

Or skip it — defaults are sensible (25% share, both windows enabled).

### 3. Set up systemd service (auto-start on boot)

```bash
sudo tee /etc/systemd/system/claude-meter.service > /dev/null << 'EOF'
[Unit]
Description=claude-meter rate-limited proxy
After=network.target

[Service]
Type=simple
User=$USER
ExecStart=/usr/local/bin/claude-meter start --plan-tier max_20x --instance-share 0.25
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now claude-meter
```

Verify it's running:
```bash
sudo systemctl status claude-meter
```

### 4. Point OpenClaw at the proxy

Set in your OpenClaw environment (`.env` or config):

```bash
ANTHROPIC_BASE_URL=http://127.0.0.1:7735
```

That's it. No OpenClaw code changes needed.

### 5. Verify

```bash
# Health check
curl http://127.0.0.1:7735/health

# Live status
curl -s http://127.0.0.1:7735/status | jq .

# CLI status (pretty-printed)
claude-meter status

# Agent hook (what the AI runs)
bash claude-meter/scripts/check-usage.sh
```

---

## OpenClaw Agent Instructions

### 1. Add the pre-flight hook to AGENTS.md

Add this to each instance's `AGENTS.md`. This is the critical part — it makes the AI agent usage-aware.

**In Session Startup (add as the last step):**
```markdown
5. **Run usage check:** `bash claude-meter/scripts/check-usage.sh` — check account utilization before doing anything
```

**As a standalone section (add after Session Startup):**
```markdown
## 📊 Rate Limiter (claude-meter)

This instance runs behind a claude-meter proxy that enforces a 25% share of the shared Anthropic account budget.

**Pre-flight check (MANDATORY):** Run `bash claude-meter/scripts/check-usage.sh` BEFORE processing EVERY user message. This is not optional — treat it like reading the message itself. Do it first, silently, every single time.

- **✅ Usage OK** → proceed normally, do not mention the check
- **⚠️ Usage high (>80%)** → conserve tokens: skip non-essential work, use shorter prompts, avoid spawning multiple sub-agents, prefer cached context
- **🚫 RATE LIMITED** → STOP. Tell the user you're rate limited, show the reset time, and suggest waiting. Do NOT retry in a loop.

**Quick status:** `curl -s http://127.0.0.1:7735/status | jq .`

**Be token-conscious always:** You share this account with 3 other instances. Prefer cache-friendly prompts, batch work, and avoid unnecessary retries.
```

### 2. Add HEARTBEAT.md health check

Add this to each instance's `HEARTBEAT.md` so the agent monitors claude-meter during heartbeats:

```markdown
## claude-meter Health
- Run `systemctl is-active claude-meter` — if not active, run `sudo systemctl restart claude-meter` and notify the user
```

### 3. What the hook does

`claude-meter/scripts/check-usage.sh` hits the local proxy's `/status` endpoint and returns:
- Exit 0 + `✅ Usage OK` — headroom exists, proceed normally
- Exit 0 + `⚠️ Usage high` — >80% of budget used, agent should conserve
- Exit 1 + `🚫 RATE LIMITED` — at or above limit, agent should stop and wait
- Exit 0 + `⚠️ not reachable` — proxy down, fail open (proceed without check)

The agent reads the output and adjusts behavior accordingly. No code changes to OpenClaw needed.

---

## Manual Usage Check

To check usage at any time (as a human or from the agent):

```bash
# Quick one-liner (agent hook)
bash claude-meter/scripts/check-usage.sh

# Full JSON status
curl -s http://127.0.0.1:7735/status | jq .

# Pretty CLI output
claude-meter status
```

---

## Adjusting Shares

For fewer instances, increase the share:
- 2 instances: `--instance-share 0.50`
- 3 instances: `--instance-share 0.33`
- 4 instances: `--instance-share 0.25` (default)

Update the systemd service:
```bash
sudo systemctl edit claude-meter
# Add under [Service]: ExecStart= (blank to clear) then ExecStart=/usr/local/bin/claude-meter start --plan-tier max_20x --instance-share 0.50
sudo systemctl restart claude-meter
```

Environment variables also work:
```bash
CLAUDE_METER_INSTANCE_SHARE=0.25
CLAUDE_METER_5H_LIMIT=0.25
CLAUDE_METER_7D_LIMIT=0.25
```

---

## Monitoring

Logs are written to `~/.claude-meter/normalized/YYYY-MM-DD.jsonl` and include `blocked` and `warn` event types alongside normal API records.

Run the analysis:
```bash
python3 analysis/analyze_normalized_log.py ~/.claude-meter --summary
```

---

## How It Works

1. **Before each request:** Proxy checks last-known utilization from Anthropic headers. If ≥ 25%, returns HTTP 429 with `Retry-After`.
2. **After each response:** Proxy extracts updated utilization from response headers and stores in memory.
3. **Fail open:** If state is stale (>300s without headers) or any error occurs, requests pass through. Never permanently blocks due to proxy bugs.
4. **No persistence:** State is in-memory only. On restart, allows all requests until first response updates state.
5. **Auto-restart:** systemd service restarts on crash and starts on boot. Heartbeat monitors health.
