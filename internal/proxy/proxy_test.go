package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"claude-meter-proxy/internal/capture"
)

func TestProxyForwardsRequestAndResponse(t *testing.T) {
	t.Parallel()

	var gotMethod string
	var gotPath string
	var gotHeader string
	var gotBody string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.RequestURI()
		gotHeader = r.Header.Get("X-Test-Header")

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		gotBody = string(body)

		w.Header().Set("X-Upstream", "ok")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("proxied response"))
	}))
	defer upstream.Close()

	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}

	server := New(Config{
		UpstreamBaseURL: upstreamURL,
	})

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/messages?beta=true", strings.NewReader("hello upstream"))
	req.Header.Set("X-Test-Header", "present")

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)

	resp := recorder.Result()
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() response error = %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("upstream method = %q, want %q", gotMethod, http.MethodPost)
	}
	if gotPath != "/v1/messages?beta=true" {
		t.Fatalf("upstream path = %q, want %q", gotPath, "/v1/messages?beta=true")
	}
	if gotHeader != "present" {
		t.Fatalf("upstream header = %q, want %q", gotHeader, "present")
	}
	if gotBody != "hello upstream" {
		t.Fatalf("upstream body = %q, want %q", gotBody, "hello upstream")
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("response status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	if resp.Header.Get("X-Upstream") != "ok" {
		t.Fatalf("response header = %q, want %q", resp.Header.Get("X-Upstream"), "ok")
	}
	if string(respBody) != "proxied response" {
		t.Fatalf("response body = %q, want %q", string(respBody), "proxied response")
	}
}

func TestProxyCapturesCompletedExchange(t *testing.T) {
	t.Parallel()

	captureCh := make(chan capture.CompletedExchange, 1)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}

	server := New(Config{
		UpstreamBaseURL: upstreamURL,
		CaptureCh:       captureCh,
	})

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/messages?beta=true", strings.NewReader(`{"hello":"world"}`))
	recorder := httptest.NewRecorder()

	server.Handler().ServeHTTP(recorder, req)

	select {
	case exchange := <-captureCh:
		if exchange.Request.Method != http.MethodPost {
			t.Fatalf("Request.Method = %q, want %q", exchange.Request.Method, http.MethodPost)
		}
		if exchange.Request.Path != "/v1/messages?beta=true" {
			t.Fatalf("Request.Path = %q, want %q", exchange.Request.Path, "/v1/messages?beta=true")
		}
		if string(exchange.Request.Body) != `{"hello":"world"}` {
			t.Fatalf("Request.Body = %q, want %q", string(exchange.Request.Body), `{"hello":"world"}`)
		}
		if exchange.Response.Status != http.StatusOK {
			t.Fatalf("Response.Status = %d, want %d", exchange.Response.Status, http.StatusOK)
		}
		if string(exchange.Response.Body) != `{"ok":true}` {
			t.Fatalf("Response.Body = %q, want %q", string(exchange.Response.Body), `{"ok":true}`)
		}
		if exchange.DurationMS < 0 {
			t.Fatalf("DurationMS = %d, want non-negative", exchange.DurationMS)
		}
	default:
		t.Fatal("expected one captured exchange")
	}
}
