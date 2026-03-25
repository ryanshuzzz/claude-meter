# Claude Meter Proxy V0 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a local-first Go proxy that transparently forwards Claude Code traffic to Anthropic and asynchronously captures full raw exchanges to JSONL.

**Architecture:** The proxy hot path stays dumb: accept requests, forward upstream, stream responses back untouched, and enqueue a completed HTTP exchange in memory. A background writer persists raw exchanges to daily JSONL files under `~/.claude-meter/raw/`; normalization and estimation stay offline for later phases.

**Tech Stack:** Go 1.22, stdlib `net/http`, local JSONL storage

## Notes

- This workspace is not a git repo, so commit steps are intentionally omitted for now.
- Phase 1 is raw-capture only. No SQLite, no dashboard, no inline cap estimation.
- The implementation lives under `spikes/anthropic_limits/proxy/` so it stays isolated from the rest of the workspace.

### Task 1: Initialize the Go spike module

**Files:**
- Create: `spikes/anthropic_limits/proxy/go.mod`
- Create: `spikes/anthropic_limits/proxy/README.md`

**Step 1: Create the module definition**

Create `spikes/anthropic_limits/proxy/go.mod`:

```go
module claude-meter-proxy

go 1.22
```

**Step 2: Add a short spike README**

Document:
- purpose of the spike
- `go run ./cmd/claude-meter start`
- log directory location

**Step 3: Verify module setup**

Run: `cd spikes/anthropic_limits/proxy && go test ./...`
Expected: package discovery runs cleanly even if there are no tests yet

### Task 2: Define the raw exchange data model and JSONL writer

**Files:**
- Create: `spikes/anthropic_limits/proxy/internal/capture/types.go`
- Create: `spikes/anthropic_limits/proxy/internal/storage/jsonl.go`
- Create: `spikes/anthropic_limits/proxy/internal/storage/jsonl_test.go`

**Step 1: Write the failing JSONL writer test**

Create `spikes/anthropic_limits/proxy/internal/storage/jsonl_test.go` with a test that:
- constructs one `CompletedExchange`
- writes it through the writer
- reads the output file back
- asserts one valid JSON object was appended

**Step 2: Run the test to verify it fails**

Run: `cd spikes/anthropic_limits/proxy && go test ./internal/storage -run TestRawExchangeWriter -v`
Expected: FAIL because the writer/types do not exist yet

**Step 3: Add minimal capture types**

Create `spikes/anthropic_limits/proxy/internal/capture/types.go` with:
- `Header`
- `RecordedRequest`
- `RecordedResponse`
- `CompletedExchange`

Keep the types focused on raw capture only.

**Step 4: Add the minimal JSONL writer**

Create `spikes/anthropic_limits/proxy/internal/storage/jsonl.go` with:
- log directory creation
- daily filename resolution
- append-only JSONL writing for `CompletedExchange`

**Step 5: Re-run the test**

Run: `cd spikes/anthropic_limits/proxy && go test ./internal/storage -run TestRawExchangeWriter -v`
Expected: PASS

### Task 3: Add a pass-through proxy integration test

**Files:**
- Create: `spikes/anthropic_limits/proxy/internal/proxy/proxy.go`
- Create: `spikes/anthropic_limits/proxy/internal/proxy/proxy_test.go`

**Step 1: Write the failing proxy test**

Create `spikes/anthropic_limits/proxy/internal/proxy/proxy_test.go` with an integration-style test that:
- starts an `httptest` upstream server
- starts the proxy handler pointed at that upstream server
- sends a request through the proxy
- asserts method/path/body/headers reach upstream
- asserts status/body/headers come back to the client unchanged

**Step 2: Run the test to verify it fails**

Run: `cd spikes/anthropic_limits/proxy && go test ./internal/proxy -run TestProxyForwardsRequestAndResponse -v`
Expected: FAIL because the proxy implementation does not exist yet

**Step 3: Implement the minimal proxy**

Create `spikes/anthropic_limits/proxy/internal/proxy/proxy.go` with:
- `Config`
- `Server`
- `New(...)`
- `Handler()` / `ServeHTTP(...)`

Behavior:
- accept any method/path
- read request body
- forward to configured upstream base URL
- stream upstream response back unchanged
- capture request/response into one `CompletedExchange`
- non-blocking enqueue to a provided channel

**Step 4: Re-run the proxy test**

Run: `cd spikes/anthropic_limits/proxy && go test ./internal/proxy -run TestProxyForwardsRequestAndResponse -v`
Expected: PASS

### Task 4: Add raw capture queue behavior

**Files:**
- Modify: `spikes/anthropic_limits/proxy/internal/proxy/proxy_test.go`
- Modify: `spikes/anthropic_limits/proxy/internal/proxy/proxy.go`

**Step 1: Write a failing capture test**

Add a test that:
- sends one request through the proxy
- reads one `CompletedExchange` from the capture channel
- asserts request and response bodies, status, path, and duration were captured

**Step 2: Run the test to verify it fails**

Run: `cd spikes/anthropic_limits/proxy && go test ./internal/proxy -run TestProxyCapturesCompletedExchange -v`
Expected: FAIL because capture is incomplete

**Step 3: Implement minimal capture completion**

Update `spikes/anthropic_limits/proxy/internal/proxy/proxy.go` so that after the response completes it:
- assembles the `CompletedExchange`
- attempts a non-blocking send onto the capture channel
- increments a dropped counter if the channel is full

**Step 4: Re-run the proxy tests**

Run: `cd spikes/anthropic_limits/proxy && go test ./internal/proxy -v`
Expected: PASS

### Task 5: Wire the background raw writer and CLI entrypoint

**Files:**
- Create: `spikes/anthropic_limits/proxy/internal/app/app.go`
- Create: `spikes/anthropic_limits/proxy/cmd/claude-meter/main.go`

**Step 1: Write a failing end-to-end smoke test**

If practical, add a small test in `internal/app` that:
- starts the app with a temp raw log directory and `httptest` upstream
- sends one request
- waits briefly for the writer goroutine
- asserts a raw JSONL file was created

If that test is too expensive for the first pass, defer it and use Task 6 verification instead.

**Step 2: Implement the app wiring**

Create `spikes/anthropic_limits/proxy/internal/app/app.go` with:
- queue creation
- background writer goroutine
- proxy construction
- HTTP server startup

Create `spikes/anthropic_limits/proxy/cmd/claude-meter/main.go` with:
- `start` command only
- flags for `--port`, `--upstream`, `--log-dir`

**Step 3: Manual smoke verification**

Run:

```bash
cd spikes/anthropic_limits/proxy
go run ./cmd/claude-meter start --port 7735
```

In another shell:

```bash
curl -i http://127.0.0.1:7735/
```

Expected:
- proxy process starts
- raw log directory is created
- requests are written to JSONL

### Task 6: Final verification

**Files:** none

**Step 1: Run the full Go test suite**

Run: `cd spikes/anthropic_limits/proxy && go test ./...`
Expected: PASS

**Step 2: Run a full build**

Run: `cd spikes/anthropic_limits/proxy && go build ./...`
Expected: PASS

**Step 3: Record remaining gaps**

Note explicitly:
- no normalized-record pipeline yet
- no Anthropic-specific SSE/usage parsing yet
- no local CLI analysis commands yet

Plan complete and saved to `docs/plans/2026-03-25-claude-meter-proxy-v0.md`.

Two execution options:

**1. Subagent-Driven (this session)** - I dispatch fresh subagent per task, review between tasks, fast iteration

**2. Parallel Session (separate)** - Open new session with executing-plans, batch execution with checkpoints

Which approach?
