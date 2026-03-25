package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-meter-proxy/internal/capture"
)

func TestRawExchangeWriterAppendsJSONL(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writer, err := NewRawExchangeWriter(dir)
	if err != nil {
		t.Fatalf("NewRawExchangeWriter() error = %v", err)
	}

	exchange := capture.CompletedExchange{
		ID:               1,
		RequestStartedAt: time.Date(2026, 3, 25, 14, 0, 0, 0, time.UTC),
		ResponseEndedAt:  time.Date(2026, 3, 25, 14, 0, 1, 0, time.UTC),
		DurationMS:       1000,
		Request: capture.RecordedRequest{
			Method: "POST",
			Path:   "/v1/messages?beta=true",
			Headers: []capture.Header{
				{Name: "content-type", Value: "application/json"},
			},
			Body: []byte(`{"model":"claude-sonnet-4-6"}`),
		},
		Response: capture.RecordedResponse{
			Status: 200,
			Headers: []capture.Header{
				{Name: "content-type", Value: "application/json"},
			},
			Body: []byte(`{"ok":true}`),
		},
	}

	if err := writer.Write(exchange); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one JSONL file, got %d", len(matches))
	}

	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var got capture.CompletedExchange
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if got.ID != exchange.ID {
		t.Fatalf("ID = %d, want %d", got.ID, exchange.ID)
	}
	if got.Request.Path != exchange.Request.Path {
		t.Fatalf("Request.Path = %q, want %q", got.Request.Path, exchange.Request.Path)
	}
	if got.Response.Status != exchange.Response.Status {
		t.Fatalf("Response.Status = %d, want %d", got.Response.Status, exchange.Response.Status)
	}
}

func TestRawExchangeWriterRedactsSensitiveHeaders(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writer, err := NewRawExchangeWriter(dir)
	if err != nil {
		t.Fatalf("NewRawExchangeWriter() error = %v", err)
	}

	exchange := capture.CompletedExchange{
		ID:               2,
		RequestStartedAt: time.Date(2026, 3, 25, 15, 0, 0, 0, time.UTC),
		ResponseEndedAt:  time.Date(2026, 3, 25, 15, 0, 1, 0, time.UTC),
		DurationMS:       1000,
		Request: capture.RecordedRequest{
			Method: "POST",
			Path:   "/v1/messages?beta=true",
			Headers: []capture.Header{
				{Name: "Authorization", Value: "Bearer secret"},
				{Name: "Cookie", Value: "session=secret"},
				{Name: "Content-Type", Value: "application/json"},
			},
			Body: []byte(`{"model":"claude-sonnet-4-6"}`),
		},
		Response: capture.RecordedResponse{
			Status: 200,
			Headers: []capture.Header{
				{Name: "Set-Cookie", Value: "cfuvid=secret"},
				{Name: "Content-Type", Value: "application/json"},
			},
			Body: []byte(`{"ok":true}`),
		},
	}

	if err := writer.Write(exchange); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	got := readSingleExchange(t, dir)

	if value := headerValue(got.Request.Headers, "authorization"); value != "[REDACTED]" {
		t.Fatalf("request authorization header = %q, want %q", value, "[REDACTED]")
	}
	if value := headerValue(got.Request.Headers, "cookie"); value != "[REDACTED]" {
		t.Fatalf("request cookie header = %q, want %q", value, "[REDACTED]")
	}
	if value := headerValue(got.Response.Headers, "set-cookie"); value != "[REDACTED]" {
		t.Fatalf("response set-cookie header = %q, want %q", value, "[REDACTED]")
	}
	if value := headerValue(got.Request.Headers, "content-type"); value != "application/json" {
		t.Fatalf("request content-type header = %q, want %q", value, "application/json")
	}
}

func TestRawExchangeWriterUsesPrivatePermissions(t *testing.T) {
	t.Parallel()

	baseDir := filepath.Join(t.TempDir(), "raw")
	writer, err := NewRawExchangeWriter(baseDir)
	if err != nil {
		t.Fatalf("NewRawExchangeWriter() error = %v", err)
	}

	dirInfo, err := os.Stat(baseDir)
	if err != nil {
		t.Fatalf("Stat(dir) error = %v", err)
	}
	if perms := dirInfo.Mode().Perm(); perms != 0o700 {
		t.Fatalf("dir permissions = %o, want %o", perms, 0o700)
	}

	exchange := capture.CompletedExchange{
		ID:               3,
		RequestStartedAt: time.Date(2026, 3, 25, 16, 0, 0, 0, time.UTC),
		ResponseEndedAt:  time.Date(2026, 3, 25, 16, 0, 1, 0, time.UTC),
		DurationMS:       1000,
		Request:          capture.RecordedRequest{Method: "GET", Path: "/"},
		Response:         capture.RecordedResponse{Status: 404},
	}

	if err := writer.Write(exchange); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(baseDir, "*.jsonl"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one JSONL file, got %d", len(matches))
	}

	fileInfo, err := os.Stat(matches[0])
	if err != nil {
		t.Fatalf("Stat(file) error = %v", err)
	}
	if perms := fileInfo.Mode().Perm(); perms != 0o600 {
		t.Fatalf("file permissions = %o, want %o", perms, 0o600)
	}
}

func readSingleExchange(t *testing.T, dir string) capture.CompletedExchange {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one JSONL file, got %d", len(matches))
	}

	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var got capture.CompletedExchange
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	return got
}

func headerValue(headers []capture.Header, name string) string {
	for _, header := range headers {
		if strings.EqualFold(header.Name, name) {
			return header.Value
		}
	}

	return ""
}
