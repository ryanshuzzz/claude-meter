package app

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	"claude-meter-proxy/internal/capture"
	"claude-meter-proxy/internal/config"
	"claude-meter-proxy/internal/normalize"
	"claude-meter-proxy/internal/proxy"
	"claude-meter-proxy/internal/ratelimit"
	"claude-meter-proxy/internal/storage"
)

type rawWriter interface {
	Write(capture.CompletedExchange) error
}

type normalizedWriter interface {
	Write(normalize.Record) error
}

type normalizer interface {
	Normalize(capture.CompletedExchange) normalize.Record
}

// Config holds the configuration for the App.
type Config struct {
	UpstreamBaseURL *url.URL
	LogDir          string
	QueueSize       int
	PlanTier        string
	Client          *http.Client
	// InstanceShare overrides the config file instance_share value.
	// Zero means use the config file default.
	InstanceShare float64
}

// App wires together the proxy, background writer, and status endpoint.
type App struct {
	proxy            *proxy.Server
	exchanges        chan capture.CompletedExchange
	rawWriter        rawWriter
	normalizedWriter normalizedWriter
	normalizer       normalizer
	state            *ratelimit.AccountState
	cfg              *config.Config
	startedAt        time.Time

	closeOnce sync.Once
	wg        sync.WaitGroup
}

func New(cfg Config) (*App, error) {
	if cfg.UpstreamBaseURL == nil {
		return nil, fmt.Errorf("upstream base URL is required")
	}
	if cfg.LogDir == "" {
		return nil, fmt.Errorf("log dir is required")
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 256
	}

	rateCfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load rate limit config: %w", err)
	}
	if cfg.InstanceShare > 0 {
		rateCfg.RateLimits.InstanceShare = cfg.InstanceShare
		rateCfg.RateLimits.Windows.H5.HardLimit = cfg.InstanceShare
		rateCfg.RateLimits.Windows.D7.HardLimit = cfg.InstanceShare
	}

	state := ratelimit.NewAccountState()

	rw, err := storage.NewRawExchangeWriter(filepath.Join(cfg.LogDir, "raw"))
	if err != nil {
		return nil, err
	}
	nw, err := storage.NewNormalizedRecordWriter(filepath.Join(cfg.LogDir, "normalized"))
	if err != nil {
		return nil, err
	}
	norm := normalize.New(cfg.PlanTier)

	exchanges := make(chan capture.CompletedExchange, cfg.QueueSize)
	app := &App{
		exchanges:        exchanges,
		rawWriter:        rw,
		normalizedWriter: nw,
		normalizer:       norm,
		state:            state,
		cfg:              rateCfg,
		startedAt:        time.Now(),
	}

	app.proxy = proxy.New(proxy.Config{
		UpstreamBaseURL:  cfg.UpstreamBaseURL,
		Client:           cfg.Client,
		CaptureCh:        exchanges,
		State:            state,
		Cfg:              rateCfg,
		NormalizedWriter: nw,
	})

	app.startBackgroundWriter()

	return app, nil
}

func (a *App) startBackgroundWriter() {
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		for ex := range a.exchanges {
			a.processExchange(ex)
		}
	}()
}

func (a *App) processExchange(ex capture.CompletedExchange) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("claude-meter: recovered panic processing exchange %d: %v", ex.ID, r)
		}
	}()

	if err := a.rawWriter.Write(ex); err != nil {
		log.Printf("claude-meter: raw write error for exchange %d: %v", ex.ID, err)
	}
	if err := a.normalizedWriter.Write(a.normalizer.Normalize(ex)); err != nil {
		log.Printf("claude-meter: normalized write error for exchange %d: %v", ex.ID, err)
	}
}

// Handler returns an http.Handler that routes /status and /health locally
// and forwards all other requests through the proxy.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", a.handleStatus)
	mux.HandleFunc("GET /health", a.handleHealth)
	mux.HandleFunc("POST /reset", a.handleReset)
	mux.Handle("/", a.proxy.Handler())
	return mux
}

// windowStatus is the JSON shape for one rate limit window in the status response.
type windowStatus struct {
	Utilization        float64 `json:"utilization"`
	AccountUtilization float64 `json:"account_utilization"`
	Limit              float64 `json:"limit"`
	Headroom           float64 `json:"headroom"`
	PctOfLimitUsed     float64 `json:"pct_of_limit_used"`
	ResetAt            string  `json:"reset_at"`
	Stale              bool    `json:"stale"`
	ObservedAt         string  `json:"observed_at"`
}

// statusResponse is the JSON body returned by the /status endpoint.
type statusResponse struct {
	InstanceLimit        float64                  `json:"instance_limit"`
	Windows              map[string]windowStatus  `json:"windows"`
	BlockedRequestsToday int64                    `json:"blocked_requests_today"`
	ProxyUptimeSeconds   int64                    `json:"proxy_uptime_seconds"`
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w5h, w7d := a.state.Snapshot()
	now := time.Now()
	staleThreshold := time.Duration(a.cfg.RateLimits.StaleAfterSeconds) * time.Second

	makeWindow := func(ws ratelimit.WindowState, limit float64) windowStatus {
		stale := ws.ObservedAt.IsZero() || now.Sub(ws.ObservedAt) > staleThreshold
		headroom := limit - ws.LocalUtil
		if headroom < 0 {
			headroom = 0
		}
		pct := 0.0
		if limit > 0 {
			pct = ws.LocalUtil / limit * 100
		}
		resetAt := ""
		if !ws.ResetAt.IsZero() {
			resetAt = ws.ResetAt.UTC().Format(time.RFC3339)
		}
		observedAt := ""
		if !ws.ObservedAt.IsZero() {
			observedAt = ws.ObservedAt.UTC().Format(time.RFC3339)
		}
		return windowStatus{
			Utilization:        ws.LocalUtil,
			AccountUtilization: ws.Utilization,
			Limit:              limit,
			Headroom:           headroom,
			PctOfLimitUsed:     pct,
			ResetAt:            resetAt,
			Stale:              stale,
			ObservedAt:         observedAt,
		}
	}

	resp := statusResponse{
		InstanceLimit: a.cfg.RateLimits.InstanceShare,
		Windows: map[string]windowStatus{
			"5h": makeWindow(w5h, a.cfg.RateLimits.Windows.H5.HardLimit),
			"7d": makeWindow(w7d, a.cfg.RateLimits.Windows.D7.HardLimit),
		},
		BlockedRequestsToday: a.proxy.BlockedCount(),
		ProxyUptimeSeconds:   int64(time.Since(a.startedAt).Seconds()),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("claude-meter: encode status response: %v", err)
	}
}

func (a *App) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (a *App) handleReset(w http.ResponseWriter, _ *http.Request) {
	a.state.Reset()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true,"message":"local utilization counters reset"}` + "\n"))
}

func (a *App) Close() error {
	a.closeOnce.Do(func() {
		close(a.exchanges)
		a.wg.Wait()
	})
	return nil
}
