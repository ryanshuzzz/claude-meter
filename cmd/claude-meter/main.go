package main

import (
	"context"
	"encoding/json"
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
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <start|status|reset|backfill-normalized> [options]\n", os.Args[0])
		os.Exit(2)
	}

	switch os.Args[1] {
	case "start":
		runStart(os.Args[2:])
	case "status":
		runStatus(os.Args[2:])
	case "reset":
		runReset(os.Args[2:])
	case "backfill-normalized":
		if err := runBackfillNormalized(os.Args[2:]); err != nil {
			log.Fatalf("backfill-normalized: %v", err)
		}
	default:
		fmt.Fprintf(os.Stderr, "usage: %s <start|status|reset|backfill-normalized> [options]\n", os.Args[0])
		os.Exit(2)
	}
}

func runStart(args []string) {
	startFlags := flag.NewFlagSet("start", flag.ExitOnError)
	port := startFlags.Int("port", 7735, "port to listen on")
	upstream := startFlags.String("upstream", "https://api.anthropic.com", "Anthropic upstream base URL")
	logDir := startFlags.String("log-dir", defaultLogDir(), "base log directory")
	queueSize := startFlags.Int("queue-size", 256, "in-memory completed exchange buffer")
	planTier := startFlags.String("plan-tier", "unknown", "declared plan tier for normalized records")
	instanceShare := startFlags.Float64("instance-share", 0, "override instance share of account budget (0 = use config file default)")
	startFlags.Parse(args)

	if *instanceShare != 0 && (*instanceShare < 0.01 || *instanceShare > 1.0) {
		log.Fatalf("instance-share must be between 0.01 and 1.0, got %g", *instanceShare)
	}

	upstreamURL, err := url.Parse(*upstream)
	if err != nil {
		log.Fatalf("parse upstream URL: %v", err)
	}

	application, err := app.New(app.Config{
		UpstreamBaseURL: upstreamURL,
		LogDir:          expandHome(*logDir),
		QueueSize:       *queueSize,
		PlanTier:        *planTier,
		InstanceShare:   *instanceShare,
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
		log.Printf("declared plan tier: %s", *planTier)
		if *instanceShare > 0 {
			log.Printf("instance share override: %.0f%%", *instanceShare*100)
		}
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

// statusWindowJSON is the JSON shape for one window in the /status response.
type statusWindowJSON struct {
	Utilization        float64 `json:"utilization"`
	AccountUtilization float64 `json:"account_utilization"`
	Limit              float64 `json:"limit"`
	Headroom           float64 `json:"headroom"`
	PctOfLimitUsed     float64 `json:"pct_of_limit_used"`
	ResetAt            string  `json:"reset_at"`
	Stale              bool    `json:"stale"`
	ObservedAt         string  `json:"observed_at"`
}

// statusJSON is the JSON body of the /status endpoint.
type statusJSON struct {
	InstanceLimit        float64                      `json:"instance_limit"`
	Windows              map[string]statusWindowJSON  `json:"windows"`
	BlockedRequestsToday int64                        `json:"blocked_requests_today"`
	ProxyUptimeSeconds   int64                        `json:"proxy_uptime_seconds"`
}

func runStatus(args []string) {
	statusFlags := flag.NewFlagSet("status", flag.ExitOnError)
	port := statusFlags.Int("port", 7735, "proxy port to query")
	statusFlags.Parse(args)

	statusURL := fmt.Sprintf("http://127.0.0.1:%d/status", *port)
	resp, err := http.Get(statusURL) //nolint:gosec
	if err != nil {
		log.Fatalf("status: connect to %s: %v", statusURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Fatalf("status: server returned %d", resp.StatusCode)
	}

	var status statusJSON
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		log.Fatalf("status: decode response: %v", err)
	}

	printStatus(status)
}

func printStatus(s statusJSON) {
	fmt.Println("claude-meter status")
	fmt.Println("====================")
	fmt.Printf("Instance share:  %.1f%% of account\n", s.InstanceLimit*100)
	fmt.Println()

	for _, wname := range []string{"5h", "7d"} {
		w, ok := s.Windows[wname]
		if !ok {
			continue
		}
		fmt.Printf("%s Window\n", wname)
		bar := progressBar(w.PctOfLimitUsed, 16)
		fmt.Printf("  This instance: %.1f%% / %.1f%% cap  [%s]  %.0f%% of budget used\n",
			w.Utilization*100, w.Limit*100, bar, w.PctOfLimitUsed)
		fmt.Printf("  Account-wide:  %.1f%%\n", w.AccountUtilization*100)
		fmt.Printf("  Headroom:      %.1f%% remaining\n", w.Headroom*100)
		if w.ResetAt != "" {
			resetAt, err := time.Parse(time.RFC3339, w.ResetAt)
			if err == nil {
				remaining := time.Until(resetAt)
				fmt.Printf("  Resets:        in %s\n", formatTimeRemaining(remaining))
			}
		}
		if w.Stale {
			fmt.Printf("  (stale — no recent data)\n")
		}
		fmt.Println()
	}

	fmt.Printf("Blocked today:  %d requests\n", s.BlockedRequestsToday)
	fmt.Printf("Uptime:         %s\n", formatTimeRemaining(time.Duration(s.ProxyUptimeSeconds)*time.Second))
}

func progressBar(pct float64, width int) string {
	filled := int(pct / 100.0 * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func formatTimeRemaining(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func runReset(args []string) {
	resetFlags := flag.NewFlagSet("reset", flag.ExitOnError)
	port := resetFlags.Int("port", 7735, "proxy port to query")
	resetFlags.Parse(args)

	resetURL := fmt.Sprintf("http://127.0.0.1:%d/reset", *port)
	resp, err := http.Post(resetURL, "application/json", nil) //nolint:gosec
	if err != nil {
		log.Fatalf("reset: connect to %s: %v", resetURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Fatalf("reset: server returned %d", resp.StatusCode)
	}

	fmt.Println("Local utilization counters reset successfully.")
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
