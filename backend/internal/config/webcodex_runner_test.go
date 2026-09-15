package config

import (
	"math"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func validWebCodexRunnerConfig() WebCodexRunnerConfig {
	return WebCodexRunnerConfig{Enabled: true, MaxRunners: 4, MaxPendingPerRunner: 8, MaxJobsPerRunner: 12,
		OnlineWindowSeconds: 30, MaxBodyBytes: 65536, MaxTokenBytes: 1024}
}

func TestWebCodexRunnerConfigRequiresExplicitBounds(t *testing.T) {
	require.NoError(t, (WebCodexRunnerConfig{}).Validate())
	require.NoError(t, validWebCodexRunnerConfig().Validate())
	for _, tc := range []struct {
		name   string
		change func(*WebCodexRunnerConfig)
	}{
		{"max_runners", func(c *WebCodexRunnerConfig) { c.MaxRunners = 0 }},
		{"max_pending_per_runner", func(c *WebCodexRunnerConfig) { c.MaxPendingPerRunner = -1 }},
		{"max_jobs_per_runner zero", func(c *WebCodexRunnerConfig) { c.MaxJobsPerRunner = 0 }},
		{"max_jobs_per_runner negative", func(c *WebCodexRunnerConfig) { c.MaxJobsPerRunner = -1 }},
		{"online_window_seconds", func(c *WebCodexRunnerConfig) { c.OnlineWindowSeconds = 0 }},
		{"online_window_seconds overflow", func(c *WebCodexRunnerConfig) { c.OnlineWindowSeconds = math.MaxInt64 }},
		{"max_body_bytes", func(c *WebCodexRunnerConfig) { c.MaxBodyBytes = 0 }},
		{"max_token_bytes", func(c *WebCodexRunnerConfig) { c.MaxTokenBytes = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := validWebCodexRunnerConfig()
			tc.change(&c)
			require.ErrorContains(t, (&Config{WebCodexRunner: c}).Validate(), "webcodex_runner."+strings.Fields(tc.name)[0])
		})
	}
}

func TestWebCodexRunnerEnvironmentOnlyConfiguration(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	setDefaults()
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	t.Setenv("WEBCODEX_RUNNER_ENABLED", "false")
	var disabled Config
	require.NoError(t, viper.Unmarshal(&disabled))
	require.False(t, disabled.WebCodexRunner.Enabled)
	for key, value := range map[string]string{
		"ENABLED": "true", "MAX_RUNNERS": "4", "MAX_PENDING_PER_RUNNER": "8", "MAX_JOBS_PER_RUNNER": "12",
		"ONLINE_WINDOW_SECONDS": "30", "MAX_BODY_BYTES": "65536", "MAX_TOKEN_BYTES": "1024",
	} {
		t.Setenv("WEBCODEX_RUNNER_"+key, value)
	}
	var enabled Config
	require.NoError(t, viper.Unmarshal(&enabled))
	require.Equal(t, validWebCodexRunnerConfig(), enabled.WebCodexRunner)
	require.NoError(t, enabled.WebCodexRunner.Validate())
	// No fallback to max_pending_per_runner when the independent Job bound is absent.
	t.Setenv("WEBCODEX_RUNNER_MAX_JOBS_PER_RUNNER", "0")
	var missing Config
	require.NoError(t, viper.Unmarshal(&missing))
	require.Equal(t, 8, missing.WebCodexRunner.MaxPendingPerRunner)
	require.Zero(t, missing.WebCodexRunner.MaxJobsPerRunner)
	require.ErrorContains(t, missing.WebCodexRunner.Validate(), "max_jobs_per_runner")
	t.Setenv("WEBCODEX_RUNNER_ENABLED", "false")
	require.NoError(t, viper.Unmarshal(&missing))
	require.NoError(t, missing.WebCodexRunner.Validate())
}
