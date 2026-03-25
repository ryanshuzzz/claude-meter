package normalize

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"

	"claude-meter-proxy/internal/capture"
)

type SSEEvent struct {
	Event string
	Data  []byte
}

func parseSSEEvents(raw []byte, headers []capture.Header) ([]SSEEvent, error) {
	decoded, err := decodeBodyBestEffort(raw, headers)
	if err != nil {
		return nil, err
	}

	return splitSSEEvents(decoded), nil
}

func decodeBodyBestEffort(raw []byte, headers []capture.Header) ([]byte, error) {
	if !strings.EqualFold(headerValue(headers, "content-encoding"), "gzip") {
		return raw, nil
	}

	reader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	decoded, readErr := io.ReadAll(reader)
	if readErr != nil && len(decoded) == 0 {
		return nil, readErr
	}

	return decoded, nil
}

func splitSSEEvents(decoded []byte) []SSEEvent {
	normalized := strings.ReplaceAll(string(decoded), "\r\n", "\n")
	blocks := strings.Split(normalized, "\n\n")
	events := make([]SSEEvent, 0, len(blocks))
	for _, block := range blocks {
		event, ok := parseSSEEventBlock(block)
		if ok {
			events = append(events, event)
		}
	}

	return events
}

func parseSSEEventBlock(block string) (SSEEvent, bool) {
	lines := strings.Split(block, "\n")
	var eventType string
	dataLines := make([]string, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}

	if eventType == "" || len(dataLines) == 0 {
		return SSEEvent{}, false
	}

	return SSEEvent{
		Event: eventType,
		Data:  []byte(strings.Join(dataLines, "\n")),
	}, true
}

func parseMessagesSSE(events []SSEEvent) (string, Usage, error) {
	var responseModel string
	var fallbackUsage Usage
	var finalUsage Usage
	var sawFinalUsage bool

	for _, event := range events {
		switch event.Event {
		case "message_start":
			model, usage, err := parseMessageStartEvent(event.Data)
			if err != nil {
				continue
			}
			if responseModel == "" {
				responseModel = model
			}
			fallbackUsage = usage
		case "message_delta":
			usage, err := parseMessageDeltaEvent(event.Data)
			if err != nil {
				continue
			}
			finalUsage = usage
			sawFinalUsage = true
		}
	}

	if responseModel == "" && !sawFinalUsage && isZeroUsage(fallbackUsage) {
		return "", Usage{}, fmt.Errorf("no message_start or message_delta data")
	}
	if sawFinalUsage {
		return responseModel, finalUsage, nil
	}

	return responseModel, fallbackUsage, nil
}
