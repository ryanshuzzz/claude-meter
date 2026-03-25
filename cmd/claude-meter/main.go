package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"claude-meter-proxy/internal/app"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "start" {
		fmt.Fprintf(os.Stderr, "usage: %s start [--port PORT] [--upstream URL] [--log-dir DIR]\n", os.Args[0])
		os.Exit(2)
	}

	startFlags := flag.NewFlagSet("start", flag.ExitOnError)
	port := startFlags.Int("port", 7735, "port to listen on")
	upstream := startFlags.String("upstream", "https://api.anthropic.com", "Anthropic upstream base URL")
	logDir := startFlags.String("log-dir", defaultLogDir(), "base log directory")
	queueSize := startFlags.Int("queue-size", 256, "in-memory completed exchange buffer")
	startFlags.Parse(os.Args[2:])

	upstreamURL, err := url.Parse(*upstream)
	if err != nil {
		log.Fatalf("parse upstream URL: %v", err)
	}

	application, err := app.New(app.Config{
		UpstreamBaseURL: upstreamURL,
		LogDir:          expandHome(*logDir),
		QueueSize:       *queueSize,
	})
	if err != nil {
		log.Fatalf("create app: %v", err)
	}
	defer application.Close()

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	server := &http.Server{
		Addr:    addr,
		Handler: application.Handler(),
	}

	go func() {
		log.Printf("claude-meter proxy listening on http://%s", addr)
		log.Printf("forwarding to %s", upstreamURL.String())
		log.Printf("writing raw exchanges under %s", expandHome(*logDir))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = server.Shutdown(ctx)
}

func defaultLogDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude-meter"
	}
	return filepath.Join(home, ".claude-meter")
}

func expandHome(path string) string {
	if path == "~" {
		return defaultLogDir()
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
