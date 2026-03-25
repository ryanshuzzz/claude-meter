package main

import (
	"bytes"
	"compress/gzip"
	"testing"
)

func gzipBytesForBackfillTest(t *testing.T, payload []byte) []byte {
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
