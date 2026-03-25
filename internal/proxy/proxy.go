package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"claude-meter-proxy/internal/capture"
)

type Config struct {
	UpstreamBaseURL *url.URL
	Client          *http.Client
	CaptureCh       chan<- capture.CompletedExchange
}

type Server struct {
	upstreamBaseURL *url.URL
	client          *http.Client
	captureCh       chan<- capture.CompletedExchange
	nextID          atomic.Uint64
	droppedCount    atomic.Uint64
}

func New(cfg Config) *Server {
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}

	return &Server{
		upstreamBaseURL: cfg.UpstreamBaseURL,
		client:          client,
		captureCh:       cfg.CaptureCh,
	}
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.ServeHTTP)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now().UTC()
	requestID := s.nextID.Add(1)

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
