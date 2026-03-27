package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	t.Parallel()

	cfg := defaults()

	if !cfg.RateLimits.Enabled {
		t.Error("expected enabled=true by default")
	}
	if cfg.RateLimits.InstanceShare != 0.25 {
		t.Errorf("instance_share = %g, want 0.25", cfg.RateLimits.InstanceShare)
	}
	if cfg.RateLimits.Windows.H5.HardLimit != 0.25 {
		t.Errorf("5h hard_limit = %g, want 0.25", cfg.RateLimits.Windows.H5.HardLimit)
	}
	if cfg.RateLimits.Windows.H5.WarnThreshold != 0.20 {
		t.Errorf("5h warn_threshold = %g, want 0.20", cfg.RateLimits.Windows.H5.WarnThreshold)
	}
	if cfg.RateLimits.Windows.D7.HardLimit != 0.25 {
		t.Errorf("7d hard_limit = %g, want 0.25", cfg.RateLimits.Windows.D7.HardLimit)
	}
	if cfg.RateLimits.StaleAfterSeconds != 300 {
		t.Errorf("stale_after_seconds = %d, want 300", cfg.RateLimits.StaleAfterSeconds)
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Parallel()

	cfg, err := LoadFrom(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v, want nil (should use defaults)", err)
	}
	if cfg.RateLimits.InstanceShare != 0.25 {
		t.Errorf("instance_share = %g, want 0.25 (defaults)", cfg.RateLimits.InstanceShare)
	}
}

func TestLoadFromYAML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")

	yamlContent := `rate_limits:
  enabled: false
  instance_share: 0.33
  windows:
    5h:
      enabled: true
      hard_limit: 0.33
      warn_threshold: 0.25
    7d:
      enabled: false
      hard_limit: 0.33
      warn_threshold: 0.30
  stale_after_seconds: 600
`
	if err := os.WriteFile(cfgFile, []byte(yamlContent), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(cfgFile)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}

	if cfg.RateLimits.Enabled {
		t.Error("expected enabled=false from yaml")
	}
	if cfg.RateLimits.InstanceShare != 0.33 {
		t.Errorf("instance_share = %g, want 0.33", cfg.RateLimits.InstanceShare)
	}
	if !cfg.RateLimits.Windows.H5.Enabled {
		t.Error("expected 5h enabled=true from yaml")
	}
	if cfg.RateLimits.Windows.D7.Enabled {
		t.Error("expected 7d enabled=false from yaml")
	}
	if cfg.RateLimits.Windows.D7.WarnThreshold != 0.30 {
		t.Errorf("7d warn_threshold = %g, want 0.30", cfg.RateLimits.Windows.D7.WarnThreshold)
	}
	if cfg.RateLimits.StaleAfterSeconds != 600 {
		t.Errorf("stale_after_seconds = %d, want 600", cfg.RateLimits.StaleAfterSeconds)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("CLAUDE_METER_INSTANCE_SHARE", "0.5")
	t.Setenv("CLAUDE_METER_5H_LIMIT", "0.4")
	t.Setenv("CLAUDE_METER_7D_LIMIT", "0.3")

	cfg, err := LoadFrom(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}

	if cfg.RateLimits.InstanceShare != 0.5 {
		t.Errorf("instance_share = %g, want 0.5", cfg.RateLimits.InstanceShare)
	}
	if cfg.RateLimits.Windows.H5.HardLimit != 0.4 {
		t.Errorf("5h hard_limit = %g, want 0.4", cfg.RateLimits.Windows.H5.HardLimit)
	}
	if cfg.RateLimits.Windows.D7.HardLimit != 0.3 {
		t.Errorf("7d hard_limit = %g, want 0.3", cfg.RateLimits.Windows.D7.HardLimit)
	}
}

func TestEnvOverridePrecedence(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgFile, []byte("rate_limits:\n  instance_share: 0.10\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CLAUDE_METER_INSTANCE_SHARE", "0.5")
	// set limits lower so validation passes
	t.Setenv("CLAUDE_METER_5H_LIMIT", "0.4")
	t.Setenv("CLAUDE_METER_7D_LIMIT", "0.4")

	cfg, err := LoadFrom(cfgFile)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	// env var should win over yaml file value
	if cfg.RateLimits.InstanceShare != 0.5 {
		t.Errorf("instance_share = %g, want 0.5 (env should override yaml)", cfg.RateLimits.InstanceShare)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{
			name:    "valid defaults",
			mutate:  func(c *Config) {},
			wantErr: false,
		},
		{
			name: "instance_share above 1.0",
			mutate: func(c *Config) {
				c.RateLimits.InstanceShare = 1.5
			},
			wantErr: true,
		},
		{
			name: "instance_share below 0.0",
			mutate: func(c *Config) {
				c.RateLimits.InstanceShare = -0.1
			},
			wantErr: true,
		},
		{
			name: "5h hard_limit exceeds instance_share",
			mutate: func(c *Config) {
				c.RateLimits.InstanceShare = 0.2
				c.RateLimits.Windows.H5.HardLimit = 0.3
			},
			wantErr: true,
		},
		{
			name: "7d hard_limit exceeds instance_share",
			mutate: func(c *Config) {
				c.RateLimits.InstanceShare = 0.2
				c.RateLimits.Windows.D7.HardLimit = 0.3
			},
			wantErr: true,
		},
		{
			name: "hard_limit equals instance_share (valid)",
			mutate: func(c *Config) {
				c.RateLimits.InstanceShare = 0.5
				c.RateLimits.Windows.H5.HardLimit = 0.5
				c.RateLimits.Windows.D7.HardLimit = 0.5
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaults()
			tt.mutate(&cfg)
			err := validate(&cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestInvalidYAML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgFile, []byte("{unclosed mapping"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadFrom(cfgFile)
	if err == nil {
		t.Error("LoadFrom() should return error for invalid YAML")
	}
}
