package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteBlockedEvent(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "normalized")
	writer, err := NewNormalizedRecordWriter(dir)
	if err != nil {
		t.Fatalf("NewNormalizedRecordWriter() error = %v", err)
	}

	ts := time.Date(2026, 3, 27, 18, 0, 0, 0, time.UTC)
	event := BlockedEvent{
		Type:               "blocked",
		Ts:                 ts,
		Window:             "5h",
		AccountUtilization: 0.251,
		InstanceLimit:      0.25,
		RetryAfter:         time.Date(2026, 3, 27, 20, 0, 0, 0, time.UTC),
		RequestPath:        "/v1/messages",
		RequestModel:       "claude-opus-4-6",
	}

	if err := writer.WriteBlockedEvent(event); err != nil {
		t.Fatalf("WriteBlockedEvent() error = %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected one JSONL file, got %d files (err: %v)", len(matches), err)
	}

	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var got BlockedEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if got.Type != "blocked" {
		t.Errorf("type = %q, want %q", got.Type, "blocked")
	}
	if got.Window != "5h" {
		t.Errorf("window = %q, want %q", got.Window, "5h")
	}
	if got.AccountUtilization != 0.251 {
		t.Errorf("account_utilization = %g, want 0.251", got.AccountUtilization)
	}
	if got.RequestModel != "claude-opus-4-6" {
		t.Errorf("request_model = %q, want %q", got.RequestModel, "claude-opus-4-6")
	}
}

func TestWriteWarnEvent(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "normalized")
	writer, err := NewNormalizedRecordWriter(dir)
	if err != nil {
		t.Fatalf("NewNormalizedRecordWriter() error = %v", err)
	}

	ts := time.Date(2026, 3, 27, 17, 0, 0, 0, time.UTC)
	event := WarnEvent{
		Type:               "warn",
		Ts:                 ts,
		Window:             "7d",
		AccountUtilization: 0.21,
		InstanceLimit:      0.25,
		HeadroomRemaining:  0.04,
	}

	if err := writer.WriteWarnEvent(event); err != nil {
		t.Fatalf("WriteWarnEvent() error = %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected one JSONL file, got %d (err: %v)", len(matches), err)
	}

	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var got WarnEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if got.Type != "warn" {
		t.Errorf("type = %q, want %q", got.Type, "warn")
	}
	if got.HeadroomRemaining != 0.04 {
		t.Errorf("headroom_remaining = %g, want 0.04", got.HeadroomRemaining)
	}
}

func TestWriteEventDoesNotBreakNormalizedRecords(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "normalized")
	writer, err := NewNormalizedRecordWriter(dir)
	if err != nil {
		t.Fatalf("NewNormalizedRecordWriter() error = %v", err)
	}

	ts := time.Date(2026, 3, 27, 18, 0, 0, 0, time.UTC)

	// Write a blocked event then a warn event to the same file.
	blocked := BlockedEvent{Type: "blocked", Ts: ts, Window: "5h", AccountUtilization: 0.30, InstanceLimit: 0.25}
	warn := WarnEvent{Type: "warn", Ts: ts, Window: "5h", AccountUtilization: 0.21, InstanceLimit: 0.25, HeadroomRemaining: 0.04}

	if err := writer.WriteBlockedEvent(blocked); err != nil {
		t.Fatalf("WriteBlockedEvent() error = %v", err)
	}
	if err := writer.WriteWarnEvent(warn); err != nil {
		t.Fatalf("WriteWarnEvent() error = %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected one JSONL file, got %d (err: %v)", len(matches), err)
	}

	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSONL lines, got %d", len(lines))
	}

	// Each line should be valid JSON.
	for i, line := range lines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("line %d is not valid JSON: %v", i+1, err)
		}
	}
}
