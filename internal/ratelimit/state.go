package ratelimit

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"claude-meter-proxy/internal/config"
)

// WindowState holds the most recently observed utilization for one rate limit window.
type WindowState struct {
	Utilization float64   // 0.0–1.0, fraction of account budget used
	ResetAt     time.Time // when this window resets
	ObservedAt  time.Time // when we last received a valid header for this window
}

// AccountState is the in-memory rate limit state updated on every proxied response.
// Thread-safe. One instance per proxy process.
type AccountState struct {
	mu  sync.RWMutex
	W5h WindowState
	W7d WindowState
}

// NewAccountState returns an empty AccountState ready for use.
func NewAccountState() *AccountState {
	return &AccountState{}
}

// Update parses response headers and stores any observed rate limit state.
// Called from the proxy response handler after every upstream response.
// Windows not present in headers are left unchanged.
func (s *AccountState) Update(h http.Header) {
	windows := ParseHeaders(h)
	if len(windows) == 0 {
		return
	}

	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	if ws, ok := windows["5h"]; ok {
		ws.ObservedAt = now
		s.W5h = ws
	}
	if ws, ok := windows["7d"]; ok {
		ws.ObservedAt = now
		s.W7d = ws
	}
}

// Check returns whether the current request should be blocked.
// Returns (blocked, reason, retryAfter).
// Fails open: if state is stale or lock is contended, allows the request through.
// Called from the proxy request handler BEFORE forwarding upstream.
func (s *AccountState) Check(cfg *config.RateLimitConfig) (bool, string, time.Time) {
	if !cfg.Enabled {
		return false, "", time.Time{}
	}

	// TryRLock to avoid blocking callers when a write is in progress.
	// Fail open if lock is contended.
	if !s.mu.TryRLock() {
		log.Printf("rate limit: state lock contended, allowing request through (fail open)")
		return false, "", time.Time{}
	}
	defer s.mu.RUnlock()

	now := time.Now()
	staleThreshold := time.Duration(cfg.StaleAfterSeconds) * time.Second

	if cfg.Windows.H5.Enabled {
		stale := s.W5h.ObservedAt.IsZero() || now.Sub(s.W5h.ObservedAt) > staleThreshold
		if stale && !s.W5h.ObservedAt.IsZero() {
			log.Printf("rate limit: 5h state is stale (last observed %v ago), allowing request through", now.Sub(s.W5h.ObservedAt).Round(time.Second))
		}
		if !stale && s.W5h.Utilization >= cfg.Windows.H5.HardLimit {
			return true,
				fmt.Sprintf("5h window: account at %.1f%% (instance cap: %.1f%%)",
					s.W5h.Utilization*100, cfg.Windows.H5.HardLimit*100),
				s.W5h.ResetAt
		}
	}

	if cfg.Windows.D7.Enabled {
		stale := s.W7d.ObservedAt.IsZero() || now.Sub(s.W7d.ObservedAt) > staleThreshold
		if stale && !s.W7d.ObservedAt.IsZero() {
			log.Printf("rate limit: 7d state is stale (last observed %v ago), allowing request through", now.Sub(s.W7d.ObservedAt).Round(time.Second))
		}
		if !stale && s.W7d.Utilization >= cfg.Windows.D7.HardLimit {
			return true,
				fmt.Sprintf("7d window: account at %.1f%% (instance cap: %.1f%%)",
					s.W7d.Utilization*100, cfg.Windows.D7.HardLimit*100),
				s.W7d.ResetAt
		}
	}

	return false, "", time.Time{}
}

// Snapshot returns a copy of the current window states for the status endpoint.
func (s *AccountState) Snapshot() (w5h, w7d WindowState) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.W5h, s.W7d
}
