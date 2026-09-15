//go:build unit

package server

import (
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
	"github.com/Wei-Shaw/sub2api/internal/webcodex/runner"
	"github.com/stretchr/testify/require"
)

func TestWebCodexRunnerConstructsSeparateJobAndPendingBudgets(t *testing.T) {
	runtime, router, mock, _ := webCodexSetup(t)
	first := webCodexStartMountedJob(t, runtime, router, mock)
	cfg := webCodexTestConfig().WebCodexRunner
	require.Equal(t, 2, cfg.MaxJobsPerRunner)
	require.Equal(t, 4, cfg.MaxPendingPerRunner)
	start := func(id string) (protocol.ShellJobInfo, error) {
		operation := protocol.JobProcessOperation{JobID: id, Process: protocol.ShellProcessArgv{Executable: "printf", Args: []string{"capacity fixture"}}, TimeoutSecs: 30, Context: protocol.ShellJobContext{CommandPreview: "fixture"}}
		expected := operation.ExpectedStructuredExecution()
		operation.Context.StructuredExecution = &expected
		return runtime.registry.StartProcessJob(runner.Access{Username: webCodexFixtureOwner}, protocol.JobInvocation{Metadata: protocol.InvocationMetadata{ClientID: "node-a", RequestID: "request-" + id, RequestedBy: webCodexFixtureOwner}, Operation: operation})
	}
	_, err := start("second")
	require.NoError(t, err)
	_, err = start("overflow")
	require.ErrorIs(t, err, runner.ErrCapacity)
	before, err := runtime.registry.GetJob(runner.Access{Username: webCodexFixtureOwner}, first.JobID)
	require.NoError(t, err)
	require.Equal(t, "queued", before.Status)
	webCodexPollMountedJob(t, router, mock)
	// One queued Job plus three synchronous requests fills the separate budget of four.
	for i := 0; i < 3; i++ {
		_, err := runtime.registry.Enqueue(runner.Access{Username: webCodexFixtureOwner}, protocol.RunnerRequest{ClientID: "node-a", RequestID: fmt.Sprintf("pending-%d", i), Kind: "run_shell", Command: "printf fixture", TimeoutSecs: 1, RequestedBy: webCodexFixtureOwner})
		require.NoError(t, err)
	}
	_, err = runtime.registry.Enqueue(runner.Access{Username: webCodexFixtureOwner}, protocol.RunnerRequest{ClientID: "node-a", RequestID: "pending-overflow", Kind: "run_shell", Command: "printf fixture", TimeoutSecs: 1, RequestedBy: webCodexFixtureOwner})
	require.ErrorIs(t, err, runner.ErrCapacity)
}

func TestWebCodexRunnerJobBudgetValidatedBeforeDependencies(t *testing.T) {
	for _, value := range []int{0, -1} {
		cfg := webCodexTestConfig()
		cfg.WebCodexRunner.MaxJobsPerRunner = value
		_, err := ProvideWebCodexRunner(cfg, nil, nil)
		require.ErrorContains(t, err, "max_jobs_per_runner")
		cfg.WebCodexRunner.Enabled = false
		runtime, err := ProvideWebCodexRunner(cfg, nil, nil)
		require.NoError(t, err)
		require.Nil(t, runtime.registry)
		runtime.Close()
	}
}
