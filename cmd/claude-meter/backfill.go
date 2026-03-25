package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"claude-meter-proxy/internal/capture"
	"claude-meter-proxy/internal/normalize"
	"claude-meter-proxy/internal/storage"
)

func runBackfillNormalized(args []string) error {
	backfillFlags := flag.NewFlagSet("backfill-normalized", flag.ContinueOnError)
	logDir := backfillFlags.String("log-dir", defaultLogDir(), "base log directory")
	planTier := backfillFlags.String("plan-tier", "unknown", "declared plan tier for normalized records")
	if err := backfillFlags.Parse(args); err != nil {
		return err
	}

	baseLogDir := expandHome(*logDir)
	writer, err := storage.NewNormalizedRecordWriter(filepath.Join(baseLogDir, "normalized"))
	if err != nil {
		return err
	}
	normalizer := normalize.New(*planTier)

	rawFiles, err := filepath.Glob(filepath.Join(baseLogDir, "raw", "*.jsonl"))
	if err != nil {
		return err
	}
	sort.Strings(rawFiles)

	for _, path := range rawFiles {
		if err := backfillFile(path, writer, normalizer); err != nil {
			return fmt.Errorf("backfill %s: %w", path, err)
		}
	}

	return nil
}

func backfillFile(path string, writer *storage.NormalizedRecordWriter, normalizer *normalize.Normalizer) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	for {
		var exchange capture.CompletedExchange
		if err := decoder.Decode(&exchange); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		if err := writer.Write(normalizer.Normalize(exchange)); err != nil {
			return err
		}
	}
}
