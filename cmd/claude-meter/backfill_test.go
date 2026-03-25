package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"claude-meter-proxy/internal/capture"
	"claude-meter-proxy/internal/normalize"
)

func TestRunBackfillNormalizedWritesNormalizedRecords(t *testing.T) {
	t.Parallel()

	logDir := t.TempDir()
	rawDir := filepath.Join(logDir, "raw")
	if err := os.MkdirAll(rawDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	exchange := capture.CompletedExchange{
		ID:               1,
		RequestStartedAt: time.Date(2026, 3, 25, 23, 30, 0, 0, time.UTC),
		ResponseEndedAt:  time.Date(2026, 3, 25, 23, 30, 1, 0, time.UTC),
		DurationMS:       1000,
		Request: capture.RecordedRequest{
			Method: "POST",
			Path:   "/v1/messages?beta=true",
			Headers: []capture.Header{
				{Name: "Content-Type", Value: "application/json"},
			},
			Body: []byte(`{"model":"claude-opus-4-6","metadata":{"user_id":"{\"session_id\":\"backfill-session\"}"}}`),
		},
		Response: capture.RecordedResponse{
			Status: 200,
			Headers: []capture.Header{
				{Name: "Content-Type", Value: "text/event-stream; charset=utf-8"},
				{Name: "Content-Encoding", Value: "gzip"},
			},
			Body: gzipBytesForBackfillTest(t, []byte(
				"event: message_start\n"+
					"data: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-opus-4-6\",\"usage\":{\"input_tokens\":3,\"cache_creation_input_tokens\":220,\"cache_read_input_tokens\":385688,\"output_tokens\":1}}}\n\n"+
					"event: message_delta\n"+
					"data: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":3,\"cache_creation_input_tokens\":220,\"cache_read_input_tokens\":385688,\"output_tokens\":245}}\n\n"+
					"event: message_stop\n"+
					"data: {\"type\":\"message_stop\"}\n\n",
			)),
		},
	}

	rawFile := filepath.Join(rawDir, "2026-03-25.jsonl")
	payload, err := json.Marshal(exchange)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(rawFile, payload, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := runBackfillNormalized([]string{"--log-dir", logDir, "--plan-tier", "max_20x"}); err != nil {
		t.Fatalf("runBackfillNormalized() error = %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(logDir, "normalized", "*.jsonl"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one normalized JSONL file, got %d", len(matches))
	}

	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var record normalize.Record
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if record.DeclaredPlanTier != "max_20x" {
		t.Fatalf("DeclaredPlanTier = %q, want %q", record.DeclaredPlanTier, "max_20x")
	}
	if record.ResponseModel != "claude-opus-4-6" {
		t.Fatalf("ResponseModel = %q, want %q", record.ResponseModel, "claude-opus-4-6")
	}
	if record.Usage.OutputTokens != 245 {
		t.Fatalf("Usage.OutputTokens = %d, want %d", record.Usage.OutputTokens, 245)
	}
}
