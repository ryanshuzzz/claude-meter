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
	Utilization  float64   // account-wide (0.0–1.0) from Anthropic headers
	ResetAt      time.Time // when this window resets (from headers)
	ObservedAt   time.Time // when we last received a valid header for this window
	LocalUtil    float64   // accumulated local utilization attributed to this instance
	LocalResetAt time.Time // the reset time when LocalUtil was last zeroed
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
// Does NOT update local attribution — use UpdateWithAttribution for that.
// Retained for backward compatibility and tests that only need account-wide state.
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
		ws.LocalUtil = s.W5h.LocalUtil
		ws.LocalResetAt = s.W5h.LocalResetAt
		s.W5h = ws
	}
	if ws, ok := windows["7d"]; ok {
		ws.ObservedAt = now
		ws.LocalUtil = s.W7d.LocalUtil
		ws.LocalResetAt = s.W7d.LocalResetAt
		s.W7d = ws
	}
}

// UpdateWithAttribution parses response headers, updates account-wide state,
// and attributes the utilization delta to this instance's local accumulator.
//
// preUtil5h/preUtil7d are the account-wide utilization values captured BEFORE
// the request was forwarded upstream. hadPrior5h/hadPrior7d indicate whether
// valid prior state existed (false on first ever update — skip attribution).
func (s *AccountState) UpdateWithAttribution(h http.Header, preUtil5h, preUtil7d float64, hadPrior5h, hadPrior7d bool) {
	windows := ParseHeaders(h)
	if len(windows) == 0 {
		return
	}

	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	if ws, ok := windows["5h"]; ok {
		ws.ObservedAt = now
		s.updateWindowLocal(&ws, &s.W5h, preUtil5h, hadPrior5h)
		s.W5h = ws
	}
	if ws, ok := windows["7d"]; ok {
		ws.ObservedAt = now
		s.updateWindowLocal(&ws, &s.W7d, preUtil7d, hadPrior7d)
		s.W7d = ws
	}
}

// updateWindowLocal computes and applies local utilization attribution for a window.
// Must be called under s.mu write lock.
func (s *AccountState) updateWindowLocal(newWS *WindowState, prev *WindowState, preUtil float64, hadPrior bool) {
	// Window reset: reset time changed → new window, zero local accumulator.
	if !newWS.ResetAt.IsZero() && newWS.ResetAt != prev.LocalResetAt && !prev.LocalResetAt.IsZero() {
		log.Printf("rate limit: window reset detected (old reset %v → new reset %v), clearing local util %.4f",
			prev.LocalResetAt, newWS.ResetAt, prev.LocalUtil)
		newWS.LocalUtil = 0
		newWS.LocalResetAt = newWS.ResetAt
		return
	}

	if !hadPrior {
		// First update ever — don't attribute existing account utilization to us.
		newWS.LocalUtil = prev.LocalUtil
		if newWS.ResetAt.IsZero() {
			newWS.LocalResetAt = prev.LocalResetAt
		} else {
			newWS.LocalResetAt = newWS.ResetAt
		}
		return
	}

	// Normal case: attribute the positive delta to this instance.
	delta := newWS.Utilization - preUtil
	if delta > 0 {
		newWS.LocalUtil = prev.LocalUtil + delta
	} else {
		newWS.LocalUtil = prev.LocalUtil
	}
	newWS.LocalResetAt = prev.LocalResetAt
}

// Check returns whether the current request should be blocked based on
// this instance's LOCAL utilization (not account-wide).
// Returns (blocked, reason, retryAfter).
// Fails open: if state is stale or lock is contended, allows the request through.
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
		if !stale && s.W5h.LocalUtil >= cfg.Windows.H5.HardLimit {
			return true,
				fmt.Sprintf("5h window: this instance at %.1f%% (cap: %.1f%%, account: %.1f%%)",
					s.W5h.LocalUtil*100, cfg.Windows.H5.HardLimit*100, s.W5h.Utilization*100),
				s.W5h.ResetAt
		}
	}

	if cfg.Windows.D7.Enabled {
		stale := s.W7d.ObservedAt.IsZero() || now.Sub(s.W7d.ObservedAt) > staleThreshold
		if stale && !s.W7d.ObservedAt.IsZero() {
			log.Printf("rate limit: 7d state is stale (last observed %v ago), allowing request through", now.Sub(s.W7d.ObservedAt).Round(time.Second))
		}
		if !stale && s.W7d.LocalUtil >= cfg.Windows.D7.HardLimit {
			return true,
				fmt.Sprintf("7d window: this instance at %.1f%% (cap: %.1f%%, account: %.1f%%)",
					s.W7d.LocalUtil*100, cfg.Windows.D7.HardLimit*100, s.W7d.Utilization*100),
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

// Reset zeroes the local utilization accumulators for both windows.
// Used for manual resets via the /reset endpoint.
func (s *AccountState) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	log.Printf("rate limit: manual reset — clearing local 5h util %.4f, 7d util %.4f", s.W5h.LocalUtil, s.W7d.LocalUtil)
	s.W5h.LocalUtil = 0
	s.W7d.LocalUtil = 0
}
