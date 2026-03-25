package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
