package ratelimit

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const headerPrefix = "anthropic-ratelimit-unified-"

// ParseHeaders extracts rate limit window states from HTTP response headers.
// Only windows for which at least one valid header is found are returned.
// Absent headers leave the caller's existing state unchanged.
// Malformed values are logged as warnings and that window is skipped.
// Utilization values >1.0 are normalized by dividing by 100 (percent → fraction).
func ParseHeaders(h http.Header) map[string]WindowState {
	result := make(map[string]WindowState)
	for _, window := range []string{"5h", "7d"} {
		if ws, ok := parseWindow(h, window); ok {
			result[window] = ws
		}
	}

	// Log any unrecognized ratelimit-related headers for future visibility.
	for name := range h {
		lower := strings.ToLower(name)
		if strings.Contains(lower, "ratelimit") && !strings.HasPrefix(lower, headerPrefix) {
			log.Printf("rate limit: unrecognized ratelimit header %q", name)
		}
	}

	return result
}

func parseWindow(h http.Header, window string) (WindowState, bool) {
	prefix := headerPrefix + window + "-"
	utilKey := canonicalHeader(prefix + "utilization")
	resetKey := canonicalHeader(prefix + "reset")

	utilVal := h.Get(utilKey)
	resetVal := h.Get(resetKey)

	if utilVal == "" && resetVal == "" {
		return WindowState{}, false
	}

	var ws WindowState
	found := false

	if utilVal != "" {
		util, err := strconv.ParseFloat(strings.TrimSpace(utilVal), 64)
		if err != nil {
			log.Printf("rate limit: malformed %s utilization header %q: %v", window, utilVal, err)
		} else {
			if util > 1.0 {
				util = util / 100.0
			}
			ws.Utilization = util
			found = true
		}
	}

	if resetVal != "" {
		reset, err := strconv.ParseInt(strings.TrimSpace(resetVal), 10, 64)
		if err != nil {
			log.Printf("rate limit: malformed %s reset header %q: %v", window, resetVal, err)
		} else {
			ws.ResetAt = time.Unix(reset, 0)
			found = true
		}
	}

	return ws, found
}

// canonicalHeader converts a header name to its canonical HTTP form.
// e.g. "anthropic-ratelimit-unified-5h-utilization" → "Anthropic-Ratelimit-Unified-5h-Utilization"
func canonicalHeader(name string) string {
	return http.CanonicalHeaderKey(name)
}
