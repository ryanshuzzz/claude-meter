package normalize

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"claude-meter-proxy/internal/capture"
)

const ratelimitPrefix = "anthropic-ratelimit-unified-"

var ratelimitTopLevelFields = map[string]func(*Ratelimit, string){
	"status": func(r *Ratelimit, value string) {
		r.Status = value
	},
	"representative-claim": func(r *Ratelimit, value string) {
		r.RepresentativeClaim = value
	},
	"fallback-percentage": func(r *Ratelimit, value string) {
		if parsed, ok := parseFloat(value); ok {
			r.FallbackPercentage = parsed
		}
	},
	"overage-disabled-reason": func(r *Ratelimit, value string) {
		r.OverageDisabledReason = value
	},
	"overage-status": func(r *Ratelimit, value string) {
		r.OverageStatus = value
	},
}

type Normalizer struct {
	planTier string
}

func New(planTier string) *Normalizer {
	return &Normalizer{planTier: planTier}
}

func (n *Normalizer) Normalize(exchange capture.CompletedExchange) Record {
	record := Record{
		ID:                exchange.ID,
		RequestTimestamp:  exchange.RequestStartedAt,
		ResponseTimestamp: exchange.ResponseEndedAt,
		Method:            exchange.Request.Method,
		Path:              exchange.Request.Path,
		Status:            exchange.Response.Status,
		LatencyMS:         exchange.DurationMS,
		DeclaredPlanTier:  n.planTier,
		RequestID:         headerValue(exchange.Response.Headers, "request-id"),
		Ratelimit:         parseRatelimit(exchange.Response.Headers),
	}

	switch basePath(exchange.Request.Path) {
	case "/v1/messages":
		n.enrichMessagesRecord(&record, exchange)
	case "/v1/messages/count_tokens":
		n.enrichCountTokensRecord(&record, exchange)
	}

	return record
}

func (n *Normalizer) enrichMessagesRecord(record *Record, exchange capture.CompletedExchange) {
	requestBody, err := decodeBody(exchange.Request.Headers, exchange.Request.Body)
	if err == nil {
		record.RequestModel, record.SessionID = parseMessagesRequest(requestBody)
	}

	if isEventStream(exchange.Response.Headers) {
		events, err := parseSSEEvents(exchange.Response.Body, exchange.Response.Headers)
		if err == nil {
			record.ResponseModel, record.Usage, _ = parseMessagesSSE(events)
			if record.ResponseModel != "" || !isZeroUsage(record.Usage) {
				return
			}
		}
	}

	responseBody, err := decodeBody(exchange.Response.Headers, exchange.Response.Body)
	if err == nil {
		record.ResponseModel, record.Usage = parseMessagesResponse(responseBody)
	}
}

func (n *Normalizer) enrichCountTokensRecord(record *Record, exchange capture.CompletedExchange) {
	requestBody, err := decodeBody(exchange.Request.Headers, exchange.Request.Body)
	if err == nil {
		record.RequestModel = parseRequestModel(requestBody)
	}

	responseBody, err := decodeBody(exchange.Response.Headers, exchange.Response.Body)
	if err == nil {
		record.Usage = parseCountTokensResponse(responseBody)
	}
}

func parseRatelimit(headers []capture.Header) Ratelimit {
	ratelimit := Ratelimit{
		Windows: make(map[string]RatelimitWindow),
	}

	if retryAfter, ok := parseInt(headerValue(headers, "retry-after")); ok {
		ratelimit.RetryAfterS = int(retryAfter)
	}

	for _, header := range headers {
		key := strings.ToLower(strings.TrimSpace(header.Name))
		value := strings.TrimSpace(header.Value)
		if !strings.HasPrefix(key, ratelimitPrefix) {
			continue
		}

		suffix := strings.TrimPrefix(key, ratelimitPrefix)
		if setter, ok := ratelimitTopLevelFields[suffix]; ok {
			setter(&ratelimit, value)
			continue
		}

		windowName, fieldName, ok := splitWindowField(suffix)
		if !ok {
			continue
		}

		window := ratelimit.Windows[windowName]
		switch fieldName {
		case "status":
			window.Status = value
		case "reset":
			if parsed, ok := parseInt(value); ok {
				window.ResetTS = parsed
			}
		case "utilization":
			if parsed, ok := parseFloat(value); ok {
				window.Utilization = parsed
			}
		case "surpassed-threshold":
			if parsed, ok := parseBool(value); ok {
				window.SurpassedThreshold = parsed
			}
		}
		ratelimit.Windows[windowName] = window
	}

	if len(ratelimit.Windows) == 0 {
		ratelimit.Windows = nil
	}

	return ratelimit
}

func splitWindowField(suffix string) (windowName string, fieldName string, ok bool) {
	for _, candidate := range []string{"status", "reset", "utilization", "surpassed-threshold"} {
		needle := "-" + candidate
		if strings.HasSuffix(suffix, needle) {
			return strings.TrimSuffix(suffix, needle), candidate, true
		}
	}

	return "", "", false
}

func headerValue(headers []capture.Header, name string) string {
	for _, header := range headers {
		if strings.EqualFold(strings.TrimSpace(header.Name), name) {
			return strings.TrimSpace(header.Value)
		}
	}

	return ""
}

func parseInt(value string) (int64, bool) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func parseFloat(value string) (float64, bool) {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func parseBool(value string) (bool, bool) {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, false
	}
	return parsed, true
}

func decodeBody(headers []capture.Header, raw []byte) ([]byte, error) {
	if strings.EqualFold(headerValue(headers, "content-encoding"), "gzip") {
		reader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer reader.Close()

		return io.ReadAll(reader)
	}

	return raw, nil
}

func parseMessagesRequest(body []byte) (requestModel string, sessionID string) {
	type requestMetadata struct {
		UserID any `json:"user_id"`
	}
	type requestPayload struct {
		Model    string          `json:"model"`
		Metadata requestMetadata `json:"metadata"`
	}

	var payload requestPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", ""
	}

	return payload.Model, extractSessionID(payload.Metadata.UserID)
}

func parseRequestModel(body []byte) string {
	type requestPayload struct {
		Model string `json:"model"`
	}

	var payload requestPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}

	return payload.Model
}

func parseMessagesResponse(body []byte) (responseModel string, usage Usage) {
	type responseUsage struct {
		InputTokens              int `json:"input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		OutputTokens             int `json:"output_tokens"`
	}
	type responsePayload struct {
		Model string        `json:"model"`
		Usage responseUsage `json:"usage"`
	}

	var payload responsePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", Usage{}
	}

	return payload.Model, Usage{
		InputTokens:              payload.Usage.InputTokens,
		CacheCreationInputTokens: payload.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     payload.Usage.CacheReadInputTokens,
		OutputTokens:             payload.Usage.OutputTokens,
	}
}

func parseMessageStartEvent(data []byte) (string, Usage, error) {
	type messagePayload struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			OutputTokens             int `json:"output_tokens"`
		} `json:"usage"`
	}
	type eventPayload struct {
		Message messagePayload `json:"message"`
	}

	var payload eventPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", Usage{}, err
	}

	return payload.Message.Model, Usage{
		InputTokens:              payload.Message.Usage.InputTokens,
		CacheCreationInputTokens: payload.Message.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     payload.Message.Usage.CacheReadInputTokens,
		OutputTokens:             payload.Message.Usage.OutputTokens,
	}, nil
}

func parseMessageDeltaEvent(data []byte) (Usage, error) {
	type eventPayload struct {
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			OutputTokens             int `json:"output_tokens"`
		} `json:"usage"`
	}

	var payload eventPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return Usage{}, err
	}

	return Usage{
		InputTokens:              payload.Usage.InputTokens,
		CacheCreationInputTokens: payload.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     payload.Usage.CacheReadInputTokens,
		OutputTokens:             payload.Usage.OutputTokens,
	}, nil
}

func parseCountTokensResponse(body []byte) Usage {
	type responsePayload struct {
		InputTokens int `json:"input_tokens"`
	}

	var payload responsePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return Usage{}
	}

	return Usage{
		InputTokens: payload.InputTokens,
	}
}

func isEventStream(headers []capture.Header) bool {
	return strings.Contains(strings.ToLower(headerValue(headers, "content-type")), "text/event-stream")
}

func isZeroUsage(usage Usage) bool {
	return usage.InputTokens == 0 &&
		usage.CacheCreationInputTokens == 0 &&
		usage.CacheReadInputTokens == 0 &&
		usage.OutputTokens == 0
}

func extractSessionID(userID any) string {
	switch typed := userID.(type) {
	case string:
		var payload struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal([]byte(typed), &payload); err != nil {
			return ""
		}
		return payload.SessionID
	case map[string]any:
		sessionID, _ := typed["session_id"].(string)
		return sessionID
	default:
		return ""
	}
}

func basePath(path string) string {
	if index := strings.IndexByte(path, '?'); index >= 0 {
		return path[:index]
	}

	return path
}
