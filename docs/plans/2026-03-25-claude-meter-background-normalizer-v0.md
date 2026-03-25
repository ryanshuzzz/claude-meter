# Claude Meter Background Normalizer V0 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a background normalizer that derives analysis-ready normalized request records from captured proxy exchanges and writes them to local JSONL without touching the proxy hot path.

**Architecture:** Keep the proxy unchanged on the request path: it still captures one `CompletedExchange` and enqueues it. In the background worker, persist raw first, then derive one normalized record from the in-memory exchange and append it to `~/.claude-meter/normalized/`. The normalized schema should stay compatible with the existing Python analyzer so `analysis/analyze_normalized_log.py` can consume proxy-generated records without modification.

**Tech Stack:** Go 1.22, stdlib `net/http` and `compress/gzip`, local JSONL storage, existing Python analysis scripts

## Notes

- This repo is already isolated at `~/Projects/opslane/claude-meter`, so no additional worktree is required for this phase.
- The hot path must remain dumb: no Anthropic-specific parsing in `internal/proxy`.
- Raw persistence stays the source of truth. Normalization is best-effort and must never break proxying or raw logging.
- V0 only needs to normalize endpoints we already understand well:
  - `POST /v1/messages`
  - `POST /v1/messages?beta=true`
  - `POST /v1/messages/count_tokens`
- Unknown endpoints should still emit a minimal generic normalized record when possible.
- The schema should remain compatible with the current Python normalized record shape:
  - `id`
  - `request_timestamp`
  - `response_timestamp`
  - `method`
  - `path`
  - `status`
  - `latency_ms`
  - `request_model`
  - `response_model`
  - `session_id`
  - `request_id`
  - `usage`
  - `ratelimit`
- Add `declared_plan_tier` now. The Python analyzer ignores unknown fields, so this is forward-compatible and useful for later segmentation.

### Task 1: Define the normalized record schema and JSONL writer

**Files:**
- Create: `internal/normalize/types.go`
- Create: `internal/storage/normalized_jsonl.go`
- Create: `internal/storage/normalized_jsonl_test.go`

**Step 1: Write the failing normalized writer test**

Create `internal/storage/normalized_jsonl_test.go` with a test that:
- constructs one normalized record with `5h` and `7d` windows
- writes it through a normalized writer
- reads the JSONL file back
- asserts the record round-trips correctly
- asserts the writer uses private permissions (`0700` dir, `0600` file)

Use a concrete test record like:

```go
record := normalize.Record{
    ID:                42,
    RequestTimestamp:  time.Date(2026, 3, 25, 21, 56, 59, 0, time.UTC),
    ResponseTimestamp: time.Date(2026, 3, 25, 21, 57, 00, 0, time.UTC),
    Method:            "POST",
    Path:              "/v1/messages?beta=true",
    Status:            200,
    LatencyMS:         491,
    RequestModel:      "claude-haiku-4-5-20251001",
    ResponseModel:     "claude-haiku-4-5-20251001",
    SessionID:         "eaa76a80-f3ca-4398-a7f3-f9e133817f6c",
    DeclaredPlanTier:  "max_20x",
    RequestID:         "req_011CZQYczYiLGqSxebEhms6X",
    Usage: normalize.Usage{
        InputTokens: 1,
        OutputTokens: 8,
    },
    Ratelimit: normalize.Ratelimit{
        Status:               "allowed",
        RepresentativeClaim:  "five_hour",
        OverageDisabledReason:"out_of_credits",
        Windows: map[string]normalize.RatelimitWindow{
            "5h": {Status: "allowed", Utilization: 0.10, ResetTS: 1774490400},
            "7d": {Status: "allowed", Utilization: 0.61, ResetTS: 1774580400},
        },
    },
}
```

**Step 2: Run the test to verify it fails**

Run: `go test ./internal/storage -run TestNormalizedRecordWriter -v`
Expected: FAIL because the normalized writer and types do not exist yet

**Step 3: Add the normalized schema types**

Create `internal/normalize/types.go` with:
- `Record`
- `Usage`
- `Ratelimit`
- `RatelimitWindow`

Prefer explicit JSON field tags that match the existing Python normalized record schema exactly.

Use structs like:

```go
type Record struct {
    ID                uint64    `json:"id"`
    RequestTimestamp  time.Time `json:"request_timestamp"`
    ResponseTimestamp time.Time `json:"response_timestamp"`
    Method            string    `json:"method"`
    Path              string    `json:"path"`
    Status            int       `json:"status"`
    LatencyMS         int64     `json:"latency_ms"`
    RequestModel      string    `json:"request_model,omitempty"`
    ResponseModel     string    `json:"response_model,omitempty"`
    SessionID         string    `json:"session_id,omitempty"`
    DeclaredPlanTier  string    `json:"declared_plan_tier,omitempty"`
    RequestID         string    `json:"request_id,omitempty"`
    Usage             Usage     `json:"usage"`
    Ratelimit         Ratelimit `json:"ratelimit"`
}
```

**Step 4: Add the normalized JSONL writer**

Create `internal/storage/normalized_jsonl.go` with behavior parallel to the raw writer:
- create `normalized/` with `0700`
- append one JSON object per line
- create log files with `0600`
- use the exchange request date for the filename

**Step 5: Re-run the test**

Run: `go test ./internal/storage -run TestNormalizedRecordWriter -v`
Expected: PASS

### Task 2: Add header parsing and generic normalization helpers

**Files:**
- Create: `internal/normalize/normalizer.go`
- Create: `internal/normalize/normalizer_test.go`

**Step 1: Write the failing generic normalization test**

Create a test named `TestNormalizerBuildsGenericRecordFromExchange` that:
- creates one `capture.CompletedExchange`
- includes request/response headers for:
  - `Request-Id`
  - `Retry-After`
  - `Anthropic-Ratelimit-Unified-Status`
  - `Anthropic-Ratelimit-Unified-Representative-Claim`
  - `Anthropic-Ratelimit-Unified-5h-Status`
  - `Anthropic-Ratelimit-Unified-5h-Utilization`
  - `Anthropic-Ratelimit-Unified-5h-Reset`
  - `Anthropic-Ratelimit-Unified-7d-Status`
  - `Anthropic-Ratelimit-Unified-7d-Utilization`
- uses an unknown endpoint path like `/v1/other`
- asserts the normalizer still produces:
  - id, timestamps, method, path, status, latency
  - `request_id`
  - parsed top-level ratelimit fields
  - parsed window map with `5h` and `7d`
  - empty usage/model/session fields

**Step 2: Run the test to verify it fails**

Run: `go test ./internal/normalize -run TestNormalizerBuildsGenericRecordFromExchange -v`
Expected: FAIL because the normalizer does not exist yet

**Step 3: Implement header and record helpers**

Create `internal/normalize/normalizer.go` with:
- `type Normalizer struct`
- `func New(planTier string) *Normalizer`
- `func (n *Normalizer) Normalize(exchange capture.CompletedExchange) Record`
- helper functions for:
  - header lookup from `[]capture.Header`
  - parsing `anthropic-ratelimit-unified-*`
  - coercing header values into `int`, `float64`, or `string`

Keep the ratelimit shape aligned with `analysis/normalize_sniffer_log.py`:

```go
type Ratelimit struct {
    Status               string                     `json:"status,omitempty"`
    RepresentativeClaim  string                     `json:"representative_claim,omitempty"`
    FallbackPercentage   float64                    `json:"fallback_percentage,omitempty"`
    OverageDisabledReason string                    `json:"overage_disabled_reason,omitempty"`
    OverageStatus        string                     `json:"overage_status,omitempty"`
    RetryAfterS          int                        `json:"retry_after_s,omitempty"`
    Windows              map[string]RatelimitWindow `json:"windows,omitempty"`
}
```

**Step 4: Re-run the generic normalization test**

Run: `go test ./internal/normalize -run TestNormalizerBuildsGenericRecordFromExchange -v`
Expected: PASS

### Task 3: Add `/v1/messages` request/response parsing with gzip support

**Files:**
- Modify: `internal/normalize/normalizer.go`
- Modify: `internal/normalize/normalizer_test.go`

**Step 1: Write the failing messages normalization test**

Add a test named `TestNormalizerExtractsMessagesRecord` that:
- builds a `capture.CompletedExchange` for `POST /v1/messages?beta=true`
- sets the request `Content-Type: application/json`
- puts a request body containing:

```json
{
  "model": "claude-opus-4-6",
  "metadata": {
    "user_id": "{\"account_uuid\":\"3245d789-0f21-4a1f-a16f-1df8cdd2250a\",\"session_id\":\"aa144daf-374f-4cac-b3f7-ba7d4ff0675a\"}"
  }
}
```

- puts a gzipped response body containing:

```json
{
  "id": "msg_123",
  "model": "claude-opus-4-6",
  "usage": {
    "input_tokens": 101,
    "cache_creation_input_tokens": 2000,
    "cache_read_input_tokens": 30000,
    "output_tokens": 42
  }
}
```

- sets `Content-Encoding: gzip` and `Content-Type: application/json`
- asserts the record includes:
  - request model
  - response model
  - session id
  - usage fields
  - ratelimit fields already covered by Task 2

**Step 2: Run the test to verify it fails**

Run: `go test ./internal/normalize -run TestNormalizerExtractsMessagesRecord -v`
Expected: FAIL because request/response body parsing and gzip handling do not exist yet

**Step 3: Implement JSON request/response parsing**

Update `internal/normalize/normalizer.go` to:
- decode request JSON bodies for `/v1/messages`
- decode response JSON bodies for `/v1/messages`
- gunzip response bodies when `Content-Encoding: gzip`
- leave the record generic if the body cannot be decoded

Implement helpers like:

```go
func decodeBody(headers []capture.Header, raw []byte) ([]byte, error)
func parseMessagesRequest(body []byte) (requestModel string, sessionID string)
func parseMessagesResponse(body []byte) (responseModel string, usage Usage)
```

Do not fail normalization on parse errors. Return the generic record with whatever metadata was already available.

**Step 4: Re-run the messages normalization test**

Run: `go test ./internal/normalize -run TestNormalizerExtractsMessagesRecord -v`
Expected: PASS

### Task 4: Add `/v1/messages/count_tokens` support and unknown-endpoint fallback coverage

**Files:**
- Modify: `internal/normalize/normalizer.go`
- Modify: `internal/normalize/normalizer_test.go`

**Step 1: Write the failing count-tokens test**

Add `TestNormalizerExtractsCountTokensRecord` that:
- builds a `capture.CompletedExchange` for `POST /v1/messages/count_tokens?beta=true`
- includes a request body with `"model": "claude-sonnet-4-6"`
- includes a JSON response body with:

```json
{
  "input_tokens": 11642
}
```

- asserts:
  - `request_model == "claude-sonnet-4-6"`
  - `usage.input_tokens == 11642`
  - other usage fields remain zero-valued / omitted

**Step 2: Run the test to verify it fails**

Run: `go test ./internal/normalize -run TestNormalizerExtractsCountTokensRecord -v`
Expected: FAIL because count-tokens parsing does not exist yet

**Step 3: Implement count-tokens parsing**

Update `internal/normalize/normalizer.go` so that for `/v1/messages/count_tokens`:
- request JSON contributes `request_model`
- response JSON contributes `usage.input_tokens`
- ratelimit headers are still parsed the same way

**Step 4: Add fallback-behavior assertions**

Expand `TestNormalizerBuildsGenericRecordFromExchange` so malformed JSON or unsupported encodings:
- do not panic
- still return a generic normalized record
- still preserve header-derived ratelimit information

**Step 5: Re-run the normalize test package**

Run: `go test ./internal/normalize -v`
Expected: PASS

### Task 5: Wire the background pipeline to write normalized records

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `cmd/claude-meter/main.go`

**Step 1: Write the failing app test**

Add a test named `TestAppWritesNormalizedRecordLog` that:
- starts `app.New(...)` with:
  - temp log dir
  - `PlanTier: "max_20x"`
  - `httptest` upstream returning a JSON `/v1/messages` response
  - ratelimit headers including `5h` and `7d`
- sends one request through the app
- calls `app.Close()`
- asserts:
  - one file exists under `normalized/*.jsonl`
  - the normalized log contains one non-empty record
  - the record includes `declared_plan_tier == "max_20x"`
  - the record includes parsed `5h` utilization

**Step 2: Run the test to verify it fails**

Run: `go test ./internal/app -run TestAppWritesNormalizedRecordLog -v`
Expected: FAIL because the app only writes raw exchanges today

**Step 3: Add normalizer wiring**

Update `internal/app/app.go`:
- add `PlanTier string` to `Config`
- create:
  - `storage.NewRawExchangeWriter(filepath.Join(cfg.LogDir, "raw"))`
  - `storage.NewNormalizedRecordWriter(filepath.Join(cfg.LogDir, "normalized"))`
  - `normalize.New(cfg.PlanTier)`
- in the background worker:
  1. write raw
  2. normalize the exchange
  3. write normalized

Make the worker best-effort:
- raw write errors should not crash the app
- normalized write errors should not block or panic

**Step 4: Add the CLI flag**

Update `cmd/claude-meter/main.go` with:
- `--plan-tier` flag, default `unknown`
- startup logging line showing the declared plan tier

Use:

```go
planTier := startFlags.String("plan-tier", "unknown", "declared plan tier for normalized records")
```

Pass it through `app.Config`.

**Step 5: Re-run the app test**

Run: `go test ./internal/app -run TestAppWritesNormalizedRecordLog -v`
Expected: PASS

### Task 6: Update docs and verify analyzer compatibility

**Files:**
- Modify: `README.md`

**Step 1: Update the README**

Document:
- raw logs under `~/.claude-meter/raw/`
- normalized logs under `~/.claude-meter/normalized/`
- `--plan-tier`
- the fact that the current analyzer reads normalized JSONL directly

**Step 2: Manual compatibility smoke test**

Run the proxy:

```bash
go run ./cmd/claude-meter start --port 7735 --plan-tier max_20x
```

Generate one request through Claude Code or a small `curl` request against a test upstream.

Then verify:

```bash
find ~/.claude-meter -maxdepth 2 -type f | sort
tail -n 1 ~/.claude-meter/normalized/*.jsonl
LATEST=$(ls -t ~/.claude-meter/normalized/*.jsonl | head -n1)
python3 analysis/analyze_normalized_log.py "$LATEST" --pretty
```

Expected:
- both raw and normalized JSONL files exist
- the normalized record contains `ratelimit.windows`
- the analyzer runs without schema errors

### Task 7: Final verification

**Files:** none

**Step 1: Run the Go test suite**

Run: `go test ./...`
Expected: PASS

**Step 2: Run a full Go build**

Run: `go build ./...`
Expected: PASS

**Step 3: Run the Python analysis tests**

Run:

```bash
python3 analysis/test_normalize_sniffer_log.py
python3 analysis/test_analyze_normalized_log.py
```

Expected: PASS

**Step 4: Check repo state**

Run: `git status --short`
Expected: only intended implementation changes remain

## Recommended execution approach

Implement this in one vertical slice order:
1. schema + normalized writer
2. generic header-based normalizer
3. `/v1/messages` body parsing
4. `/count_tokens` parsing
5. app wiring and CLI flag

That sequence keeps the proxy stable while making each new layer independently testable.
