# claude-meter Rate Limiter — Deployment Guide

## Overview

Each OpenClaw instance runs its own `claude-meter` proxy that enforces a 25% share of your Anthropic account budget. No cross-machine coordination needed — Anthropic's utilization headers are the source of truth.

```
OpenClaw → claude-meter (port 7735) → api.anthropic.com
```

## Quick Setup

### 1. Build

```bash
cd claude-meter
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

### 3. Start the proxy

```bash
claude-meter start --plan-tier max_20x --instance-share 0.25
```

Or as a systemd service:

```ini
# /etc/systemd/system/claude-meter.service
[Unit]
Description=claude-meter rate-limited proxy
After=network.target

[Service]
Type=simple
User=senorai
ExecStart=/usr/local/bin/claude-meter start --plan-tier max_20x --instance-share 0.25
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now claude-meter
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
curl http://127.0.0.1:7735/status | jq .

# CLI status (pretty-printed)
claude-meter status
```

## OpenClaw Agent Instructions

### 1. Clone the repo into each instance's workspace

```bash
cd ~/.openclaw/workspace
git clone git@github.com:ryanshuzzz/claude-meter.git
cd claude-meter && git checkout senor-dev
```

### 2. Add the pre-flight hook to AGENTS.md

Add this to each instance's `AGENTS.md` — in the **Session Startup** section AND as a standalone section:

**In Session Startup (add as step 5):**
```markdown
5. **Run usage check:** `bash claude-meter/scripts/check-usage.sh` — check account utilization before doing anything
```

**As a standalone section:**
```markdown
## 📊 Rate Limiter (claude-meter)

This instance runs behind a claude-meter proxy that enforces a 25% share of the shared Anthropic account budget.

**Pre-flight check (MANDATORY):** Run `bash claude-meter/scripts/check-usage.sh` at the START of every session and BEFORE any heavy work (multi-agent spawns, large refactors, long conversations).

- **✅ Usage OK** → proceed normally
- **⚠️ Usage high (>80%)** → conserve tokens: skip non-essential work, use shorter prompts, avoid spawning multiple sub-agents, prefer cached context
- **🚫 RATE LIMITED** → STOP. Tell the user you're rate limited, show the reset time, and suggest waiting. Do NOT retry in a loop.

**Quick status:** `curl -s http://127.0.0.1:7735/status | jq .`

**Be token-conscious always:** You share this account with 3 other instances. Prefer cache-friendly prompts, batch work, and avoid unnecessary retries.
```

### 3. What the hook does

`claude-meter/scripts/check-usage.sh` hits the local proxy's `/status` endpoint and returns:
- Exit 0 + "✅ Usage OK" — headroom exists, proceed normally
- Exit 0 + "⚠️ Usage high" — >80% of budget used, agent should conserve
- Exit 1 + "🚫 RATE LIMITED" — at or above limit, agent should stop and wait
- Exit 0 + "⚠️ not reachable" — proxy down, fail open (proceed without check)

The agent reads the output and adjusts behavior accordingly. No code changes to OpenClaw needed.

## Adjusting Shares

For fewer instances, increase the share:
- 2 instances: `--instance-share 0.50`
- 3 instances: `--instance-share 0.33`
- 4 instances: `--instance-share 0.25` (default)

Environment variables also work:
```bash
CLAUDE_METER_INSTANCE_SHARE=0.25
CLAUDE_METER_5H_LIMIT=0.25
CLAUDE_METER_7D_LIMIT=0.25
```

## Monitoring

Logs are written to `~/.claude-meter/normalized/YYYY-MM-DD.jsonl` and include `blocked` and `warn` event types alongside normal API records.

Run the analysis:
```bash
python3 analysis/analyze_normalized_log.py ~/.claude-meter --summary
```

## How It Works

1. **Before each request:** Proxy checks last-known utilization from Anthropic headers. If ≥ 25%, returns HTTP 429 with `Retry-After`.
2. **After each response:** Proxy extracts updated utilization from response headers and stores in memory.
3. **Fail open:** If state is stale (>300s without headers) or any error occurs, requests pass through. Never permanently blocks due to proxy bugs.
4. **No persistence:** State is in-memory only. On restart, allows all requests until first response updates state.
