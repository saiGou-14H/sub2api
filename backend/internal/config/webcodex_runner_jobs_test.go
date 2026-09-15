package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestWebCodexRunnerYAMLRequiresIndependentJobBudget(t *testing.T) {
	for _, test := range []struct {
		name, setting string
		valid         bool
	}{
		{"omitted", "", false}, {"zero", "  max_jobs_per_runner: 0\n", false}, {"negative", "  max_jobs_per_runner: -1\n", false}, {"independent positive", "  max_jobs_per_runner: 3\n", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "webcodex_runner:\n  enabled: true\n  max_runners: 4\n  max_pending_per_runner: 8\n" + test.setting + "  online_window_seconds: 30\n  max_body_bytes: 65536\n  max_token_bytes: 1024\n"
			v := viper.New()
			v.SetConfigType("yaml")
			require.NoError(t, v.ReadConfig(strings.NewReader(source)))
			var config Config
			require.NoError(t, v.Unmarshal(&config))
			require.Equal(t, 8, config.WebCodexRunner.MaxPendingPerRunner)
			if test.valid {
				require.Equal(t, 3, config.WebCodexRunner.MaxJobsPerRunner)
				require.NoError(t, config.WebCodexRunner.Validate())
			} else {
				require.ErrorContains(t, config.WebCodexRunner.Validate(), "max_jobs_per_runner")
			}
		})
	}
}
