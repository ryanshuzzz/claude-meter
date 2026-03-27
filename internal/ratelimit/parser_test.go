package ratelimit

import (
	"net/http"
	"testing"
	"time"
)

func TestParseHeaders_BothWindows(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "0.18")
	h.Set("Anthropic-Ratelimit-Unified-5h-Reset", "1774490400")
	h.Set("Anthropic-Ratelimit-Unified-7d-Utilization", "0.61")
	h.Set("Anthropic-Ratelimit-Unified-7d-Reset", "1774580400")

	result := ParseHeaders(h)

	w5h, ok := result["5h"]
	if !ok {
		t.Fatal("expected 5h window in result")
	}
	if w5h.Utilization != 0.18 {
		t.Errorf("5h utilization = %g, want 0.18", w5h.Utilization)
	}
	if w5h.ResetAt != time.Unix(1774490400, 0) {
		t.Errorf("5h reset = %v, want %v", w5h.ResetAt, time.Unix(1774490400, 0))
	}

	w7d, ok := result["7d"]
	if !ok {
		t.Fatal("expected 7d window in result")
	}
	if w7d.Utilization != 0.61 {
		t.Errorf("7d utilization = %g, want 0.61", w7d.Utilization)
	}
}

func TestParseHeaders_Absent(t *testing.T) {
	t.Parallel()

	result := ParseHeaders(http.Header{})
	if len(result) != 0 {
		t.Errorf("expected empty result for empty headers, got %v", result)
	}
}

func TestParseHeaders_Only5h(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "0.10")

	result := ParseHeaders(h)
	if _, ok := result["5h"]; !ok {
		t.Error("expected 5h in result")
	}
	if _, ok := result["7d"]; ok {
		t.Error("did not expect 7d in result when headers absent")
	}
}

func TestParseHeaders_NormalizePercent(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "25") // 25% as integer

	result := ParseHeaders(h)
	w5h, ok := result["5h"]
	if !ok {
		t.Fatal("expected 5h in result")
	}
	if w5h.Utilization != 0.25 {
		t.Errorf("utilization = %g, want 0.25 (25 normalized by /100)", w5h.Utilization)
	}
}

func TestParseHeaders_MalformedUtilization(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "not-a-number")
	h.Set("Anthropic-Ratelimit-Unified-5h-Reset", "1774490400")

	result := ParseHeaders(h)
	// Reset header is valid so window should still appear
	w5h, ok := result["5h"]
	if !ok {
		t.Fatal("expected 5h in result even with malformed utilization (reset is valid)")
	}
	if w5h.Utilization != 0 {
		t.Errorf("utilization = %g, want 0 (malformed, not updated)", w5h.Utilization)
	}
	if w5h.ResetAt.IsZero() {
		t.Error("ResetAt should be set from valid reset header")
	}
}

func TestParseHeaders_MalformedReset(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("Anthropic-Ratelimit-Unified-7d-Utilization", "0.50")
	h.Set("Anthropic-Ratelimit-Unified-7d-Reset", "not-a-timestamp")

	result := ParseHeaders(h)
	w7d, ok := result["7d"]
	if !ok {
		t.Fatal("expected 7d in result even with malformed reset (utilization is valid)")
	}
	if w7d.Utilization != 0.50 {
		t.Errorf("utilization = %g, want 0.50", w7d.Utilization)
	}
	if !w7d.ResetAt.IsZero() {
		t.Error("ResetAt should be zero for malformed reset header")
	}
}

func TestParseHeaders_BothMalformed(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "bad")
	h.Set("Anthropic-Ratelimit-Unified-5h-Reset", "bad")

	result := ParseHeaders(h)
	if _, ok := result["5h"]; ok {
		t.Error("expected 5h absent when both headers are malformed")
	}
}

func TestParseHeaders_LowercaseHeaders(t *testing.T) {
	t.Parallel()

	// net/http canonicalizes header keys, but test that lowercase input works via Set
	h := http.Header{}
	h.Set("anthropic-ratelimit-unified-5h-utilization", "0.30")
	h.Set("anthropic-ratelimit-unified-5h-reset", "1774490400")

	result := ParseHeaders(h)
	if _, ok := result["5h"]; !ok {
		t.Error("expected 5h in result with lowercase header names")
	}
}

func TestParseHeaders_UtilizationExactlyOne(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "1.0")

	result := ParseHeaders(h)
	w5h := result["5h"]
	if w5h.Utilization != 1.0 {
		t.Errorf("utilization = %g, want 1.0 (should not normalize 1.0 / 100)", w5h.Utilization)
	}
}
