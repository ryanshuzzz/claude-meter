package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claude-meter-proxy/internal/normalize"
)

func TestAppWritesRawExchangeLog(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}

	logDir := t.TempDir()
	app, err := New(Config{
		UpstreamBaseURL: upstreamURL,
		LogDir:          logDir,
		QueueSize:       4,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/messages", strings.NewReader("hello"))
	recorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, req)

	if err := app.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(logDir, "raw", "*.jsonl"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one raw JSONL file, got %d", len(matches))
	}

	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected raw log file to be non-empty")
	}
}

func TestAppWritesNormalizedRecordLog(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Request-Id", "req_norm_123")
		w.Header().Set("Anthropic-Ratelimit-Unified-Status", "allowed")
		w.Header().Set("Anthropic-Ratelimit-Unified-Representative-Claim", "five_hour")
		w.Header().Set("Anthropic-Ratelimit-Unified-5h-Status", "allowed")
		w.Header().Set("Anthropic-Ratelimit-Unified-5h-Utilization", "0.12")
		w.Header().Set("Anthropic-Ratelimit-Unified-7d-Status", "allowed")
		w.Header().Set("Anthropic-Ratelimit-Unified-7d-Utilization", "0.62")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"model":"claude-haiku-4-5-20251001",
			"usage":{
				"input_tokens":5,
				"output_tokens":7
			}
		}`))
	}))
	defer upstream.Close()

	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}

	logDir := t.TempDir()
	app, err := New(Config{
		UpstreamBaseURL: upstreamURL,
		LogDir:          logDir,
		QueueSize:       4,
		PlanTier:        "max_20x",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"http://proxy.local/v1/messages?beta=true",
		strings.NewReader(`{
			"model":"claude-haiku-4-5-20251001",
			"metadata":{
				"user_id":"{\"session_id\":\"session_123\"}"
			}
		}`),
	)
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, req)

	if err := app.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
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

	var got normalize.Record
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if got.DeclaredPlanTier != "max_20x" {
		t.Fatalf("DeclaredPlanTier = %q, want %q", got.DeclaredPlanTier, "max_20x")
	}
	if got.Ratelimit.Windows["5h"].Utilization != 0.12 {
		t.Fatalf("5h utilization = %v, want %v", got.Ratelimit.Windows["5h"].Utilization, 0.12)
	}
	if got.SessionID != "session_123" {
		t.Fatalf("SessionID = %q, want %q", got.SessionID, "session_123")
	}
}
