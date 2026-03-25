package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"claude-meter-proxy/internal/capture"
)

const redactedHeaderValue = "[REDACTED]"

var sensitiveHeaderNames = map[string]struct{}{
	"authorization":       {},
	"proxy-authorization": {},
	"cookie":              {},
	"set-cookie":          {},
	"x-api-key":           {},
	"x-auth-token":        {},
}

type RawExchangeWriter struct {
	dir string
}

func NewRawExchangeWriter(dir string) (*RawExchangeWriter, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create raw log dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("chmod raw log dir: %w", err)
	}

	return &RawExchangeWriter{dir: dir}, nil
}

func (w *RawExchangeWriter) Write(exchange capture.CompletedExchange) error {
	path := filepath.Join(w.dir, exchange.RequestStartedAt.Format("2006-01-02")+".jsonl")

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open raw log file: %w", err)
	}
	defer file.Close()

	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod raw log file: %w", err)
	}

	payload, err := json.Marshal(sanitizeExchange(exchange))
	if err != nil {
		return fmt.Errorf("marshal exchange: %w", err)
	}

	payload = append(payload, '\n')
	if _, err := file.Write(payload); err != nil {
		return fmt.Errorf("write exchange: %w", err)
	}

	return nil
}

func DailyRawDir(baseDir string, now time.Time) string {
	return filepath.Join(baseDir, "raw", now.Format("2006-01-02"))
}

func sanitizeExchange(exchange capture.CompletedExchange) capture.CompletedExchange {
	sanitized := exchange
	sanitized.Request.Headers = sanitizeHeaders(exchange.Request.Headers)
	sanitized.Response.Headers = sanitizeHeaders(exchange.Response.Headers)

	return sanitized
}

func sanitizeHeaders(headers []capture.Header) []capture.Header {
	if len(headers) == 0 {
		return nil
	}

	sanitized := make([]capture.Header, len(headers))
	for i, header := range headers {
		sanitized[i] = header
		if _, ok := sensitiveHeaderNames[strings.ToLower(strings.TrimSpace(header.Name))]; ok {
			sanitized[i].Value = redactedHeaderValue
		}
	}

	return sanitized
}
