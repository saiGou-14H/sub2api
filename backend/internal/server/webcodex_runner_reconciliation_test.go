//go:build unit

// SPDX-License-Identifier: Apache-2.0
package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
	"github.com/Wei-Shaw/sub2api/internal/webcodex/runner"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWebCodexRunnerMountedReconciliation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()); _ = db.Close() })
	cfg := webCodexTestConfig()
	cfg.WebCodexRunner.JobRecoveryGraceSeconds = 60
	runtime, err := ProvideWebCodexRunner(cfg, db, &webCodexTestUsers{status: "active"})
	require.NoError(t, err)
	t.Cleanup(runtime.Close)
	router := gin.New()
	runtime.mount(router)
	// This is a fictional full-capability node using the real managed verifier.
	queued := webCodexStartMountedJob(t, runtime, router, mock)
	webCodexPollMountedJob(t, router, mock)
	reg, err := protocol.ReadRegisterRequest([]byte(webCodexRegistration))
	require.NoError(t, err)
	reg.Capabilities.JobStateReconciliation = true
	stream := protocol.ShellJobStreamSnapshot{FirstRetainedLine: 1, NextLine: 1}
	snapshot := protocol.ShellJobSnapshot{JobID: queued.JobID, RequestID: *queued.RequestID, Status: "running", UpdateSeq: 1, CreatedAt: queued.CreatedAt, StartedAt: webCodexJobPtr(queued.CreatedAt), Context: protocol.ShellJobContext{CommandPreview: queued.CommandPreview, StructuredExecution: queued.StructuredExecution}, Stdout: stream, Stderr: stream}
	inventory := protocol.ShellJobInventory{ActiveComplete: true, Jobs: []protocol.ShellJobSnapshot{snapshot}}
	register := func(want int) {
		reg.JobInventory, err = json.Marshal(inventory)
		require.NoError(t, err)
		body, e := json.Marshal(reg)
		require.NoError(t, e)
		webCodexExpectCredential(mock, "agent", true)
		response := webCodexSend(router, http.MethodPost, "/api/shell/agent/register", webCodexFixtureToken, string(body))
		require.Equal(t, want, response.Code, response.Body.String())
	}
	register(http.StatusOK)
	u := webCodexJobUpdateFixture()
	u.UpdateSeq = webCodexJobPtr(uint64(2))
	u.LogSnapshot = &protocol.ShellJobLogSnapshot{Stdout: protocol.ShellJobStreamSnapshot{Tail: "mounted line\n", FirstRetainedLine: 7, NextLine: 8, Truncated: true}, Stderr: stream}
	running := webCodexAcceptJobUpdate(t, router, mock, u)
	require.Equal(t, "running", running.Status)
	require.Equal(t, uint64(2), *running.LastUpdateSeq)
	// Cross-path registration cannot overwrite a sequence accepted by job_update.
	register(http.StatusOK)
	logs, err := runtime.registry.JobLog(runner.Access{Username: webCodexFixtureOwner}, protocol.RunnerJobLogRequest{JobID: queued.JobID})
	require.NoError(t, err)
	require.Equal(t, uint64(8), *logs.NextStdoutLine)
	before, err := runtime.registry.View(runner.Access{Username: webCodexFixtureOwner}, "node-a")
	require.NoError(t, err)
	inventory.Jobs = append(inventory.Jobs, snapshot)
	register(http.StatusBadRequest)
	after, err := runtime.registry.View(runner.Access{Username: webCodexFixtureOwner}, "node-a")
	require.NoError(t, err)
	require.Equal(t, before, after)
	inventory.Jobs = inventory.Jobs[:1]
	for _, test := range []struct {
		name   string
		mutate func(*protocol.RunnerJobUpdateRequest)
		scopes string
		status int
	}{
		{"scope", func(u *protocol.RunnerJobUpdateRequest) {}, "agent:register", http.StatusForbidden},
		{"request", func(u *protocol.RunnerJobUpdateRequest) { u.RequestID = webCodexJobPtr("wrong") }, webCodexJobScopes, http.StatusBadRequest},
		{"instance", func(u *protocol.RunnerJobUpdateRequest) { u.AgentInstanceID = "wrong" }, webCodexJobScopes, http.StatusBadRequest},
		{"client", func(u *protocol.RunnerJobUpdateRequest) { u.ClientID = "wrong" }, webCodexJobScopes, http.StatusForbidden},
		{"missing sequence", func(u *protocol.RunnerJobUpdateRequest) { u.UpdateSeq = nil }, webCodexJobScopes, http.StatusBadRequest},
		{"mixed snapshot", func(u *protocol.RunnerJobUpdateRequest) { u.StdoutTail = webCodexJobPtr("") }, webCodexJobScopes, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := u
			test.mutate(&body)
			webCodexExpectJobCredential(mock, "agent", test.scopes, "node-a", 42, true)
			response := webCodexSendJobUpdate(t, router, body)
			require.Equal(t, test.status, response.Code, response.Body.String())
			got, e := runtime.registry.GetJob(runner.Access{Username: webCodexFixtureOwner}, queued.JobID)
			require.NoError(t, e)
			require.Equal(t, running, got)
		})
	}
	for _, seq := range []string{"18446744073709551616", "-1", "1.5"} {
		body, e := json.Marshal(u)
		require.NoError(t, e)
		wire := strings.Replace(string(body), `"update_seq":2`, `"update_seq":`+seq, 1)
		webCodexExpectJobCredential(mock, "agent", webCodexJobScopes, "node-a", 42, true)
		response := webCodexSend(router, http.MethodPost, webCodexJobUpdatePath, webCodexJobFixtureToken, wire)
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	}
	u.Status = "completed"
	u.Finished = true
	u.UpdateSeq = webCodexJobPtr(^uint64(0))
	u.ExitCode = webCodexJobPtr(int32(0))
	u.CommandExecutionState = webCodexJobPtr(protocol.CommandCompleted)
	terminal := webCodexAcceptJobUpdate(t, router, mock, u)
	require.Equal(t, ^uint64(0), *terminal.LastUpdateSeq)
	register(http.StatusOK)
	got, err := runtime.registry.GetJob(runner.Access{Username: webCodexFixtureOwner}, queued.JobID)
	require.NoError(t, err)
	require.Equal(t, terminal, got)
	// Actual partial G2 capabilities stay refused even with a valid inventory.
	reg.Capabilities = protocol.RunnerCapabilities{JobStateReconciliation: true}
	register(http.StatusBadRequest)
}
