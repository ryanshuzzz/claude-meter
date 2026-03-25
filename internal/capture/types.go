package capture

import "time"

type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type RecordedRequest struct {
	Method  string   `json:"method"`
	Path    string   `json:"path"`
	Headers []Header `json:"headers,omitempty"`
	Body    []byte   `json:"body,omitempty"`
}

type RecordedResponse struct {
	Status  int      `json:"status"`
	Headers []Header `json:"headers,omitempty"`
	Body    []byte   `json:"body,omitempty"`
}

type CompletedExchange struct {
	ID               uint64           `json:"id"`
	RequestStartedAt time.Time        `json:"request_started_at"`
	ResponseEndedAt  time.Time        `json:"response_ended_at"`
	DurationMS       int64            `json:"duration_ms"`
	Request          RecordedRequest  `json:"request"`
	Response         RecordedResponse `json:"response"`
}
