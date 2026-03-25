package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"claude-meter-proxy/internal/normalize"
)

func TestNormalizedRecordWriterAppendsJSONL(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "normalized")
	writer, err := NewNormalizedRecordWriter(dir)
	if err != nil {
		t.Fatalf("NewNormalizedRecordWriter() error = %v", err)
	}

	record := normalize.Record{
		ID:                42,
		RequestTimestamp:  time.Date(2026, 3, 25, 21, 56, 59, 0, time.UTC),
		ResponseTimestamp: time.Date(2026, 3, 25, 21, 57, 0, 0, time.UTC),
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
			InputTokens:  1,
			OutputTokens: 8,
		},
		Ratelimit: normalize.Ratelimit{
			Status:                "allowed",
			RepresentativeClaim:   "five_hour",
			OverageDisabledReason: "out_of_credits",
			Windows: map[string]normalize.RatelimitWindow{
				"5h": {Status: "allowed", Utilization: 0.10, ResetTS: 1774490400},
				"7d": {Status: "allowed", Utilization: 0.61, ResetTS: 1774580400},
			},
		},
	}

	if err := writer.Write(record); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one JSONL file, got %d", len(matches))
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat(dir) error = %v", err)
	}
	if perms := dirInfo.Mode().Perm(); perms != 0o700 {
		t.Fatalf("dir permissions = %o, want %o", perms, 0o700)
	}

	fileInfo, err := os.Stat(matches[0])
	if err != nil {
		t.Fatalf("Stat(file) error = %v", err)
	}
	if perms := fileInfo.Mode().Perm(); perms != 0o600 {
		t.Fatalf("file permissions = %o, want %o", perms, 0o600)
	}

	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var got normalize.Record
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if got.ID != record.ID {
		t.Fatalf("ID = %d, want %d", got.ID, record.ID)
	}
	if got.DeclaredPlanTier != record.DeclaredPlanTier {
		t.Fatalf("DeclaredPlanTier = %q, want %q", got.DeclaredPlanTier, record.DeclaredPlanTier)
	}
	if got.RequestID != record.RequestID {
		t.Fatalf("RequestID = %q, want %q", got.RequestID, record.RequestID)
	}
	if got.Ratelimit.Windows["5h"].Utilization != record.Ratelimit.Windows["5h"].Utilization {
		t.Fatalf("5h utilization = %v, want %v", got.Ratelimit.Windows["5h"].Utilization, record.Ratelimit.Windows["5h"].Utilization)
	}
}
