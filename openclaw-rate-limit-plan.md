# OpenClaw Distributed Rate Limiter — Implementation Plan
## Opus Orchestration Brief

**Objective:** Fork and extend `claude-meter` (https://github.com/abhishekray07/claude-meter) to enforce hard rate limits of 25% of the total Anthropic account budget per 5-hour window and per 7-day window, shared equally among 4 OpenClaw instances running on separate machines. Each instance enforces its own cap (25% of account total) using live account-wide utilization signals returned in Anthropic's response headers — no cross-machine coordination required.

---

## 0. Orientation

Before writing any code, read and internalize the following:

- **claude-meter repo:** https://github.com/abhishekray07/claude-meter
- **Key files to read first:**
  - `cmd/claude-meter/main.go` — proxy entrypoint
  - `internal/proxy/` — transparent HTTP proxy, this is where pre-flight blocking goes
  - `internal/normalize/` — SSE/response parsing, rate-limit header extraction
  - `internal/storage/` — JSONL writers for raw and normalized logs
  - `analysis/analyze_normalized_log.py` — offline estimator (reference only)

**Do not begin implementation until you have read all files in `internal/proxy/` and `internal/normalize/` in full.**

---

## 1. Problem Statement in Precise Terms

Anthropic enforces two rolling quota windows on a Claude Max account:
- **5-hour window** (`anthropic-ratelimit-unified-5h-utilization`): resets on a rolling basis
- **7-day window** (`anthropic-ratelimit-unified-7d-utilization`): resets weekly

These headers return a float percentage (0.0–1.0 or 0–100, verify the actual format in live traffic before assuming) representing how much of the **total account budget** has been consumed across all sessions, all machines, all tools.

With 4 OpenClaw instances sharing one account, each instance should be hard-capped at **25% of the total account budget** per window. When a machine's observed account utilization would exceed 25% if the next request goes through, the proxy must reject that request with HTTP 429 before forwarding it upstream.

This means: if the account is already at 24% and the current machine has been responsible for most of that, it still gets one more request. If the account is already at 25%, all machines block. This is intentionally conservative — it protects all 4 instances from one runaway agent consuming the full pool.

---

## 2. Architecture Overview

```
OpenClaw Instance (Machine A)          OpenClaw Instance (Machine B)
         │                                       │
         ▼                                       ▼
  claude-meter proxy                    claude-meter proxy
  port 7735, limit=25%                  port 7735, limit=25%
         │                                       │
         ▼                                       ▼
  pre-flight check:                     pre-flight check:
  lastKnown5hUtil >= 0.25?              lastKnown5hUtil >= 0.25?
  lastKnown7dUtil >= 0.25?              lastKnown7dUtil >= 0.25?
         │                                       │
         └────────────┬──────────────────────────┘
                      ▼
             api.anthropic.com
             (returns updated utilization headers on every response)
```

No side channel, no shared database, no network between machines. The Anthropic headers are the source of truth.

---

## 3. Rate Limit Header Contract

Before writing anything, use a sub-agent to capture and log a small sample of live claude-meter proxy traffic and confirm the exact format of:

```
anthropic-ratelimit-unified-5h-utilization
anthropic-ratelimit-unified-5h-limit
anthropic-ratelimit-unified-5h-remaining
anthropic-ratelimit-unified-7d-utilization
anthropic-ratelimit-unified-7d-limit
anthropic-ratelimit-unified-7d-remaining
anthropic-ratelimit-unified-5h-reset
anthropic-ratelimit-unified-7d-reset
```

Log at least 5 real response headers from `~/.claude-meter/raw/` normalized records to answer:
- Are utilization values 0–1 floats or 0–100 integers?
- Are they present on every response or only some?
- Are they present on streaming SSE responses or only final responses?
- What happens on a 429 from Anthropic — do the headers still appear?

**Gate:** Do not proceed to implementation until header format is confirmed from real data.

---

## 4. Configuration System

### 4.1 New Config File

Create `~/.claude-meter/config.yaml` (or `config.json` if the project uses JSON elsewhere — match existing conventions):

```yaml
rate_limits:
  enabled: true
  
  # Hard cap per instance as a fraction of total account budget
  # 4 instances × 0.25 = 1.0 (full account)
  instance_share: 0.25
  
  windows:
    5h:
      enabled: true
      # Block when account utilization >= this threshold
      hard_limit: 0.25
      # Warn in logs when approaching (optional, for observability)
      warn_threshold: 0.20
    7d:
      enabled: true
      hard_limit: 0.25
      warn_threshold: 0.20
  
  # Behavior when blocked
  on_limit_exceeded:
    # Return 429 to caller with Retry-After header
    http_status: 429
    # Message returned to OpenClaw/caller
    message: "Instance rate limit reached (25% of account budget consumed). Retry after window resets."
    # Whether to include reset time in Retry-After header
    include_retry_after: true

  # Staleness: if no response received in this many seconds, allow requests through
  # (prevents deadlock if Anthropic stops sending headers)
  stale_after_seconds: 300
```

### 4.2 Config Loading

Add `internal/config/config.go`:
- Load from `~/.claude-meter/config.yaml`
- Override via environment variables: `CLAUDE_METER_INSTANCE_SHARE`, `CLAUDE_METER_5H_LIMIT`, `CLAUDE_METER_7D_LIMIT`
- Validate: `instance_share` must be between 0.0 and 1.0, `hard_limit` must be ≤ `instance_share`
- Expose a `Config` struct used by the proxy

---

## 5. State Management

### 5.1 New File: `internal/ratelimit/state.go`

```go
package ratelimit

import (
    "sync"
    "time"
)

// WindowState holds the most recently observed utilization for one window.
type WindowState struct {
    Utilization float64   // 0.0–1.0, fraction of account budget used
    Remaining   float64   // raw remaining value from header (units TBD after header audit)
    ResetAt     time.Time // when this window resets
    ObservedAt  time.Time // when we last got a fresh header
}

// AccountState is the in-memory state updated on every proxied response.
// Thread-safe. One instance per proxy process.
type AccountState struct {
    mu  sync.RWMutex
    W5h WindowState
    W7d WindowState
}

func NewAccountState() *AccountState {
    return &AccountState{}
}

// Update parses and stores utilization from response headers.
// Called from the proxy response handler after every upstream response.
func (s *AccountState) Update(headers map[string]string) {
    s.mu.Lock()
    defer s.mu.Unlock()
    // Parse headers into WindowState fields
    // Implementation details in Section 6
}

// Check returns (blocked bool, reason string, retryAfter time.Time)
// Called from the proxy request handler BEFORE forwarding upstream.
func (s *AccountState) Check(cfg *config.RateLimitConfig) (bool, string, time.Time) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    // Implementation details in Section 7
}
```

### 5.2 Thread Safety Requirements

The proxy handles concurrent requests. `AccountState` must:
- Use `sync.RWMutex`: reads (Check) take RLock, writes (Update) take Lock
- Never block a request waiting for a write lock longer than 5ms — if lock is contended, allow the request through (fail open, not fail closed, to avoid deadlock)
- Be safe to call from multiple goroutines simultaneously

---

## 6. Response Header Parsing

### 6.1 New File: `internal/ratelimit/parser.go`

Implement `ParseHeaders(h http.Header) (map[string]WindowState, error)`:

Parse the following (exact header names to be confirmed in Section 3):
```
anthropic-ratelimit-unified-5h-utilization  → W5h.Utilization
anthropic-ratelimit-unified-5h-reset        → W5h.ResetAt
anthropic-ratelimit-unified-7d-utilization  → W7d.Utilization
anthropic-ratelimit-unified-7d-reset        → W7d.ResetAt
```

Handle edge cases:
- Header absent: keep previous state, do not update `ObservedAt`
- Header present but malformed: log a warning, keep previous state
- Utilization > 1.0: normalize by dividing by 100 if values appear to be percentages (determine from header audit in Section 3)
- Reset timestamp: parse as RFC3339 or Unix timestamp (determine format from real data)

### 6.2 SSE Stream Handling

claude-meter already parses SSE streams in `internal/normalize/`. Rate-limit headers may only appear in the **final** response frame or in the HTTP response headers (not in the SSE body). Confirm this during the header audit.

If headers appear only in HTTP response headers (most likely): the existing proxy response handler already has access to these before streaming begins. Extract them there.

If headers appear in SSE frames: extend the SSE parser in `internal/normalize/` to emit a `HeaderUpdate` event that the proxy can consume.

---

## 7. Pre-Flight Enforcement

### 7.1 Modify `internal/proxy/proxy.go`

In the request handler, **before** dialing upstream, call `state.Check()`:

```go
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // 1. Pre-flight rate limit check
    blocked, reason, retryAfter := p.state.Check(p.cfg.RateLimits)
    if blocked {
        w.Header().Set("Retry-After", retryAfter.UTC().Format(time.RFC1123))
        w.Header().Set("X-Claude-Meter-Blocked", "true")
        w.Header().Set("X-Claude-Meter-Reason", reason)
        http.Error(w, reason, http.StatusTooManyRequests)
        p.logBlocked(r, reason) // see Section 8
        return
    }

    // 2. Forward request upstream (existing logic)
    // ...

    // 3. On response received, update state from headers
    p.state.Update(extractRateLimitHeaders(resp.Header))
}
```

### 7.2 `Check()` Logic (detailed)

```go
func (s *AccountState) Check(cfg *RateLimitConfig) (blocked bool, reason string, retryAfter time.Time) {
    if !cfg.Enabled {
        return false, "", time.Time{}
    }

    now := time.Now()

    // 5h window check
    if cfg.Windows.H5.Enabled {
        stale := s.W5h.ObservedAt.IsZero() || 
                 now.Sub(s.W5h.ObservedAt) > cfg.StaleAfterSeconds
        if !stale && s.W5h.Utilization >= cfg.Windows.H5.HardLimit {
            return true,
                fmt.Sprintf("5h window: account at %.1f%% (instance cap: %.1f%%)",
                    s.W5h.Utilization*100, cfg.Windows.H5.HardLimit*100),
                s.W5h.ResetAt
        }
    }

    // 7d window check
    if cfg.Windows.D7.Enabled {
        stale := s.W7d.ObservedAt.IsZero() ||
                 now.Sub(s.W7d.ObservedAt) > cfg.StaleAfterSeconds
        if !stale && s.W7d.Utilization >= cfg.Windows.D7.HardLimit {
            return true,
                fmt.Sprintf("7d window: account at %.1f%% (instance cap: %.1f%%)",
                    s.W7d.Utilization*100, cfg.Windows.D7.HardLimit*100),
                s.W7d.ResetAt
        }
    }

    return false, "", time.Time{}
}
```

**Stale state behavior:** If no headers have been received within `stale_after_seconds` (default 300), allow requests through. This prevents the proxy from permanently blocking if Anthropic stops returning headers (e.g. during an outage). Log a warning when operating in stale state.

---

## 8. Observability

### 8.1 Block Event Logging

Add a `blocked` record type to the normalized JSONL schema:

```json
{
  "type": "blocked",
  "ts": "2026-03-27T18:00:00Z",
  "window": "5h",
  "account_utilization": 0.251,
  "instance_limit": 0.25,
  "retry_after": "2026-03-27T20:00:00Z",
  "request_path": "/v1/messages",
  "request_model": "claude-opus-4-6"
}
```

Write this to `~/.claude-meter/normalized/YYYY-MM-DD.jsonl` alongside existing records.

### 8.2 Warn Threshold Logging

When utilization crosses `warn_threshold` (default 0.20 = 80% of this instance's budget), log a structured warning to normalized log:

```json
{
  "type": "warn",
  "ts": "...",
  "window": "5h",
  "account_utilization": 0.21,
  "instance_limit": 0.25,
  "headroom_remaining": 0.04
}
```

### 8.3 Status Endpoint

Add a `/status` HTTP endpoint to the proxy (e.g. `http://localhost:7736/status`):

```json
{
  "instance_limit": 0.25,
  "windows": {
    "5h": {
      "utilization": 0.18,
      "limit": 0.25,
      "headroom": 0.07,
      "pct_of_limit_used": 72.0,
      "reset_at": "2026-03-27T20:00:00Z",
      "stale": false,
      "observed_at": "2026-03-27T18:59:54Z"
    },
    "7d": {
      "utilization": 0.09,
      "limit": 0.25,
      "headroom": 0.16,
      "pct_of_limit_used": 36.0,
      "reset_at": "2026-03-30T00:00:00Z",
      "stale": false,
      "observed_at": "2026-03-27T18:59:54Z"
    }
  },
  "blocked_requests_today": 0,
  "proxy_uptime_seconds": 3600
}
```

This endpoint is unauthenticated (local only). Bind it to `127.0.0.1` only.

### 8.4 Update Analysis Script

Extend `analysis/analyze_normalized_log.py` to include:
- Count of blocked requests per day
- Count of warn-threshold crossings per day
- Time spent at or above cap
- Add to existing summary output:

```
Rate Limit Events
--------------------
  Blocks (5h):    3
  Blocks (7d):    0
  Warnings (5h):  12
  Warnings (7d):  2
  Time at cap:    0h 14m (5h window)
```

---

## 9. CLI Changes

### 9.1 New Flag: `--instance-share`

```bash
go run ./cmd/claude-meter start \
  --plan-tier max_20x \
  --instance-share 0.25
```

This overrides `config.yaml` instance share value. Validate: must be between 0.01 and 1.0.

### 9.2 New Subcommand: `status`

```bash
go run ./cmd/claude-meter status
```

Fetches `http://127.0.0.1:7735/status` and prints a human-readable summary:

```
claude-meter status
====================
Instance share:  25.0% of account

5h Window
  Account util:  18.0% / 25.0% cap  [██████████░░░░░░]  72% of budget used
  Headroom:      7.0% remaining
  Resets:        in 1h 2m

7d Window  
  Account util:  9.0% / 25.0% cap   [████░░░░░░░░░░░░]  36% of budget used
  Headroom:      16.0% remaining
  Resets:        in 2d 5h

Blocked today:  0 requests
```

---

## 10. OpenClaw Integration

### 10.1 Per-Machine Launch Configuration

On each machine, create a launch wrapper that sets the proxy as the upstream:

**macOS LaunchAgent** (`~/Library/LaunchAgents/com.claude-meter.rate-limited.plist`):
```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "...">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.claude-meter.rate-limited</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/claude-meter</string>
    <string>start</string>
    <string>--plan-tier</string><string>max_20x</string>
    <string>--instance-share</string><string>0.25</string>
  </array>
</dict>
</plist>
```

**OpenClaw environment** — set in OpenClaw's config or `.env`:
```bash
ANTHROPIC_BASE_URL=http://127.0.0.1:7735
```

This is the only change needed on the OpenClaw side. No OpenClaw modifications required.

### 10.2 Verification

After deploying to one machine, verify the integration is working:

```bash
# 1. Check proxy is running
curl http://127.0.0.1:7735/health

# 2. Check status endpoint shows live utilization
curl http://127.0.0.1:7736/status | jq .

# 3. Send a test request through OpenClaw and confirm:
#    a) Response succeeds
#    b) Status endpoint shows updated utilization
#    c) ~/.claude-meter/normalized/ shows a new record with parsed utilization

# 4. Simulate cap exceeded: temporarily set --instance-share 0.001 and
#    verify next request returns 429
```

---

## 11. Sub-Agent Task Breakdown

Use sub-agents for the following parallel workstreams. Spawn each as a separate Claude Code session.

### Sub-Agent A: Header Audit (BLOCKING — do first)

**Task:** Read all JSONL files under `~/.claude-meter/raw/` (or `~/.claude-meter/normalized/` if richer). Extract and log every unique rate-limit header name and its value format across at least 10 different response records. Return a structured report:

```json
{
  "header_format": {
    "5h_utilization": {"example_values": ["0.18", "18"], "unit": "fraction|percent"},
    "7d_utilization": {"example_values": [...], "unit": "fraction|percent"},
    "reset_format": "RFC3339|Unix|other",
    "present_on": ["streaming_final_frame", "non_streaming_response", "429_response"]
  }
}
```

**Gate:** All other sub-agents wait for this report before writing parsing code.

### Sub-Agent B: Config System

**Task:** Implement `internal/config/config.go` and `internal/config/config_test.go`. Load from `~/.claude-meter/config.yaml`, env overrides, validation. Write unit tests covering: missing file (use defaults), invalid share value, env override precedence.

**Depends on:** Nothing. Can run in parallel with Sub-Agent A.

### Sub-Agent C: State + Parser

**Task:** Implement `internal/ratelimit/state.go` and `internal/ratelimit/parser.go`. Use header format confirmed by Sub-Agent A. Write unit tests covering: concurrent reads/writes, stale state after timeout, header absent, header malformed, both windows blocked, only one blocked.

**Depends on:** Sub-Agent A (header format), Sub-Agent B (config types)

### Sub-Agent D: Proxy Integration

**Task:** Modify `internal/proxy/proxy.go` to wire in pre-flight check and response update. Add `/status` endpoint. Add `/health` endpoint if not already present. Write integration test: spin up proxy against a mock HTTP server that returns fake rate-limit headers, send requests, verify blocking behavior.

**Depends on:** Sub-Agent C (state + parser), Sub-Agent B (config)

### Sub-Agent E: CLI + Analysis

**Task:** Add `--instance-share` flag to `cmd/claude-meter/main.go`. Add `status` subcommand. Update `analysis/analyze_normalized_log.py` to count and display block/warn events.

**Depends on:** Sub-Agent B (config), Sub-Agent D (status endpoint schema)

### Sub-Agent F: Logging

**Task:** Add `blocked` and `warn` record types to normalized JSONL writer in `internal/storage/`. Update the normalizer schema documentation if any exists. Ensure new record types do not break existing analysis scripts (run existing analysis against a log containing new record types and confirm no crashes).

**Depends on:** Sub-Agent B (config), Sub-Agent C (state types)

---

## 12. Testing Plan

### 12.1 Unit Tests (per sub-agent, run during implementation)

- Config loading and validation
- Header parsing: all edge cases from Section 6
- State thread safety: use `go test -race`
- Check logic: all combinations of window states

### 12.2 Integration Test (Sub-Agent D)

Create `internal/proxy/ratelimit_integration_test.go`:

1. Start proxy pointing at a mock Anthropic server
2. Mock server returns headers simulating utilization at 0.10
3. Send 3 requests — all should succeed
4. Mock server changes to return utilization at 0.26
5. Send next request — should return 429
6. Check `/status` endpoint — should show blocked state
7. Check normalized log — should contain a `blocked` record

### 12.3 Stale State Test

1. Start proxy
2. Let it receive one response with utilization 0.10
3. Mock clock or wait for stale timeout
4. Set mock server to return utilization 0.30 but don't send any responses
5. Confirm requests still pass through (stale state = fail open)
6. Send one request (gets the 0.30 header)
7. Confirm next request is blocked

### 12.4 End-to-End Smoke Test (run after full integration)

On a real machine with OpenClaw:
1. Start claude-meter with `--instance-share 0.25`
2. Run `openclaw` with `ANTHROPIC_BASE_URL=http://127.0.0.1:7735`
3. Run a simple task through OpenClaw
4. Confirm `claude-meter status` shows live utilization from real headers
5. Confirm normalized log contains the request with utilization values

---

## 13. File Change Manifest

Summarized list of all files to create or modify:

| File | Action | Sub-Agent |
|------|--------|-----------|
| `internal/config/config.go` | CREATE | B |
| `internal/config/config_test.go` | CREATE | B |
| `internal/ratelimit/state.go` | CREATE | C |
| `internal/ratelimit/parser.go` | CREATE | C |
| `internal/ratelimit/state_test.go` | CREATE | C |
| `internal/ratelimit/parser_test.go` | CREATE | C |
| `internal/proxy/proxy.go` | MODIFY | D |
| `internal/storage/writer.go` | MODIFY | F |
| `cmd/claude-meter/main.go` | MODIFY | E |
| `analysis/analyze_normalized_log.py` | MODIFY | E |
| `~/.claude-meter/config.yaml` | CREATE (runtime) | D |
| `~/Library/LaunchAgents/com.claude-meter.rate-limited.plist` | CREATE (docs) | E |

---

## 14. Definition of Done

The implementation is complete when:

- [ ] `go test ./...` passes with `-race` flag, zero failures
- [ ] `claude-meter start --instance-share 0.25` runs without error
- [ ] `ANTHROPIC_BASE_URL=http://127.0.0.1:7735 claude "hello"` succeeds and updates `/status`
- [ ] When account utilization headers indicate ≥ 25%, the next request returns HTTP 429 with `Retry-After` header
- [ ] When utilization drops back below 25% (new headers received), requests succeed again
- [ ] `claude-meter status` prints readable summary with live utilization
- [ ] Blocked requests appear in normalized JSONL as `type: blocked`
- [ ] Analysis script shows block/warn counts without crashing
- [ ] No changes required to OpenClaw itself — only `ANTHROPIC_BASE_URL` env var

---

## 15. Important Constraints and Gotchas

1. **Do not modify OpenClaw.** All changes are proxy-side only. OpenClaw is configured via `ANTHROPIC_BASE_URL` env var only.

2. **Fail open, not closed.** If state is stale, headers are missing, or an error occurs in Check(), allow the request through and log the anomaly. Never permanently block due to a proxy bug.

3. **The 25% cap is on account-wide utilization, not per-machine request count.** Four instances each capped at 25% of the total account budget means the account can reach 100% if all four hit their caps simultaneously. This is correct and intended.

4. **Do not persist state across proxy restarts.** State is in-memory only. On restart, the proxy starts with no utilization data and allows requests until headers are received. The first response will update state immediately.

5. **The proxy must remain fully transparent.** All requests not blocked must be forwarded byte-for-byte unchanged, including streaming SSE responses. Do not buffer streaming responses.

6. **Header names may change.** Anthropic has changed header names before. The parser should log unrecognized but related headers (anything containing `ratelimit`) as debug output to make future changes visible.

7. **Match existing code style.** Before writing any Go, read at least 3 existing Go files in the repo and match naming conventions, error handling patterns, and logging style exactly.
