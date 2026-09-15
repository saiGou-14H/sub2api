//go:build unit

// SPDX-License-Identifier: Apache-2.0
// Mounted managed-credential tests for legacy WebCodex process Jobs, source
// revision 97ad66949a859174911c2f6da2ff1063be98bfa9. Full G2 is a test fixture
// only; this does not advertise DSH generation 3 or its deferred capabilities.
package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
	"github.com/Wei-Shaw/sub2api/internal/webcodex/runner"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const webCodexJobFixtureToken = "fabricated-mounted-job-update-token-only"
const webCodexJobUpdatePath = "/api/shell/agent/job_update"
const webCodexJobScopes = "agent:register agent:poll agent:result agent:job_update"

// This supplies SQL rows, not an Authenticate stub: webCodexSetup constructs
// ProvideWebCodexRunner's actual repository, host-user resolver and verifier.
func webCodexExpectJobCredential(mock sqlmock.Sqlmock, kind, scopes, client string, ownerFixture int64, touch bool) {
	webCodexExpectJobCredentialExpiry(mock, kind, scopes, client, ownerFixture, nil, touch)
}

func webCodexExpectJobCredentialExpiry(mock sqlmock.Sqlmock, kind, scopes, client string, ownerFixture int64, expires *int64, touch bool) {
	sum := sha256.Sum256([]byte(webCodexJobFixtureToken))
	hash := hex.EncodeToString(sum[:])
	rows := sqlmock.NewRows([]string{"id", "user_id", "name", "key_hash", "key_prefix", "created_at", "last_used_at", "revoked_at", "scopes", "expires_at", "kind", "allowed_client_id"}).
		AddRow("fixture-job-key", ownerFixture, "fabricated job fixture", hash, "fabricated", int64(1), nil, nil, scopes, expires, kind, client)
	mock.ExpectQuery(`SELECT .* FROM wc_api_keys WHERE key_hash = \$1 AND revoked_at IS NULL`).WithArgs(hash).WillReturnRows(rows)
	if touch {
		mock.ExpectExec(`UPDATE wc_api_keys SET last_used_at = \$2 WHERE id = \$1`).WithArgs("fixture-job-key", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	}
}

func webCodexJobPtr[T any](value T) *T { return &value }

func webCodexStartMountedJob(t *testing.T, runtime *WebCodexRunner, router *gin.Engine, mock sqlmock.Sqlmock) protocol.ShellJobInfo {
	t.Helper()
	webCodexExpectCredential(mock, "agent", true)
	registered := webCodexSend(router, http.MethodPost, "/api/shell/agent/register", webCodexFixtureToken, webCodexRegistration)
	require.Equal(t, http.StatusOK, registered.Code, registered.Body.String())
	operation := protocol.JobProcessOperation{
		JobID: "mounted-job-a", Process: protocol.ShellProcessArgv{Executable: "printf", Args: []string{"fixture two words"}}, TimeoutSecs: 30,
		Context: protocol.ShellJobContext{CommandPreview: "untrusted fixture preview"},
	}
	metadata := operation.ExpectedStructuredExecution()
	operation.Context.StructuredExecution = &metadata
	job, err := runtime.registry.StartProcessJob(runner.Access{Username: webCodexFixtureOwner}, protocol.JobInvocation{
		Metadata: protocol.InvocationMetadata{RequestID: "mounted-request-a", ClientID: "node-a", RequestedBy: webCodexFixtureOwner, CreatedAt: 1}, Operation: operation,
	})
	require.NoError(t, err)
	require.Equal(t, "queued", job.Status)
	require.Equal(t, `printf "fixture two words"`, job.CommandPreview)
	return job
}

func webCodexPollMountedJob(t *testing.T, router *gin.Engine, mock sqlmock.Sqlmock) {
	t.Helper()
	webCodexExpectCredential(mock, "agent", true)
	polled := webCodexSend(router, http.MethodPost, "/api/shell/agent/poll", webCodexFixtureToken, `{"client_id":"node-a","agent_instance_id":"process-a"}`)
	require.Equal(t, http.StatusOK, polled.Code, polled.Body.String())
	var response protocol.RunnerPollResponse
	require.NoError(t, json.Unmarshal(polled.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.NotNil(t, response.Request)
	require.Equal(t, "start_process_job", response.Request.Kind)
	require.Equal(t, "mounted-request-a", response.Request.RequestID)
	require.Equal(t, "mounted-job-a", *response.Request.JobID)
}

func webCodexJobUpdateFixture() protocol.RunnerJobUpdateRequest {
	return protocol.RunnerJobUpdateRequest{ClientID: "node-a", AgentInstanceID: "process-a", JobID: "mounted-job-a", RequestID: webCodexJobPtr("mounted-request-a"), Status: "running"}
}

func webCodexSendJobUpdate(t *testing.T, router *gin.Engine, update protocol.RunnerJobUpdateRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(update)
	require.NoError(t, err)
	return webCodexSend(router, http.MethodPost, webCodexJobUpdatePath, webCodexJobFixtureToken, string(body))
}

func webCodexAcceptJobUpdate(t *testing.T, router *gin.Engine, mock sqlmock.Sqlmock, update protocol.RunnerJobUpdateRequest) protocol.ShellJobInfo {
	t.Helper()
	webCodexExpectJobCredential(mock, "agent", webCodexJobScopes, "node-a", 42, true)
	rec := webCodexSendJobUpdate(t, router, update)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var response protocol.RunnerJobUpdateResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.NotNil(t, response.Job)
	return *response.Job
}

func TestWebCodexRunnerMountedJobLifecycle(t *testing.T) {
	runtime, router, mock, _ := webCodexSetup(t)
	queued := webCodexStartMountedJob(t, runtime, router, mock)
	access := runner.Access{Username: webCodexFixtureOwner}
	webCodexPollMountedJob(t, router, mock)
	dispatched, err := runtime.registry.GetJob(access, queued.JobID)
	require.NoError(t, err)
	require.Equal(t, "agent_queued", dispatched.Status)
	update := webCodexJobUpdateFixture()
	update.StdoutChunk = webCodexJobPtr("first\n")
	update.StderrChunk = webCodexJobPtr("fixture warning\n")
	update.UpdateSeq = webCodexJobPtr(uint64(1))
	update.Activity = &protocol.ShellJobActivity{State: protocol.ActivityWorking, Phase: protocol.ActivityProcessRunning, Source: protocol.ActivityRunnerExecution}
	running := webCodexAcceptJobUpdate(t, router, mock, update)
	require.Equal(t, "running", running.Status)
	require.NotNil(t, running.StartedAt)
	require.Equal(t, update.Activity, running.Activity)
	require.Nil(t, running.Result)
	update = webCodexJobUpdateFixture()
	update.Status, update.Finished = "completed", true
	update.StdoutChunk = webCodexJobPtr("second\n")
	update.ExitCode, update.DurationMS = webCodexJobPtr(int32(0)), webCodexJobPtr(uint64(1250))
	update.CommandExecutionState = webCodexJobPtr(protocol.CommandCompleted)
	update.UpdateSeq = webCodexJobPtr(uint64(2))
	terminal := webCodexAcceptJobUpdate(t, router, mock, update)
	require.Equal(t, "completed", terminal.Status)
	require.Nil(t, terminal.Activity)
	require.NotNil(t, terminal.EndedAt)
	require.NotNil(t, terminal.Result)
	require.NotNil(t, terminal.Result.Shell)
	require.Equal(t, int32(0), *terminal.Result.Shell.ExitCode)
	require.Equal(t, uint64(1250), *terminal.DurationMS)
	require.Equal(t, uint64(2), *terminal.LastUpdateSeq)
	got, err := runtime.registry.GetJob(access, queued.JobID)
	require.NoError(t, err)
	require.Equal(t, terminal, got)
	logs, err := runtime.registry.JobLog(access, protocol.RunnerJobLogRequest{JobID: queued.JobID})
	require.NoError(t, err)
	require.True(t, logs.Success)
	require.Equal(t, "first\nsecond\n", *logs.StdoutTail)
	require.Equal(t, "fixture warning\n", *logs.StderrTail)
	require.Equal(t, uint64(3), *logs.NextStdoutLine)
	require.Equal(t, uint64(2), *logs.NextStderrLine)
	cursor, err := runtime.registry.JobLog(access, protocol.RunnerJobLogRequest{JobID: queued.JobID, SinceStdoutLine: webCodexJobPtr(uint64(2))})
	require.NoError(t, err)
	require.Equal(t, "second\n", *cursor.StdoutTail)
	jobs, err := runtime.registry.ListJobs(access, webCodexJobPtr("node-a"), webCodexJobPtr("completed"), webCodexJobPtr(uint64(1)))
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, queued.JobID, jobs[0].JobID)
	// A late active update cannot reopen the terminal latch or append output.
	late := webCodexJobUpdateFixture()
	late.StdoutChunk = webCodexJobPtr("late fabricated output")
	require.Equal(t, terminal, webCodexAcceptJobUpdate(t, router, mock, late))
}

func TestWebCodexRunnerMountedJobLifecycleMismatch(t *testing.T) {
	for _, test := range []struct {
		name, status string
		started      bool
		state        *protocol.ShellCommandExecutionState
		wantState    protocol.ShellCommandExecutionState
		violation    string
	}{
		{"not started after running", "failed", true, webCodexJobPtr(protocol.CommandNotStarted), protocol.CommandOutcomeUnknown, "structured_job_lifecycle_invalid"},
		{"completed but not started", "completed", false, webCodexJobPtr(protocol.CommandNotStarted), protocol.CommandNotStarted, "structured_job_lifecycle_invalid"},
		{"missing terminal execution state", "completed", false, nil, protocol.CommandNotStarted, "structured_job_lifecycle_missing"},
		{"execution state on active job", "running", false, webCodexJobPtr(protocol.CommandCompleted), protocol.CommandNotStarted, "command_execution_state_on_active_job"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, router, mock, _ := webCodexSetup(t)
			job := webCodexStartMountedJob(t, runtime, router, mock)
			webCodexPollMountedJob(t, router, mock)
			if test.started {
				webCodexAcceptJobUpdate(t, router, mock, webCodexJobUpdateFixture())
			}
			update := webCodexJobUpdateFixture()
			update.Status, update.CommandExecutionState = test.status, test.state
			update.StdoutChunk = webCodexJobPtr("rejected fixture output")
			failed := webCodexAcceptJobUpdate(t, router, mock, update)
			require.Equal(t, "failed", failed.Status)
			require.Contains(t, *failed.Error, test.violation)
			require.Equal(t, test.wantState, *failed.CommandExecutionState)
			logs, err := runtime.registry.JobLog(runner.Access{Username: webCodexFixtureOwner}, protocol.RunnerJobLogRequest{JobID: job.JobID})
			require.NoError(t, err)
			require.Empty(t, *logs.StdoutTail)
		})
	}
}

func TestWebCodexRunnerMountedJobUpdateRejections(t *testing.T) {
	for _, test := range []struct {
		name   string
		modify func(*protocol.RunnerJobUpdateRequest)
	}{
		{"stale instance", func(u *protocol.RunnerJobUpdateRequest) { u.AgentInstanceID = "stale-process" }},
		{"wrong request", func(u *protocol.RunnerJobUpdateRequest) { u.RequestID = webCodexJobPtr("wrong-request") }},
		{"unknown job", func(u *protocol.RunnerJobUpdateRequest) { u.JobID = "unknown-job" }},
		{"snapshot unsupported", func(u *protocol.RunnerJobUpdateRequest) {
			u.LogSnapshot = &protocol.ShellJobLogSnapshot{Stdout: protocol.ShellJobStreamSnapshot{FirstRetainedLine: 1, NextLine: 1}, Stderr: protocol.ShellJobStreamSnapshot{FirstRetainedLine: 1, NextLine: 1}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, router, mock, _ := webCodexSetup(t)
			job := webCodexStartMountedJob(t, runtime, router, mock)
			webCodexPollMountedJob(t, router, mock)
			before, err := runtime.registry.GetJob(runner.Access{Username: webCodexFixtureOwner}, job.JobID)
			require.NoError(t, err)
			update := webCodexJobUpdateFixture()
			test.modify(&update)
			webCodexExpectJobCredential(mock, "agent", webCodexJobScopes, "node-a", 42, true)
			require.Equal(t, http.StatusBadRequest, webCodexSendJobUpdate(t, router, update).Code)
			after, err := runtime.registry.GetJob(runner.Access{Username: webCodexFixtureOwner}, job.JobID)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestWebCodexRunnerMountedJobCredentialRejections(t *testing.T) {
	for _, test := range []struct {
		name, kind, scopes, client string
		owner                      int64
		disabled                   bool
		want                       int
	}{
		{"missing job scope", "agent", "agent:register agent:poll agent:result", "node-a", 42, false, http.StatusForbidden},
		{"user kind", "user", webCodexJobScopes, "node-a", 42, false, http.StatusForbidden},
		{"allowed client mismatch", "agent", webCodexJobScopes, "node-b", 42, false, http.StatusForbidden},
		{"disabled owner", "agent", webCodexJobScopes, "node-a", 42, true, http.StatusUnauthorized},
		{"missing owner", "agent", webCodexJobScopes, "node-a", 99, false, http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, router, mock, users := webCodexSetup(t)
			webCodexStartMountedJob(t, runtime, router, mock)
			webCodexPollMountedJob(t, router, mock)
			if test.disabled {
				users.status = "disabled"
			}
			webCodexExpectJobCredential(mock, test.kind, test.scopes, test.client, test.owner, !test.disabled && test.owner == 42)
			require.Equal(t, test.want, webCodexSendJobUpdate(t, router, webCodexJobUpdateFixture()).Code)
		})
	}
}

func TestWebCodexRunnerMountedJobRegisteredOwnerMismatch(t *testing.T) {
	runtime, router, mock, _ := webCodexSetup(t)
	registration, err := protocol.ReadRegisterRequest([]byte(webCodexRegistration))
	require.NoError(t, err)
	// Seed another managed owner's registration through the trusted Registry API;
	// the attempted HTTP mutation still uses the real managed verifier for user 42.
	_, err = runtime.registry.Register(runner.Principal{Kind: runner.AgentToken, Username: "sub2api_user_99", AllowedClientID: "node-a", Scopes: []string{runner.ScopeRegister}}, registration)
	require.NoError(t, err)
	webCodexExpectJobCredential(mock, "agent", webCodexJobScopes, "node-a", 42, true)
	require.Equal(t, http.StatusForbidden, webCodexSendJobUpdate(t, router, webCodexJobUpdateFixture()).Code)
}

func TestWebCodexRunnerMountedJobRevokedAndExpiredCredentials(t *testing.T) {
	t.Run("revoked lookup is absent", func(t *testing.T) {
		_, router, mock, _ := webCodexSetup(t)
		sum := sha256.Sum256([]byte(webCodexJobFixtureToken))
		mock.ExpectQuery(`SELECT .* FROM wc_api_keys WHERE key_hash = \$1 AND revoked_at IS NULL`).WithArgs(hex.EncodeToString(sum[:])).WillReturnRows(sqlmock.NewRows([]string{"id"}))
		require.Equal(t, http.StatusUnauthorized, webCodexSendJobUpdate(t, router, webCodexJobUpdateFixture()).Code)
	})
	t.Run("expired row", func(t *testing.T) {
		_, router, mock, _ := webCodexSetup(t)
		webCodexExpectJobCredentialExpiry(mock, "agent", webCodexJobScopes, "node-a", 42, webCodexJobPtr(int64(1)), false)
		require.Equal(t, http.StatusUnauthorized, webCodexSendJobUpdate(t, router, webCodexJobUpdateFixture()).Code)
	})
}

func TestWebCodexRunnerMountedJobIngress(t *testing.T) {
	_, router, mock, _ := webCodexSetup(t)
	require.Equal(t, http.StatusUnauthorized, webCodexSend(router, http.MethodPost, webCodexJobUpdatePath, "", "{}").Code)
	get := webCodexSend(router, http.MethodGet, webCodexJobUpdatePath, "", "")
	require.Equal(t, http.StatusMethodNotAllowed, get.Code)
	require.Equal(t, "POST", get.Header().Get("Allow"))
	request := httptest.NewRequest(http.MethodPost, webCodexJobUpdatePath, strings.NewReader("{}"))
	request.Header.Add("Authorization", "Bearer "+webCodexJobFixtureToken)
	request.Header.Add("Authorization", "Bearer "+webCodexJobFixtureToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, request)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	webCodexExpectJobCredential(mock, "agent", webCodexJobScopes, "node-a", 42, true)
	require.Equal(t, http.StatusRequestEntityTooLarge, webCodexSend(router, http.MethodPost, webCodexJobUpdatePath, webCodexJobFixtureToken, strings.Repeat("x", 8193)).Code)
}

func TestWebCodexRunnerDisabledJobUpdateMount(t *testing.T) {
	runtime, err := ProvideWebCodexRunner(&config.Config{}, nil, nil)
	require.NoError(t, err)
	t.Cleanup(runtime.Close)
	router := gin.New()
	runtime.mount(router)
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		require.Equal(t, http.StatusNotFound, webCodexSend(router, method, webCodexJobUpdatePath, "", "{}").Code)
	}
}
