package normalize

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"claude-meter-proxy/internal/capture"
)

func TestParseSSEEventsExtractsCompleteEventsFromGzippedStream(t *testing.T) {
	t.Parallel()

	stream := stringsToBytes(
		"event: message_start\n" +
			"data: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-opus-4-6\",\"usage\":{\"input_tokens\":3,\"cache_creation_input_tokens\":220,\"cache_read_input_tokens\":385688,\"output_tokens\":1}}}\n\n" +
			"event: content_block_start\n" +
			"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
			"event: content_block_delta\n" +
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n" +
			"event: message_delta\n" +
			"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"input_tokens\":3,\"cache_creation_input_tokens\":220,\"cache_read_input_tokens\":385688,\"output_tokens\":245}}\n\n" +
			"event: message_stop\n" +
			"data: {\"type\":\"message_stop\"}\n\n",
	)

	events, err := parseSSEEvents(gzipBytes(t, stream), sseHeaders())
	if err != nil {
		t.Fatalf("parseSSEEvents() error = %v", err)
	}

	if len(events) != 5 {
		t.Fatalf("len(events) = %d, want %d", len(events), 5)
	}
	if events[0].Event != "message_start" {
		t.Fatalf("events[0].Event = %q, want %q", events[0].Event, "message_start")
	}
	if string(events[0].Data) == "" {
		t.Fatal("events[0].Data should not be empty")
	}
	if events[4].Event != "message_stop" {
		t.Fatalf("events[4].Event = %q, want %q", events[4].Event, "message_stop")
	}
}

func TestParseSSEEventsToleratesTrailingTruncatedGzip(t *testing.T) {
	t.Parallel()

	stream := stringsToBytes(
		"event: message_start\n" +
			"data: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-opus-4-6\",\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}}\n\n" +
			"event: message_delta\n" +
			"data: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":3,\"output_tokens\":245}}\n\n" +
			"event: message_stop\n" +
			"data: {\"type\":\"message_stop\"}\n\n",
	)

	compressed := gzipBytes(t, stream)
	compressed = compressed[:len(compressed)-8]

	events, err := parseSSEEvents(compressed, sseHeaders())
	if err != nil {
		t.Fatalf("parseSSEEvents() error = %v", err)
	}

	if len(events) < 2 {
		t.Fatalf("len(events) = %d, want at least %d", len(events), 2)
	}
	if events[0].Event != "message_start" {
		t.Fatalf("events[0].Event = %q, want %q", events[0].Event, "message_start")
	}
	if events[1].Event != "message_delta" {
		t.Fatalf("events[1].Event = %q, want %q", events[1].Event, "message_delta")
	}
}

func TestNormalizerParsesTextFixtureStream(t *testing.T) {
	t.Parallel()

	record := normalizeFixtureRecord(t, "messages_sse.txt")

	if record.ResponseModel != "claude-opus-4-6" {
		t.Fatalf("ResponseModel = %q, want %q", record.ResponseModel, "claude-opus-4-6")
	}
	if record.Usage.OutputTokens != 245 {
		t.Fatalf("Usage.OutputTokens = %d, want %d", record.Usage.OutputTokens, 245)
	}
}

func TestNormalizerParsesToolUseFixtureStream(t *testing.T) {
	t.Parallel()

	record := normalizeFixtureRecord(t, "messages_tool_use_sse.txt")

	if record.ResponseModel != "claude-opus-4-6" {
		t.Fatalf("ResponseModel = %q, want %q", record.ResponseModel, "claude-opus-4-6")
	}
	if record.Usage.OutputTokens != 89 {
		t.Fatalf("Usage.OutputTokens = %d, want %d", record.Usage.OutputTokens, 89)
	}
	if record.Usage.CacheCreationInputTokens != 81313 {
		t.Fatalf("Usage.CacheCreationInputTokens = %d, want %d", record.Usage.CacheCreationInputTokens, 81313)
	}
}

func gzipBytes(t *testing.T, payload []byte) []byte {
	t.Helper()

	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	return buffer.Bytes()
}

func sseHeaders() []capture.Header {
	return []capture.Header{
		{Name: "Content-Type", Value: "text/event-stream; charset=utf-8"},
		{Name: "Content-Encoding", Value: "gzip"},
	}
}

func stringsToBytes(value string) []byte {
	return []byte(value)
}

func normalizeFixtureRecord(t *testing.T, fixtureName string) Record {
	t.Helper()

	payload, err := os.ReadFile(filepath.Join("testdata", fixtureName))
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", fixtureName, err)
	}

	exchange := capture.CompletedExchange{
		ID:               99,
		RequestStartedAt: time.Date(2026, 3, 25, 23, 0, 0, 0, time.UTC),
		ResponseEndedAt:  time.Date(2026, 3, 25, 23, 0, 1, 0, time.UTC),
		DurationMS:       1000,
		Request: capture.RecordedRequest{
			Method: "POST",
			Path:   "/v1/messages?beta=true",
			Headers: []capture.Header{
				{Name: "Content-Type", Value: "application/json"},
			},
			Body: []byte(`{"model":"claude-opus-4-6","metadata":{"user_id":"{\"session_id\":\"fixture-session\"}"}}`),
		},
		Response: capture.RecordedResponse{
			Status: 200,
			Headers: []capture.Header{
				{Name: "Content-Type", Value: "text/event-stream; charset=utf-8"},
				{Name: "Content-Encoding", Value: "gzip"},
			},
			Body: gzipBytes(t, payload),
		},
	}

	return New("max_20x").Normalize(exchange)
}
