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

Add this to your OpenClaw instance's workspace files (AGENTS.md, TOOLS.md, or a dedicated config note) so your AI agent knows about the rate limiter:

```markdown
## Rate Limiter (claude-meter)

This instance runs behind a claude-meter proxy that enforces a 25% share of the Anthropic account budget.

- **Status command:** Run `curl -s http://127.0.0.1:7735/status | jq .` to check current utilization
- **When you get a 429:** The proxy blocked you because the account hit 25% utilization. Check the `X-Claude-Meter-Reason` and `Retry-After` headers. Wait for the window to reset.
- **Be token-conscious:** You share this account with 3 other instances. Prefer cache-friendly prompts, avoid unnecessary retries, and batch work when possible.
- **Check before big jobs:** Before starting a large multi-agent task, check `claude-meter status` to see how much headroom you have.
```

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
