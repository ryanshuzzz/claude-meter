package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// BlockedEvent is written to the normalized JSONL log when a request is blocked
// by the rate limiter before being forwarded upstream.
type BlockedEvent struct {
	Type               string    `json:"type"`
	Ts                 time.Time `json:"ts"`
	Window             string    `json:"window"`
	AccountUtilization float64   `json:"account_utilization"`
	InstanceLimit      float64   `json:"instance_limit"`
	RetryAfter         time.Time `json:"retry_after"`
	RequestPath        string    `json:"request_path"`
	RequestModel       string    `json:"request_model,omitempty"`
}

// WarnEvent is written to the normalized JSONL log when account utilization
// crosses the warn_threshold for a window.
type WarnEvent struct {
	Type               string    `json:"type"`
	Ts                 time.Time `json:"ts"`
	Window             string    `json:"window"`
	AccountUtilization float64   `json:"account_utilization"`
	InstanceLimit      float64   `json:"instance_limit"`
	HeadroomRemaining  float64   `json:"headroom_remaining"`
}

// WriteEvent writes an arbitrary JSON-serializable event to the normalized JSONL
// file for the given date. It follows the same file layout as Write().
func (w *NormalizedRecordWriter) WriteEvent(ts time.Time, v any) error {
	path := filepath.Join(w.dir, ts.Format("2006-01-02")+".jsonl")

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open normalized log file: %w", err)
	}
	defer file.Close()

	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod normalized log file: %w", err)
	}

	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	payload = append(payload, '\n')
	if _, err := file.Write(payload); err != nil {
		return fmt.Errorf("write event: %w", err)
	}

	return nil
}

// WriteBlockedEvent is a convenience wrapper for writing a BlockedEvent.
func (w *NormalizedRecordWriter) WriteBlockedEvent(e BlockedEvent) error {
	return w.WriteEvent(e.Ts, e)
}

// WriteWarnEvent is a convenience wrapper for writing a WarnEvent.
func (w *NormalizedRecordWriter) WriteWarnEvent(e WarnEvent) error {
	return w.WriteEvent(e.Ts, e)
}
