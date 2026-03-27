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

func formatFloat(f float64) string {
	return fmt.Sprintf("%g", f)
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
	s.Update(headersWithUtil(0.10, 0.10))

	blocked, reason, _ := s.Check(defaultConfig())
	if blocked {
		t.Errorf("should allow at 10%%, got blocked: %s", reason)
	}
}

func TestAccountState_Block5hAtLimit(t *testing.T) {
	t.Parallel()

	s := NewAccountState()
	s.Update(headersWithUtil(0.25, 0.10))

	blocked, reason, _ := s.Check(defaultConfig())
	if !blocked {
		t.Error("should block when 5h utilization >= hard limit")
	}
	if reason == "" {
		t.Error("reason should not be empty when blocked")
	}
}

func TestAccountState_Block7dAtLimit(t *testing.T) {
	t.Parallel()

	s := NewAccountState()
	s.Update(headersWithUtil(0.10, 0.25))

	blocked, reason, _ := s.Check(defaultConfig())
	if !blocked {
		t.Error("should block when 7d utilization >= hard limit")
	}
	_ = reason
}

func TestAccountState_BlockBothWindows(t *testing.T) {
	t.Parallel()

	s := NewAccountState()
	s.Update(headersWithUtil(0.30, 0.30))

	blocked, _, _ := s.Check(defaultConfig())
	if !blocked {
		t.Error("should block when both windows exceed limit")
	}
}

func TestAccountState_AllowWhenDisabled(t *testing.T) {
	t.Parallel()

	s := NewAccountState()
	s.Update(headersWithUtil(0.99, 0.99))

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
	s.Update(headersWithUtil(0.99, 0.10))

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

	// Manually set state with an old ObservedAt.
	s.mu.Lock()
	s.W5h = WindowState{
		Utilization: 0.99,
		ObservedAt:  time.Now().Add(-10 * time.Minute), // 10 min ago
	}
	s.W7d = WindowState{
		Utilization: 0.99,
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
	s.Update(headersWithUtil(0.30, 0.10))

	cfg := defaultConfig()
	cfg.StaleAfterSeconds = 300

	blocked, _, _ := s.Check(cfg)
	if !blocked {
		t.Error("should block with fresh 5h state above limit")
	}
}

func TestAccountState_RetryAfterIsSet(t *testing.T) {
	t.Parallel()

	s := NewAccountState()
	s.Update(headersWithUtil(0.30, 0.10))

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

	// Writers
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			util := float64(i%30) / 100.0
			s.Update(headersWithUtil(util, util))
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
