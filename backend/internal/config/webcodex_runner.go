package config

import (
	"fmt"
	"math"
	"time"
)

// WebCodexRunnerConfig enables the original managed-credential polling routes.
// Disabled is the default. Enabled installations supply every operational bound;
// these are host resource limits, not additions to the Runner wire protocol.
type WebCodexRunnerConfig struct {
	Enabled             bool  `mapstructure:"enabled"`
	MaxRunners          int   `mapstructure:"max_runners"`
	MaxPendingPerRunner int   `mapstructure:"max_pending_per_runner"`
	MaxJobsPerRunner    int   `mapstructure:"max_jobs_per_runner"`
	OnlineWindowSeconds int64 `mapstructure:"online_window_seconds"`
	MaxBodyBytes        int64 `mapstructure:"max_body_bytes"`
	MaxTokenBytes       int   `mapstructure:"max_token_bytes"`
}

// Validate requires explicit positive bounds before exposing any Runner route.
func (c WebCodexRunnerConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	checks := []struct {
		name  string
		valid bool
	}{
		{"max_runners", c.MaxRunners > 0},
		{"max_pending_per_runner", c.MaxPendingPerRunner > 0},
		{"max_jobs_per_runner", c.MaxJobsPerRunner > 0},
		{"online_window_seconds", c.OnlineWindowSeconds > 0 && c.OnlineWindowSeconds <= math.MaxInt64/int64(time.Second)},
		{"max_body_bytes", c.MaxBodyBytes > 0},
		{"max_token_bytes", c.MaxTokenBytes > 0},
	}
	for _, check := range checks {
		if !check.valid {
			return fmt.Errorf("webcodex_runner.%s must be positive and representable", check.name)
		}
	}
	return nil
}
