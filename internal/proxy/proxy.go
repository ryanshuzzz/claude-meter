package proxy

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"claude-meter-proxy/internal/capture"
	"claude-meter-proxy/internal/config"
	"claude-meter-proxy/internal/ratelimit"
	"claude-meter-proxy/internal/storage"
)

// Config holds the configuration for the proxy Server.
type Config struct {
	UpstreamBaseURL  *url.URL
	Client           *http.Client
	CaptureCh        chan<- capture.CompletedExchange
	State            *ratelimit.AccountState
	Cfg              *config.Config
	NormalizedWriter *storage.NormalizedRecordWriter
}

// Server is the HTTP reverse proxy.
type Server struct {
	upstreamBaseURL  *url.URL
	client           *http.Client
	captureCh        chan<- capture.CompletedExchange
	nextID           atomic.Uint64
	droppedCount     atomic.Uint64
	state            *ratelimit.AccountState
	cfg              *config.Config
	normalizedWriter *storage.NormalizedRecordWriter
	blockedToday     atomic.Int64
	w5hWarnIssued    atomic.Bool
	w7dWarnIssued    atomic.Bool
}

func New(cfg Config) *Server {
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}

	return &Server{
		upstreamBaseURL:  cfg.UpstreamBaseURL,
		client:           client,
		captureCh:        cfg.CaptureCh,
		state:            cfg.State,
		cfg:              cfg.Cfg,
		normalizedWriter: cfg.NormalizedWriter,
	}
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.ServeHTTP)
}

// BlockedCount returns the number of requests blocked by the rate limiter since startup.
func (s *Server) BlockedCount() int64 {
	return s.blockedToday.Load()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	requestID := s.nextID.Add(1)

	// Pre-flight rate limit check. Fail open on any error.
	if s.state != nil && s.cfg != nil {
		blocked, reason, retryAfter := s.state.Check(&s.cfg.RateLimits)
		if blocked {
			s.blockedToday.Add(1)
			s.logBlockedEvent(r, retryAfter)
			if !retryAfter.IsZero() {
				w.Header().Set("Retry-After", retryAfter.UTC().Format(time.RFC1123))
			}
			w.Header().Set("X-Claude-Meter-Blocked", "true")
			w.Header().Set("X-Claude-Meter-Reason", reason)
			http.Error(w, reason, http.StatusTooManyRequests)
			return
		}
	}

	// Capture pre-request state for local utilization attribution.
	var preUtil5h, preUtil7d float64
	var hadPrior5h, hadPrior7d bool
	if s.state != nil {
		snap5h, snap7d := s.state.Snapshot()
		preUtil5h = snap5h.Utilization
		preUtil7d = snap7d.Utilization
		hadPrior5h = !snap5h.ObservedAt.IsZero()
		hadPrior7d = !snap7d.ObservedAt.IsZero()
	}

	requestBody, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	upstreamURL := s.upstreamBaseURL.ResolveReference(&url.URL{
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	})

	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL.String(), bytes.NewReader(requestBody))
	if err != nil {
		http.Error(w, "failed to create upstream request", http.StatusInternalServerError)
		return
	}
	upstreamReq.Header = r.Header.Clone()

	resp, err := s.client.Do(upstreamReq)
	if err != nil {
		http.Error(w, fmt.Sprintf("upstream request failed: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Update rate limit state from response headers with local attribution.
	if s.state != nil {
		s.state.UpdateWithAttribution(resp.Header, preUtil5h, preUtil7d, hadPrior5h, hadPrior7d)
		s.checkWarnThreshold()
	}

	for name, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(resp.StatusCode)

	var responseBody bytes.Buffer
	_, copyErr := io.Copy(io.MultiWriter(w, &responseBody), resp.Body)

	s.enqueueCapture(capture.CompletedExchange{
		ID:               requestID,
		RequestStartedAt: start,
		ResponseEndedAt:  time.Now().UTC(),
		DurationMS:       time.Since(start).Milliseconds(),
		Request: capture.RecordedRequest{
			Method:  r.Method,
			Path:    pathWithQuery(r.URL),
			Headers: flattenHeaders(r.Header),
			Body:    requestBody,
		},
		Response: capture.RecordedResponse{
			Status:  resp.StatusCode,
			Headers: flattenHeaders(resp.Header),
			Body:    responseBody.Bytes(),
		},
	})

	if copyErr != nil {
		return
	}
}

// logBlockedEvent writes a BlockedEvent to the normalized log when a request is blocked.
func (s *Server) logBlockedEvent(r *http.Request, retryAfter time.Time) {
	if s.normalizedWriter == nil {
		return
	}
	w5h, w7d := s.state.Snapshot()
	window := "7d"
	util := w7d.LocalUtil
	if s.cfg.RateLimits.Windows.H5.Enabled && w5h.LocalUtil >= s.cfg.RateLimits.Windows.H5.HardLimit {
		window = "5h"
		util = w5h.LocalUtil
	}
	ev := storage.BlockedEvent{
		Type:               "blocked",
		Ts:                 time.Now().UTC(),
		Window:             window,
		AccountUtilization: util,
		InstanceLimit:      s.cfg.RateLimits.InstanceShare,
		RetryAfter:         retryAfter,
		RequestPath:        r.URL.Path,
	}
	if err := s.normalizedWriter.WriteBlockedEvent(ev); err != nil {
		log.Printf("claude-meter: write blocked event: %v", err)
	}
}

// checkWarnThreshold emits a warn event the first time utilization crosses the warn threshold
// for each window. The flag resets when utilization drops back below the threshold.
func (s *Server) checkWarnThreshold() {
	if s.cfg == nil || s.normalizedWriter == nil {
		return
	}
	cfg := &s.cfg.RateLimits
	w5h, w7d := s.state.Snapshot()
	now := time.Now().UTC()

	if cfg.Windows.H5.Enabled && w5h.LocalUtil >= cfg.Windows.H5.WarnThreshold {
		if s.w5hWarnIssued.CompareAndSwap(false, true) {
			ev := storage.WarnEvent{
				Type:               "warn",
				Ts:                 now,
				Window:             "5h",
				AccountUtilization: w5h.LocalUtil,
				InstanceLimit:      cfg.Windows.H5.HardLimit,
				HeadroomRemaining:  cfg.Windows.H5.HardLimit - w5h.LocalUtil,
			}
			if err := s.normalizedWriter.WriteWarnEvent(ev); err != nil {
				log.Printf("claude-meter: write warn event: %v", err)
			}
		}
	} else {
		s.w5hWarnIssued.Store(false)
	}

	if cfg.Windows.D7.Enabled && w7d.LocalUtil >= cfg.Windows.D7.WarnThreshold {
		if s.w7dWarnIssued.CompareAndSwap(false, true) {
			ev := storage.WarnEvent{
				Type:               "warn",
				Ts:                 now,
				Window:             "7d",
				AccountUtilization: w7d.LocalUtil,
				InstanceLimit:      cfg.Windows.D7.HardLimit,
				HeadroomRemaining:  cfg.Windows.D7.HardLimit - w7d.LocalUtil,
			}
			if err := s.normalizedWriter.WriteWarnEvent(ev); err != nil {
				log.Printf("claude-meter: write warn event: %v", err)
			}
		}
	} else {
		s.w7dWarnIssued.Store(false)
	}
}

func (s *Server) enqueueCapture(exchange capture.CompletedExchange) {
	if s.captureCh == nil {
		return
	}

	select {
	case s.captureCh <- exchange:
	default:
		s.droppedCount.Add(1)
	}
}

func flattenHeaders(headers http.Header) []capture.Header {
	if len(headers) == 0 {
		return nil
	}

	flattened := make([]capture.Header, 0, len(headers))
	for name, values := range headers {
		for _, value := range values {
			flattened = append(flattened, capture.Header{
				Name:  http.CanonicalHeaderKey(name),
				Value: value,
			})
		}
	}
	return flattened
}

func pathWithQuery(u *url.URL) string {
	if u.RawQuery == "" {
		return u.Path
	}
	return u.Path + "?" + u.RawQuery
}
