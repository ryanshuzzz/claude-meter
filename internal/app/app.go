package app

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"

	"claude-meter-proxy/internal/capture"
	"claude-meter-proxy/internal/normalize"
	"claude-meter-proxy/internal/proxy"
	"claude-meter-proxy/internal/storage"
)

type Config struct {
	UpstreamBaseURL *url.URL
	LogDir          string
	QueueSize       int
	PlanTier        string
	Client          *http.Client
}

type App struct {
	proxy     *proxy.Server
	exchanges chan capture.CompletedExchange

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

	rawWriter, err := storage.NewRawExchangeWriter(filepath.Join(cfg.LogDir, "raw"))
	if err != nil {
		return nil, err
	}
	normalizedWriter, err := storage.NewNormalizedRecordWriter(filepath.Join(cfg.LogDir, "normalized"))
	if err != nil {
		return nil, err
	}
	normalizer := normalize.New(cfg.PlanTier)

	exchanges := make(chan capture.CompletedExchange, cfg.QueueSize)
	app := &App{
		exchanges: exchanges,
	}

	app.proxy = proxy.New(proxy.Config{
		UpstreamBaseURL: cfg.UpstreamBaseURL,
		Client:          cfg.Client,
		CaptureCh:       exchanges,
	})

	app.wg.Add(1)
	go func() {
		defer app.wg.Done()
		for exchange := range exchanges {
			_ = rawWriter.Write(exchange)
			_ = normalizedWriter.Write(normalizer.Normalize(exchange))
		}
	}()

	return app, nil
}

func (a *App) Handler() http.Handler {
	return a.proxy.Handler()
}

func (a *App) Close() error {
	a.closeOnce.Do(func() {
		close(a.exchanges)
		a.wg.Wait()
	})
	return nil
}
