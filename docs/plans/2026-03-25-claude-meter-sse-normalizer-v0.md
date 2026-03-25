# Claude Meter SSE Normalizer V0 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Extend the background normalizer so real `/v1/messages` Claude Code traffic yields `response_model` and `usage` from gzipped SSE responses, not just request metadata and rate-limit headers.

**Architecture:** Keep the proxy hot path unchanged. In the background normalizer, detect `text/event-stream` responses, decode gzip best-effort, parse complete SSE events from the recovered byte stream, and extract `response_model` plus usage from `message_start` and `message_delta`. Preserve the existing generic fallback path so malformed or partial streams still produce normalized records with header-derived rate-limit data.

**Tech Stack:** Go 1.22, stdlib `compress/gzip`, `encoding/json`, local JSONL storage, existing Python analyzer

## Root-Cause Notes

- Real proxy captures in [2026-03-25.jsonl](/Users/abhishekray/.claude-meter/raw/2026-03-25.jsonl) show `/v1/messages?beta=true` responses with:
  - `Content-Type: text/event-stream; charset=utf-8`
  - `Content-Encoding: gzip`
- Decoded SSE samples contain:
  - `event: message_start` with `message.model` and initial `message.usage`
  - `event: message_delta` with final `usage` and `stop_reason`
  - `event: message_stop`
- The current parser in `internal/normalize/normalizer.go` only attempts JSON body decoding for `/v1/messages`, which is why current normalized records have `request_model` but mostly empty `response_model` and `usage`.
- Some gzip bodies appear incomplete at end-of-stream, so the decoder must preserve partial output and parse complete SSE events from it instead of failing hard.

## Recommended Approach

Use a dedicated SSE parsing helper inside `internal/normalize/` rather than bolting more branches onto `normalizer.go`.

Why this approach:
- keeps message-stream logic isolated from generic ratelimit normalization
- makes partial-gzip and SSE parsing testable in small units
- avoids changing the proxy or raw-capture format
- preserves analyzer compatibility by continuing to emit the same normalized schema

Avoid:
- trying to parse SSE inline in `internal/proxy`
- replacing raw logs or normalized schema
- broad “reparse everything” refactors before the SSE path works

### Task 1: Add failing tests for gzipped SSE decoding and event extraction

**Files:**
- Create: `internal/normalize/sse_test.go`
- Modify: `internal/normalize/normalizer_test.go`

**Step 1: Write the failing SSE parser test**

Create `internal/normalize/sse_test.go` with a test named `TestParseSSEEventsExtractsCompleteEventsFromGzippedStream` that:
- builds a gzipped event stream with:
  - `message_start`
  - `content_block_start`
  - `content_block_delta`
  - `message_delta`
  - `message_stop`
- calls a not-yet-existing helper such as:

```go
events, err := parseSSEEvents(gzippedBytes, []capture.Header{
    {Name: "Content-Type", Value: "text/event-stream; charset=utf-8"},
    {Name: "Content-Encoding", Value: "gzip"},
})
```

- asserts:
  - `len(events) == 5`
  - the first event type is `message_start`
  - the last event type is `message_stop`
  - the `data` payloads are preserved as JSON strings

Use a concrete stream like:

```text
event: message_start
data: {"type":"message_start","message":{"model":"claude-opus-4-6","usage":{"input_tokens":3,"cache_creation_input_tokens":220,"cache_read_input_tokens":385688,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":3,"cache_creation_input_tokens":220,"cache_read_input_tokens":385688,"output_tokens":245}}

event: message_stop
data: {"type":"message_stop"}
```

**Step 2: Run the test to verify it fails**

Run: `go test ./internal/normalize -run TestParseSSEEventsExtractsCompleteEventsFromGzippedStream -v`
Expected: FAIL because the SSE parser helpers do not exist yet

**Step 3: Write the failing partial-stream tolerance test**

Add `TestParseSSEEventsToleratesTrailingTruncatedGzip` that:
- uses the same gzipped stream
- truncates a small suffix from the compressed bytes
- asserts the parser still returns at least:
  - `message_start`
  - `message_delta`
- and does not return an error if complete events are recoverable

This is important because real raw logs include some truncated gzip bodies.

**Step 4: Run the test to verify it fails**

Run: `go test ./internal/normalize -run 'TestParseSSEEvents(ExtractsCompleteEventsFromGzippedStream|ToleratesTrailingTruncatedGzip)' -v`
Expected: FAIL because tolerant gzip/SSE parsing is not implemented

### Task 2: Implement the SSE decoder and event parser

**Files:**
- Create: `internal/normalize/sse.go`
- Modify: `internal/normalize/normalizer.go`

**Step 1: Implement minimal SSE types and helpers**

Create `internal/normalize/sse.go` with:
- `type SSEEvent struct`
- `func parseSSEEvents(raw []byte, headers []capture.Header) ([]SSEEvent, error)`
- helper functions:
  - `decodeBodyBestEffort(...)`
  - `splitSSEEvents(...)`
  - `parseSSEEventBlock(...)`

Use a simple event shape:

```go
type SSEEvent struct {
    Event string
    Data  []byte
}
```

**Step 2: Implement best-effort body decoding**

`decodeBodyBestEffort` should:
- return raw bytes unchanged for non-gzip bodies
- use `gzip.NewReader` for gzip
- read until EOF or partial failure
- if bytes were successfully decoded before an error, return the partial decoded bytes and no fatal error
- only fail hard when no useful bytes can be recovered

The intention is: partial body is better than no body when the complete SSE events we need are already present.

**Step 3: Implement SSE splitting**

Treat SSE records as blocks separated by blank lines:
- normalize `\r\n` to `\n`
- split on `\n\n`
- parse lines beginning with:
  - `event:`
  - `data:`
- ignore `ping` events for normalization purposes later, but keep the parser generic
- join multiple `data:` lines with `\n`

**Step 4: Re-run the SSE parser tests**

Run: `go test ./internal/normalize -run 'TestParseSSEEvents(ExtractsCompleteEventsFromGzippedStream|ToleratesTrailingTruncatedGzip)' -v`
Expected: PASS

### Task 3: Add failing tests for `/v1/messages` SSE normalization

**Files:**
- Modify: `internal/normalize/normalizer_test.go`

**Step 1: Replace the current JSON-response assumption**

Keep the existing request-side assertions from `TestNormalizerExtractsMessagesRecord`, but change the response body to a gzipped SSE stream instead of a plain JSON object.

The test should assert:
- `request_model == "claude-opus-4-6"`
- `response_model == "claude-opus-4-6"` from `message_start.message.model`
- `session_id` still comes from request metadata
- usage fields come from the final `message_delta.usage`
  - `input_tokens == 3`
  - `cache_creation_input_tokens == 220`
  - `cache_read_input_tokens == 385688`
  - `output_tokens == 245`

**Step 2: Add a fallback-to-message_start test**

Add `TestNormalizerUsesMessageStartUsageWhenMessageDeltaMissing` with an SSE stream containing:
- `message_start`
- `content_block_*`
- no `message_delta`

Assert the record still uses `message_start.message.usage` and `message_start.message.model`.

**Step 3: Add a partial-stream tolerance test**

Add `TestNormalizerExtractsMessagesRecordFromPartialSSEStream` that:
- truncates the compressed stream after a complete `message_delta` but before the gzip trailer
- asserts the record still extracts:
  - `response_model`
  - `usage.output_tokens`

This directly covers the behavior observed in real raw logs.

**Step 4: Run the tests to verify they fail**

Run:

```bash
go test ./internal/normalize -run 'TestNormalizer(ExtractsMessagesRecord|UsesMessageStartUsageWhenMessageDeltaMissing|ExtractsMessagesRecordFromPartialSSEStream)' -v
```

Expected: FAIL because `enrichMessagesRecord` still assumes a JSON response body

### Task 4: Implement `/v1/messages` SSE normalization

**Files:**
- Modify: `internal/normalize/normalizer.go`
- Modify: `internal/normalize/sse.go`

**Step 1: Add SSE-specific message extraction**

Update `enrichMessagesRecord` so it:
- still parses request JSON via `parseMessagesRequest`
- checks response `Content-Type`
- if the response is event-stream:
  - parse SSE events
  - extract `response_model` from `message_start`
  - prefer final usage from `message_delta`
  - fall back to `message_start.message.usage` if no `message_delta` exists
- otherwise keep the current JSON response parsing path as a fallback

Use small helper functions like:

```go
func parseMessagesSSE(events []SSEEvent) (responseModel string, usage Usage)
func usageFromAny(v any) Usage
```

**Step 2: Keep it best-effort**

If SSE parsing fails:
- do not return an error
- leave the record with whatever request-side and header-side data already exists

**Step 3: Re-run the `/v1/messages` normalization tests**

Run:

```bash
go test ./internal/normalize -run 'TestNormalizer(ExtractsMessagesRecord|UsesMessageStartUsageWhenMessageDeltaMissing|ExtractsMessagesRecordFromPartialSSEStream|ExtractsCountTokensRecord|FallsBackToGenericRecordWhenBodyParsingFails)' -v
```

Expected: PASS

### Task 5: Add regression coverage using real raw-shape fixtures

**Files:**
- Create: `internal/normalize/testdata/messages_sse.txt`
- Create: `internal/normalize/testdata/messages_tool_use_sse.txt`
- Modify: `internal/normalize/sse_test.go`

**Step 1: Save fixture streams from real captures**

Create small fixture files containing decoded SSE text modeled on the real streams observed in [2026-03-25.jsonl](/Users/abhishekray/.claude-meter/raw/2026-03-25.jsonl):
- one text response stream
- one tool-use stream

These do not need to be verbatim giant payloads. Keep only the event sequence needed to prove parsing:
- `message_start`
- representative `content_block_*`
- `message_delta`
- `message_stop`

**Step 2: Add regression tests**

Add tests that:
- gzip the fixture text
- parse it through `parseSSEEvents`
- normalize it through `Normalize(...)`
- assert:
  - text and tool-use responses both produce `response_model`
  - both produce final usage from `message_delta`

**Step 3: Run the tests**

Run: `go test ./internal/normalize -v`
Expected: PASS

### Task 6: Add a backfill path for existing raw logs

**Files:**
- Create: `cmd/claude-meter/backfill.go`
- Modify: `cmd/claude-meter/main.go`
- Modify: `README.md`

**Step 1: Write the failing backfill smoke test**

If practical, add a small test under `internal/app` or a focused command test that:
- points at a temp raw JSONL file containing one captured exchange
- runs a backfill helper
- asserts a normalized JSONL file is produced

If command-level testing is awkward, keep the verification manual in Task 7.

**Step 2: Implement a minimal `backfill-normalized` command**

Add CLI support:

```bash
go run ./cmd/claude-meter backfill-normalized --log-dir ~/.claude-meter --plan-tier max_20x
```

Behavior:
- read `raw/*.jsonl`
- unmarshal `capture.CompletedExchange`
- run the same `normalize.New(planTier).Normalize(...)`
- append to `normalized/*.jsonl`

Keep it minimal:
- no dedupe for now
- document that it should be used on a fresh or moved-aside `normalized/` directory

This is the fastest way to re-analyze your already captured raw traffic after the SSE parser lands.

**Step 3: Update the README**

Document:
- `/v1/messages` SSE parsing
- best-effort handling of partial gzip bodies
- `backfill-normalized` usage

### Task 7: Final verification

**Files:** none

**Step 1: Run the full Go test suite**

Run: `go test ./...`
Expected: PASS

**Step 2: Run a full build**

Run: `go build ./...`
Expected: PASS

**Step 3: Backfill the current raw logs**

Move aside the current normalized output first if you want a clean rerun:

```bash
mv ~/.claude-meter/normalized ~/.claude-meter/normalized.pre-sse.$(date +%s)
go run ./cmd/claude-meter backfill-normalized --log-dir ~/.claude-meter --plan-tier max_20x
```

Expected:
- a fresh `~/.claude-meter/normalized/` directory exists
- `/v1/messages` records now include `response_model`
- many more records include non-empty `usage`

**Step 4: Run the analyzer on the backfilled normalized log**

Run:

```bash
LATEST=$(ls -t ~/.claude-meter/normalized/*.jsonl | head -n1)
python3 analysis/analyze_normalized_log.py "$LATEST" --pretty
```

Expected:
- `window_summary.models` is no longer empty
- `adjacent_deltas` may now become non-empty if utilization changes exist in the captured sample
- the report becomes useful for cap-estimation work, not just header visibility

## Recommended execution order

Implement in this order:
1. SSE parser + partial-gzip tests
2. `/v1/messages` SSE normalization
3. fixture/regression coverage
4. backfill command
5. rerun analysis on existing raw logs

That gets you from root-cause evidence to a usable report as quickly as possible.
