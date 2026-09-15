package config

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestWebCodexRunnerRecoveryGraceConfiguration(t *testing.T) {
	for _, test := range []struct {
		seconds int64
		valid   bool
	}{{0, true}, {1, true}, {60, true}, {-1, false}, {math.MaxInt64, false}} {
		t.Run(fmt.Sprint(test.seconds), func(t *testing.T) {
			source := fmt.Sprintf("webcodex_runner:\n  enabled: true\n  max_runners: 4\n  max_pending_per_runner: 8\n  max_jobs_per_runner: 3\n  online_window_seconds: 30\n  max_body_bytes: 65536\n  max_token_bytes: 1024\n  job_recovery_grace_seconds: %d\n", test.seconds)
			v := viper.New()
			v.SetConfigType("yaml")
			require.NoError(t, v.ReadConfig(strings.NewReader(source)))
			var c Config
			require.NoError(t, v.Unmarshal(&c))
			require.Equal(t, test.seconds, c.WebCodexRunner.JobRecoveryGraceSeconds)
			if test.valid {
				require.NoError(t, c.WebCodexRunner.Validate())
			} else {
				require.ErrorContains(t, c.WebCodexRunner.Validate(), "job_recovery_grace_seconds")
			}
			require.Equal(t, 3, c.WebCodexRunner.MaxJobsPerRunner)
			require.Equal(t, 8, c.WebCodexRunner.MaxPendingPerRunner)
		})
	}
}
