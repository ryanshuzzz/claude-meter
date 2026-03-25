package normalize

import (
	"testing"
	"time"

	"claude-meter-proxy/internal/capture"
)

func TestNormalizerBuildsGenericRecordFromExchange(t *testing.T) {
	t.Parallel()

	exchange := capture.CompletedExchange{
		ID:               7,
		RequestStartedAt: time.Date(2026, 3, 25, 21, 56, 59, 0, time.UTC),
		ResponseEndedAt:  time.Date(2026, 3, 25, 21, 57, 0, 0, time.UTC),
		DurationMS:       491,
		Request: capture.RecordedRequest{
			Method: "POST",
			Path:   "/v1/other",
			Headers: []capture.Header{
				{Name: "Content-Type", Value: "application/json"},
			},
			Body: []byte(`{"ignored":true}`),
		},
		Response: capture.RecordedResponse{
			Status: 200,
			Headers: []capture.Header{
				{Name: "Request-Id", Value: "req_123"},
				{Name: "Retry-After", Value: "2094"},
				{Name: "Anthropic-Ratelimit-Unified-Status", Value: "allowed"},
				{Name: "Anthropic-Ratelimit-Unified-Representative-Claim", Value: "five_hour"},
				{Name: "Anthropic-Ratelimit-Unified-5h-Status", Value: "allowed"},
				{Name: "Anthropic-Ratelimit-Unified-5h-Utilization", Value: "0.10"},
				{Name: "Anthropic-Ratelimit-Unified-5h-Reset", Value: "1774490400"},
				{Name: "Anthropic-Ratelimit-Unified-7d-Status", Value: "allowed"},
				{Name: "Anthropic-Ratelimit-Unified-7d-Utilization", Value: "0.61"},
			},
			Body: []byte(`{"ok":true}`),
		},
	}

	record := New("max_20x").Normalize(exchange)

	if record.ID != exchange.ID {
		t.Fatalf("ID = %d, want %d", record.ID, exchange.ID)
	}
	if !record.RequestTimestamp.Equal(exchange.RequestStartedAt) {
		t.Fatalf("RequestTimestamp = %v, want %v", record.RequestTimestamp, exchange.RequestStartedAt)
	}
	if !record.ResponseTimestamp.Equal(exchange.ResponseEndedAt) {
		t.Fatalf("ResponseTimestamp = %v, want %v", record.ResponseTimestamp, exchange.ResponseEndedAt)
	}
	if record.Method != exchange.Request.Method {
		t.Fatalf("Method = %q, want %q", record.Method, exchange.Request.Method)
	}
	if record.Path != exchange.Request.Path {
		t.Fatalf("Path = %q, want %q", record.Path, exchange.Request.Path)
	}
	if record.Status != exchange.Response.Status {
		t.Fatalf("Status = %d, want %d", record.Status, exchange.Response.Status)
	}
	if record.LatencyMS != exchange.DurationMS {
		t.Fatalf("LatencyMS = %d, want %d", record.LatencyMS, exchange.DurationMS)
	}
	if record.DeclaredPlanTier != "max_20x" {
		t.Fatalf("DeclaredPlanTier = %q, want %q", record.DeclaredPlanTier, "max_20x")
	}
	if record.RequestID != "req_123" {
		t.Fatalf("RequestID = %q, want %q", record.RequestID, "req_123")
	}
	if record.RequestModel != "" {
		t.Fatalf("RequestModel = %q, want empty", record.RequestModel)
	}
	if record.SessionID != "" {
		t.Fatalf("SessionID = %q, want empty", record.SessionID)
	}
	if record.Ratelimit.Status != "allowed" {
		t.Fatalf("Ratelimit.Status = %q, want %q", record.Ratelimit.Status, "allowed")
	}
	if record.Ratelimit.RepresentativeClaim != "five_hour" {
		t.Fatalf("Ratelimit.RepresentativeClaim = %q, want %q", record.Ratelimit.RepresentativeClaim, "five_hour")
	}
	if record.Ratelimit.RetryAfterS != 2094 {
		t.Fatalf("Ratelimit.RetryAfterS = %d, want %d", record.Ratelimit.RetryAfterS, 2094)
	}
	if got := record.Ratelimit.Windows["5h"].Utilization; got != 0.10 {
		t.Fatalf("5h utilization = %v, want %v", got, 0.10)
	}
	if got := record.Ratelimit.Windows["5h"].ResetTS; got != 1774490400 {
		t.Fatalf("5h reset = %d, want %d", got, 1774490400)
	}
	if got := record.Ratelimit.Windows["7d"].Utilization; got != 0.61 {
		t.Fatalf("7d utilization = %v, want %v", got, 0.61)
	}
}

func TestNormalizerExtractsMessagesRecord(t *testing.T) {
	t.Parallel()

	responseBody := gzipBytes(t, []byte(
		"event: message_start\n"+
			"data: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-opus-4-6\",\"usage\":{\"input_tokens\":3,\"cache_creation_input_tokens\":220,\"cache_read_input_tokens\":385688,\"output_tokens\":1}}}\n\n"+
			"event: content_block_start\n"+
			"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"+
			"event: content_block_delta\n"+
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n"+
			"event: message_delta\n"+
			"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"input_tokens\":3,\"cache_creation_input_tokens\":220,\"cache_read_input_tokens\":385688,\"output_tokens\":245}}\n\n"+
			"event: message_stop\n"+
			"data: {\"type\":\"message_stop\"}\n\n",
	))

	exchange := capture.CompletedExchange{
		ID:               8,
		RequestStartedAt: time.Date(2026, 3, 25, 22, 1, 0, 0, time.UTC),
		ResponseEndedAt:  time.Date(2026, 3, 25, 22, 1, 1, 0, time.UTC),
		DurationMS:       350,
		Request: capture.RecordedRequest{
			Method: "POST",
			Path:   "/v1/messages?beta=true",
			Headers: []capture.Header{
				{Name: "Content-Type", Value: "application/json"},
			},
			Body: []byte(`{
				"model": "claude-opus-4-6",
				"metadata": {
					"user_id": "{\"account_uuid\":\"3245d789-0f21-4a1f-a16f-1df8cdd2250a\",\"session_id\":\"aa144daf-374f-4cac-b3f7-ba7d4ff0675a\"}"
				}
			}`),
		},
		Response: capture.RecordedResponse{
			Status: 200,
			Headers: []capture.Header{
				{Name: "Content-Type", Value: "text/event-stream; charset=utf-8"},
				{Name: "Content-Encoding", Value: "gzip"},
			},
			Body: responseBody,
		},
	}

	record := New("max_20x").Normalize(exchange)

	if record.RequestModel != "claude-opus-4-6" {
		t.Fatalf("RequestModel = %q, want %q", record.RequestModel, "claude-opus-4-6")
	}
	if record.ResponseModel != "claude-opus-4-6" {
		t.Fatalf("ResponseModel = %q, want %q", record.ResponseModel, "claude-opus-4-6")
	}
	if record.SessionID != "aa144daf-374f-4cac-b3f7-ba7d4ff0675a" {
		t.Fatalf("SessionID = %q, want %q", record.SessionID, "aa144daf-374f-4cac-b3f7-ba7d4ff0675a")
	}
	if record.Usage.InputTokens != 3 {
		t.Fatalf("Usage.InputTokens = %d, want %d", record.Usage.InputTokens, 3)
	}
	if record.Usage.CacheCreationInputTokens != 220 {
		t.Fatalf("Usage.CacheCreationInputTokens = %d, want %d", record.Usage.CacheCreationInputTokens, 220)
	}
	if record.Usage.CacheReadInputTokens != 385688 {
		t.Fatalf("Usage.CacheReadInputTokens = %d, want %d", record.Usage.CacheReadInputTokens, 385688)
	}
	if record.Usage.OutputTokens != 245 {
		t.Fatalf("Usage.OutputTokens = %d, want %d", record.Usage.OutputTokens, 245)
	}
}

func TestNormalizerUsesMessageStartUsageWhenMessageDeltaMissing(t *testing.T) {
	t.Parallel()

	responseBody := gzipBytes(t, []byte(
		"event: message_start\n"+
			"data: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-sonnet-4-6\",\"usage\":{\"input_tokens\":7,\"cache_creation_input_tokens\":50,\"cache_read_input_tokens\":1000,\"output_tokens\":2}}}\n\n"+
			"event: content_block_start\n"+
			"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"+
			"event: message_stop\n"+
			"data: {\"type\":\"message_stop\"}\n\n",
	))

	exchange := capture.CompletedExchange{
		ID:               11,
		RequestStartedAt: time.Date(2026, 3, 25, 22, 7, 0, 0, time.UTC),
		ResponseEndedAt:  time.Date(2026, 3, 25, 22, 7, 1, 0, time.UTC),
		DurationMS:       200,
		Request: capture.RecordedRequest{
			Method: "POST",
			Path:   "/v1/messages?beta=true",
			Headers: []capture.Header{
				{Name: "Content-Type", Value: "application/json"},
			},
			Body: []byte(`{"model":"claude-sonnet-4-6","metadata":{"user_id":"{\"session_id\":\"session-start-only\"}"}}`),
		},
		Response: capture.RecordedResponse{
			Status: 200,
			Headers: []capture.Header{
				{Name: "Content-Type", Value: "text/event-stream; charset=utf-8"},
				{Name: "Content-Encoding", Value: "gzip"},
			},
			Body: responseBody,
		},
	}

	record := New("max_20x").Normalize(exchange)

	if record.ResponseModel != "claude-sonnet-4-6" {
		t.Fatalf("ResponseModel = %q, want %q", record.ResponseModel, "claude-sonnet-4-6")
	}
	if record.Usage.InputTokens != 7 {
		t.Fatalf("Usage.InputTokens = %d, want %d", record.Usage.InputTokens, 7)
	}
	if record.Usage.OutputTokens != 2 {
		t.Fatalf("Usage.OutputTokens = %d, want %d", record.Usage.OutputTokens, 2)
	}
}

func TestNormalizerExtractsMessagesRecordFromPartialSSEStream(t *testing.T) {
	t.Parallel()

	responseBody := gzipBytes(t, []byte(
		"event: message_start\n"+
			"data: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-opus-4-6\",\"usage\":{\"input_tokens\":3,\"cache_creation_input_tokens\":220,\"cache_read_input_tokens\":385688,\"output_tokens\":1}}}\n\n"+
			"event: message_delta\n"+
			"data: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":3,\"cache_creation_input_tokens\":220,\"cache_read_input_tokens\":385688,\"output_tokens\":245}}\n\n"+
			"event: message_stop\n"+
			"data: {\"type\":\"message_stop\"}\n\n",
	))
	responseBody = responseBody[:len(responseBody)-8]

	exchange := capture.CompletedExchange{
		ID:               12,
		RequestStartedAt: time.Date(2026, 3, 25, 22, 8, 0, 0, time.UTC),
		ResponseEndedAt:  time.Date(2026, 3, 25, 22, 8, 1, 0, time.UTC),
		DurationMS:       220,
		Request: capture.RecordedRequest{
			Method: "POST",
			Path:   "/v1/messages?beta=true",
			Headers: []capture.Header{
				{Name: "Content-Type", Value: "application/json"},
			},
			Body: []byte(`{"model":"claude-opus-4-6","metadata":{"user_id":"{\"session_id\":\"session-partial\"}"}}`),
		},
		Response: capture.RecordedResponse{
			Status: 200,
			Headers: []capture.Header{
				{Name: "Content-Type", Value: "text/event-stream; charset=utf-8"},
				{Name: "Content-Encoding", Value: "gzip"},
			},
			Body: responseBody,
		},
	}

	record := New("max_20x").Normalize(exchange)

	if record.ResponseModel != "claude-opus-4-6" {
		t.Fatalf("ResponseModel = %q, want %q", record.ResponseModel, "claude-opus-4-6")
	}
	if record.Usage.OutputTokens != 245 {
		t.Fatalf("Usage.OutputTokens = %d, want %d", record.Usage.OutputTokens, 245)
	}
}

func TestNormalizerExtractsCountTokensRecord(t *testing.T) {
	t.Parallel()

	exchange := capture.CompletedExchange{
		ID:               9,
		RequestStartedAt: time.Date(2026, 3, 25, 22, 5, 0, 0, time.UTC),
		ResponseEndedAt:  time.Date(2026, 3, 25, 22, 5, 1, 0, time.UTC),
		DurationMS:       220,
		Request: capture.RecordedRequest{
			Method: "POST",
			Path:   "/v1/messages/count_tokens?beta=true",
			Headers: []capture.Header{
				{Name: "Content-Type", Value: "application/json"},
			},
			Body: []byte(`{"model":"claude-sonnet-4-6"}`),
		},
		Response: capture.RecordedResponse{
			Status: 200,
			Headers: []capture.Header{
				{Name: "Content-Type", Value: "application/json"},
			},
			Body: []byte(`{"input_tokens":11642}`),
		},
	}

	record := New("max_20x").Normalize(exchange)

	if record.RequestModel != "claude-sonnet-4-6" {
		t.Fatalf("RequestModel = %q, want %q", record.RequestModel, "claude-sonnet-4-6")
	}
	if record.Usage.InputTokens != 11642 {
		t.Fatalf("Usage.InputTokens = %d, want %d", record.Usage.InputTokens, 11642)
	}
	if record.Usage.OutputTokens != 0 {
		t.Fatalf("Usage.OutputTokens = %d, want %d", record.Usage.OutputTokens, 0)
	}
}

func TestNormalizerFallsBackToGenericRecordWhenBodyParsingFails(t *testing.T) {
	t.Parallel()

	exchange := capture.CompletedExchange{
		ID:               10,
		RequestStartedAt: time.Date(2026, 3, 25, 22, 6, 0, 0, time.UTC),
		ResponseEndedAt:  time.Date(2026, 3, 25, 22, 6, 1, 0, time.UTC),
		DurationMS:       180,
		Request: capture.RecordedRequest{
			Method: "POST",
			Path:   "/v1/messages?beta=true",
			Headers: []capture.Header{
				{Name: "Content-Type", Value: "application/json"},
			},
			Body: []byte(`{"model":`),
		},
		Response: capture.RecordedResponse{
			Status: 200,
			Headers: []capture.Header{
				{Name: "Request-Id", Value: "req_fallback"},
				{Name: "Anthropic-Ratelimit-Unified-5h-Utilization", Value: "0.33"},
				{Name: "Content-Type", Value: "application/json"},
			},
			Body: []byte(`{"usage":`),
		},
	}

	record := New("max_20x").Normalize(exchange)

	if record.RequestID != "req_fallback" {
		t.Fatalf("RequestID = %q, want %q", record.RequestID, "req_fallback")
	}
	if got := record.Ratelimit.Windows["5h"].Utilization; got != 0.33 {
		t.Fatalf("5h utilization = %v, want %v", got, 0.33)
	}
	if record.RequestModel != "" {
		t.Fatalf("RequestModel = %q, want empty", record.RequestModel)
	}
	if record.Usage.InputTokens != 0 {
		t.Fatalf("Usage.InputTokens = %d, want %d", record.Usage.InputTokens, 0)
	}
}
