package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"claude-meter-proxy/internal/normalize"
)

type NormalizedRecordWriter struct {
	dir string
}

func NewNormalizedRecordWriter(dir string) (*NormalizedRecordWriter, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create normalized log dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("chmod normalized log dir: %w", err)
	}

	return &NormalizedRecordWriter{dir: dir}, nil
}

func (w *NormalizedRecordWriter) Write(record normalize.Record) error {
	path := filepath.Join(w.dir, record.RequestTimestamp.Format("2006-01-02")+".jsonl")

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open normalized log file: %w", err)
	}
	defer file.Close()

	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod normalized log file: %w", err)
	}

	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal normalized record: %w", err)
	}

	payload = append(payload, '\n')
	if _, err := file.Write(payload); err != nil {
		return fmt.Errorf("write normalized record: %w", err)
	}

	return nil
}
