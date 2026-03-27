package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration structure.
type Config struct {
	RateLimits RateLimitConfig `yaml:"rate_limits"`
}

// RateLimitConfig holds rate limiting configuration.
type RateLimitConfig struct {
	Enabled           bool            `yaml:"enabled"`
	InstanceShare     float64         `yaml:"instance_share"`
	Windows           WindowsConfig   `yaml:"windows"`
	OnLimitExceeded   OnLimitExceeded `yaml:"on_limit_exceeded"`
	StaleAfterSeconds int             `yaml:"stale_after_seconds"`
}

// WindowsConfig holds per-window configuration.
type WindowsConfig struct {
	H5 WindowConfig `yaml:"5h"`
	D7 WindowConfig `yaml:"7d"`
}

// WindowConfig holds configuration for a single rate limit window.
type WindowConfig struct {
	Enabled       bool    `yaml:"enabled"`
	HardLimit     float64 `yaml:"hard_limit"`
	WarnThreshold float64 `yaml:"warn_threshold"`
}

// OnLimitExceeded controls the response when the rate limit is exceeded.
type OnLimitExceeded struct {
	HTTPStatus        int    `yaml:"http_status"`
	Message           string `yaml:"message"`
	IncludeRetryAfter bool   `yaml:"include_retry_after"`
}

func defaults() Config {
	return Config{
		RateLimits: RateLimitConfig{
			Enabled:       true,
			InstanceShare: 0.25,
			Windows: WindowsConfig{
				H5: WindowConfig{
					Enabled:       true,
					HardLimit:     0.25,
					WarnThreshold: 0.20,
				},
				D7: WindowConfig{
					Enabled:       true,
					HardLimit:     0.25,
					WarnThreshold: 0.20,
				},
			},
			OnLimitExceeded: OnLimitExceeded{
				HTTPStatus:        429,
				Message:           "Instance rate limit reached (25% of account budget consumed). Retry after window resets.",
				IncludeRetryAfter: true,
			},
			StaleAfterSeconds: 300,
		},
	}
}

// Load loads configuration from the default path (~/.claude-meter/config.yaml).
// If the file does not exist, defaults are used.
func Load() (*Config, error) {
	return LoadFrom(defaultConfigPath())
}

// LoadFrom loads configuration from the given YAML file path.
// If the file does not exist, defaults are used.
func LoadFrom(path string) (*Config, error) {
	cfg := defaults()

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	if err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parse config file: %w", err)
		}
	}

	applyEnvOverrides(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude-meter/config.yaml"
	}
	return filepath.Join(home, ".claude-meter", "config.yaml")
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("CLAUDE_METER_INSTANCE_SHARE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.RateLimits.InstanceShare = f
		}
	}
	if v := os.Getenv("CLAUDE_METER_5H_LIMIT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.RateLimits.Windows.H5.HardLimit = f
		}
	}
	if v := os.Getenv("CLAUDE_METER_7D_LIMIT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.RateLimits.Windows.D7.HardLimit = f
		}
	}
}

func validate(cfg *Config) error {
	rl := &cfg.RateLimits
	if rl.InstanceShare < 0.0 || rl.InstanceShare > 1.0 {
		return fmt.Errorf("config: instance_share must be between 0.0 and 1.0, got %g", rl.InstanceShare)
	}
	if rl.Windows.H5.HardLimit > rl.InstanceShare {
		return fmt.Errorf("config: 5h hard_limit (%g) must be <= instance_share (%g)", rl.Windows.H5.HardLimit, rl.InstanceShare)
	}
	if rl.Windows.D7.HardLimit > rl.InstanceShare {
		return fmt.Errorf("config: 7d hard_limit (%g) must be <= instance_share (%g)", rl.Windows.D7.HardLimit, rl.InstanceShare)
	}
	return nil
}
