package ratelimit

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"claude-meter-proxy/internal/config"
)

func defaultConfig() *config.RateLimitConfig {
	return &config.RateLimitConfig{
		Enabled:       true,
		InstanceShare: 0.25,
		Windows: config.WindowsConfig{
			H5: config.WindowConfig{Enabled: true, HardLimit: 0.25, WarnThreshold: 0.20},
			D7: config.WindowConfig{Enabled: true, HardLimit: 0.25, WarnThreshold: 0.20},
		},
		StaleAfterSeconds: 300,
	}
}

func headersWithUtil(util5h, util7d float64) http.Header {
	h := http.Header{}
	if util5h >= 0 {
		h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", formatFloat(util5h))
		h.Set("Anthropic-Ratelimit-Unified-5h-Reset", "1774490400")
	}
	if util7d >= 0 {
		h.Set("Anthropic-Ratelimit-Unified-7d-Utilization", formatFloat(util7d))
		h.Set("Anthropic-Ratelimit-Unified-7d-Reset", "1774580400")
	}
	return h
}

func headersWithUtilAndReset(util5h, util7d float64, reset5h, reset7d string) http.Header {
	h := http.Header{}
	if util5h >= 0 {
		h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", formatFloat(util5h))
		h.Set("Anthropic-Ratelimit-Unified-5h-Reset", reset5h)
	}
	if util7d >= 0 {
		h.Set("Anthropic-Ratelimit-Unified-7d-Utilization", formatFloat(util7d))
		h.Set("Anthropic-Ratelimit-Unified-7d-Reset", reset7d)
	}
	return h
}

func formatFloat(f float64) string {
	return fmt.Sprintf("%g", f)
}

func approxEqual(a, b float64) bool {
	const eps = 1e-9
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < eps
}

func TestAccountState_AllowWhenNoStateYet(t *testing.T) {
	t.Parallel()

	s := NewAccountState()
	cfg := defaultConfig()

	blocked, reason, _ := s.Check(cfg)
	if blocked {
		t.Errorf("should allow through with no state, got blocked: %s", reason)
	}
}

func TestAccountState_AllowBelowLimit(t *testing.T) {
	t.Parallel()

	s := NewAccountState()
	// Simulate two requests to build up local util.
	// First request: establish baseline (no prior state, no attribution).
	s.UpdateWithAttribution(headersWithUtil(0.05, 0.05), 0, 0, false, false)
	// Second request: delta from 0.05 → 0.10 = 0.05 local util.
	s.UpdateWithAttribution(headersWithUtil(0.10, 0.10), 0.05, 0.05, true, true)

	blocked, reason, _ := s.Check(defaultConfig())
	if blocked {
		t.Errorf("should allow at local util 5%%, got blocked: %s", reason)
	}
}

func TestAccountState_Block5hAtLimit(t *testing.T) {
	t.Parallel()

	s := NewAccountState()
	// First: establish baseline.
	s.UpdateWithAttribution(headersWithUtil(0.0, 0.0), 0, 0, false, false)
	// Second: delta from 0.0 → 0.25 = 0.25 local util.
	s.UpdateWithAttribution(headersWithUtil(0.25, 0.10), 0.0, 0.0, true, true)

	blocked, reason, _ := s.Check(defaultConfig())
	if !blocked {
		t.Error("should block when local 5h utilization >= hard limit")
	}
	if reason == "" {
		t.Error("reason should not be empty when blocked")
	}
}

func TestAccountState_Block7dAtLimit(t *testing.T) {
	t.Parallel()

	s := NewAccountState()
	s.UpdateWithAttribution(headersWithUtil(0.0, 0.0), 0, 0, false, false)
	s.UpdateWithAttribution(headersWithUtil(0.10, 0.25), 0.0, 0.0, true, true)

	blocked, reason, _ := s.Check(defaultConfig())
	if !blocked {
		t.Error("should block when local 7d utilization >= hard limit")
	}
	_ = reason
}

func TestAccountState_BlockBothWindows(t *testing.T) {
	t.Parallel()

	s := NewAccountState()
	s.UpdateWithAttribution(headersWithUtil(0.0, 0.0), 0, 0, false, false)
	s.UpdateWithAttribution(headersWithUtil(0.30, 0.30), 0.0, 0.0, true, true)

	blocked, _, _ := s.Check(defaultConfig())
	if !blocked {
		t.Error("should block when both windows exceed limit")
	}
}

func TestAccountState_AllowWhenDisabled(t *testing.T) {
	t.Parallel()

	s := NewAccountState()
	s.UpdateWithAttribution(headersWithUtil(0.0, 0.0), 0, 0, false, false)
	s.UpdateWithAttribution(headersWithUtil(0.99, 0.99), 0.0, 0.0, true, true)

	cfg := defaultConfig()
	cfg.Enabled = false

	blocked, _, _ := s.Check(cfg)
	if blocked {
		t.Error("should not block when rate limiting is disabled")
	}
}

func TestAccountState_AllowWhenWindowDisabled(t *testing.T) {
	t.Parallel()

	s := NewAccountState()
	s.UpdateWithAttribution(headersWithUtil(0.0, 0.0), 0, 0, false, false)
	s.UpdateWithAttribution(headersWithUtil(0.99, 0.10), 0.0, 0.0, true, true)

	cfg := defaultConfig()
	cfg.Windows.H5.Enabled = false

	blocked, _, _ := s.Check(cfg)
	if blocked {
		t.Error("should not block when the 5h window check is disabled (only 7d matters, which is fine)")
	}
}

func TestAccountState_StaleAllowsThrough(t *testing.T) {
	t.Parallel()

	s := NewAccountState()

	// Manually set state with an old ObservedAt and high local util.
	s.mu.Lock()
	s.W5h = WindowState{
		Utilization: 0.99,
		LocalUtil:   0.99,
		ObservedAt:  time.Now().Add(-10 * time.Minute), // 10 min ago
	}
	s.W7d = WindowState{
		Utilization: 0.99,
		LocalUtil:   0.99,
		ObservedAt:  time.Now().Add(-10 * time.Minute),
	}
	s.mu.Unlock()

	cfg := defaultConfig()
	cfg.StaleAfterSeconds = 300 // 5 min threshold

	blocked, _, _ := s.Check(cfg)
	if blocked {
		t.Error("should allow through when state is stale (fail open)")
	}
}

func TestAccountState_FreshStateBlocks(t *testing.T) {
	t.Parallel()

	s := NewAccountState()
	s.UpdateWithAttribution(headersWithUtil(0.0, 0.0), 0, 0, false, false)
	s.UpdateWithAttribution(headersWithUtil(0.30, 0.10), 0.0, 0.0, true, true)

	cfg := defaultConfig()
	cfg.StaleAfterSeconds = 300

	blocked, _, _ := s.Check(cfg)
	if !blocked {
		t.Error("should block with fresh 5h local util above limit")
	}
}

func TestAccountState_RetryAfterIsSet(t *testing.T) {
	t.Parallel()

	s := NewAccountState()
	s.UpdateWithAttribution(headersWithUtil(0.0, 0.0), 0, 0, false, false)
	s.UpdateWithAttribution(headersWithUtil(0.30, 0.10), 0.0, 0.0, true, true)

	blocked, _, retryAfter := s.Check(defaultConfig())
	if !blocked {
		t.Fatal("expected blocked")
	}
	if retryAfter.IsZero() {
		t.Error("retryAfter should be set when blocked")
	}
}

func TestAccountState_ConcurrentReadWrite(t *testing.T) {
	// This test is intended to be run with -race to detect data races.
	s := NewAccountState()
	cfg := defaultConfig()

	var wg sync.WaitGroup
	const goroutines = 50

	// Writers using UpdateWithAttribution
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			util := float64(i%30) / 100.0
			s.UpdateWithAttribution(headersWithUtil(util, util), util*0.9, util*0.9, true, true)
		}(i)
	}

	// Readers
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Check(cfg)
		}()
	}

	// Snapshot readers
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Snapshot()
		}()
	}

	wg.Wait()
}

func TestAccountState_UpdateNoHeaders(t *testing.T) {
	t.Parallel()

	s := NewAccountState()
	s.Update(headersWithUtil(0.10, 0.10))

	// Record ObservedAt before second update
	s.mu.RLock()
	obs5h := s.W5h.ObservedAt
	s.mu.RUnlock()

	// Update with empty headers — should not change state
	s.Update(http.Header{})

	s.mu.RLock()
	newObs5h := s.W5h.ObservedAt
	s.mu.RUnlock()

	if newObs5h != obs5h {
		t.Error("ObservedAt should not change when headers are absent")
	}
}

func TestAccountState_LocalUtilAccumulates(t *testing.T) {
	t.Parallel()

	s := NewAccountState()

	// First request: establish baseline (no prior state).
	s.UpdateWithAttribution(headersWithUtil(0.02, 0.01), 0, 0, false, false)

	s.mu.RLock()
	local5h := s.W5h.LocalUtil
	s.mu.RUnlock()
	if local5h != 0 {
		t.Fatalf("local util should be 0 after first update (no prior), got %g", local5h)
	}

	// Second request: account goes from 0.02 → 0.05. Delta = 0.03 attributed to us.
	s.UpdateWithAttribution(headersWithUtil(0.05, 0.03), 0.02, 0.01, true, true)

	s.mu.RLock()
	local5h = s.W5h.LocalUtil
	local7d := s.W7d.LocalUtil
	s.mu.RUnlock()
	if !approxEqual(local5h, 0.03) {
		t.Fatalf("expected local 5h util 0.03, got %g", local5h)
	}
	if !approxEqual(local7d, 0.02) {
		t.Fatalf("expected local 7d util 0.02, got %g", local7d)
	}

	// Third request: account goes from 0.05 → 0.12. Delta = 0.07 (us + others).
	s.UpdateWithAttribution(headersWithUtil(0.12, 0.06), 0.05, 0.03, true, true)

	s.mu.RLock()
	local5h = s.W5h.LocalUtil
	local7d = s.W7d.LocalUtil
	s.mu.RUnlock()
	if !approxEqual(local5h, 0.10) {
		t.Fatalf("expected local 5h util 0.10, got %g", local5h)
	}
	if !approxEqual(local7d, 0.05) {
		t.Fatalf("expected local 7d util 0.05, got %g", local7d)
	}
}

func TestAccountState_NegativeDeltaIgnored(t *testing.T) {
	t.Parallel()

	s := NewAccountState()
	s.UpdateWithAttribution(headersWithUtil(0.10, 0.10), 0, 0, false, false)
	// Utilization drops (other instance's window expired, or Anthropic adjusted).
	s.UpdateWithAttribution(headersWithUtil(0.08, 0.08), 0.10, 0.10, true, true)

	s.mu.RLock()
	local5h := s.W5h.LocalUtil
	s.mu.RUnlock()
	if local5h != 0 {
		t.Fatalf("negative delta should not affect local util, got %g", local5h)
	}
}

func TestAccountState_WindowResetClearsLocalUtil(t *testing.T) {
	t.Parallel()

	s := NewAccountState()
	// Establish baseline with reset at 1774490400.
	s.UpdateWithAttribution(headersWithUtil(0.0, 0.0), 0, 0, false, false)
	// Build up local util.
	s.UpdateWithAttribution(headersWithUtil(0.20, 0.10), 0.0, 0.0, true, true)

	s.mu.RLock()
	local5h := s.W5h.LocalUtil
	s.mu.RUnlock()
	if !approxEqual(local5h, 0.20) {
		t.Fatalf("expected local 5h util 0.20, got %g", local5h)
	}

	// New window: reset time changes → local util should be zeroed.
	s.UpdateWithAttribution(
		headersWithUtilAndReset(0.02, 0.10, "1774508400", "1774580400"),
		0.20, 0.10, true, true,
	)

	s.mu.RLock()
	local5h = s.W5h.LocalUtil
	s.mu.RUnlock()
	if local5h != 0 {
		t.Fatalf("local 5h util should be 0 after window reset, got %g", local5h)
	}
}

func TestAccountState_AccountWideDoesNotBlock(t *testing.T) {
	t.Parallel()

	// Account is at 50% but this instance contributed nothing.
	s := NewAccountState()
	// First update with high account util — no attribution (first request).
	s.UpdateWithAttribution(headersWithUtil(0.50, 0.50), 0, 0, false, false)

	blocked, _, _ := s.Check(defaultConfig())
	if blocked {
		t.Error("should NOT block based on account-wide util; only local util matters")
	}

	// Another request where account goes 0.50 → 0.55 (we contributed 0.05).
	s.UpdateWithAttribution(headersWithUtil(0.55, 0.55), 0.50, 0.50, true, true)

	s.mu.RLock()
	local5h := s.W5h.LocalUtil
	s.mu.RUnlock()
	if !approxEqual(local5h, 0.05) {
		t.Fatalf("expected local 5h util 0.05, got %g", local5h)
	}

	blocked, _, _ = s.Check(defaultConfig())
	if blocked {
		t.Error("should not block at 5%% local util")
	}
}

func TestAccountState_Reset(t *testing.T) {
	t.Parallel()

	s := NewAccountState()
	s.UpdateWithAttribution(headersWithUtil(0.0, 0.0), 0, 0, false, false)
	s.UpdateWithAttribution(headersWithUtil(0.30, 0.30), 0.0, 0.0, true, true)

	blocked, _, _ := s.Check(defaultConfig())
	if !blocked {
		t.Fatal("should be blocked before reset")
	}

	s.Reset()

	blocked, _, _ = s.Check(defaultConfig())
	if blocked {
		t.Error("should not be blocked after manual reset")
	}

	s.mu.RLock()
	local5h := s.W5h.LocalUtil
	local7d := s.W7d.LocalUtil
	s.mu.RUnlock()
	if local5h != 0 {
		t.Errorf("expected 5h local util 0 after reset, got %g", local5h)
	}
	if local7d != 0 {
		t.Errorf("expected 7d local util 0 after reset, got %g", local7d)
	}
}
