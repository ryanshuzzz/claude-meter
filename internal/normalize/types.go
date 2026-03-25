package normalize

import "time"

type Record struct {
	ID                uint64    `json:"id"`
	RequestTimestamp  time.Time `json:"request_timestamp"`
	ResponseTimestamp time.Time `json:"response_timestamp"`
	Method            string    `json:"method"`
	Path              string    `json:"path"`
	Status            int       `json:"status"`
	LatencyMS         int64     `json:"latency_ms"`
	RequestModel      string    `json:"request_model,omitempty"`
	ResponseModel     string    `json:"response_model,omitempty"`
	SessionID         string    `json:"session_id,omitempty"`
	DeclaredPlanTier  string    `json:"declared_plan_tier,omitempty"`
	RequestID         string    `json:"request_id,omitempty"`
	Usage             Usage     `json:"usage"`
	Ratelimit         Ratelimit `json:"ratelimit"`
}

type Usage struct {
	InputTokens              int `json:"input_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens,omitempty"`
}

type Ratelimit struct {
	Status                string                     `json:"status,omitempty"`
	RepresentativeClaim   string                     `json:"representative_claim,omitempty"`
	FallbackPercentage    float64                    `json:"fallback_percentage,omitempty"`
	OverageDisabledReason string                     `json:"overage_disabled_reason,omitempty"`
	OverageStatus         string                     `json:"overage_status,omitempty"`
	RetryAfterS           int                        `json:"retry_after_s,omitempty"`
	Windows               map[string]RatelimitWindow `json:"windows,omitempty"`
}

type RatelimitWindow struct {
	Status             string  `json:"status,omitempty"`
	ResetTS            int64   `json:"reset_ts,omitempty"`
	Utilization        float64 `json:"utilization,omitempty"`
	SurpassedThreshold bool    `json:"surpassed_threshold,omitempty"`
}
