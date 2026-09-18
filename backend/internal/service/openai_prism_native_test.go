package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

func TestOpenAIPrismPrepareProjectBootstrapOrderAndAuthorization(t *testing.T) {
	base := &prismTestUpstream{}
	var paths []string
	transport := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.URL.Path)
		if req.Body != nil {
			data, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			req.Body = io.NopCloser(strings.NewReader(string(data)))
			switch req.URL.Path {
			case OpenAIPrismProjectPath:
				var project map[string]any
				require.NoError(t, json.Unmarshal(data, &project))
				require.Equal(t, "Workspace title", project["title"])
			case "/api/projects/project-created/sandbox/resources-token":
				require.JSONEq(t, `{"sandbox_session_id":null,"sandbox_token":"sandbox-secret"}`, string(data))
			case "/s/sandboxes/proxy/resources-token":
				require.JSONEq(t, `{"token":"resource-secret","resourceBaseUrl":"https://prism.openai.com/api/resources","projectId":"project-created"}`, string(data))
				require.Equal(t, "sandbox-secret", req.Header.Get("x-crixet-sandbox-token"))
			case "/api/y":
				require.JSONEq(t, `{"docId":"project-created"}`, string(data))
			case "/s/sandboxes/proxy/token":
				var document map[string]string
				require.NoError(t, json.Unmarshal(data, &document))
				require.Equal(t, "project-created", document["docId"])
				require.Equal(t, "document-secret", document["token"])
				require.Equal(t, "authorization-secret", document["authorization"])
				require.Equal(t, "sandbox-secret", req.Header.Get("x-crixet-sandbox-token"))
			}
		}
		if req.URL.Path == "/s/sandboxes/proxy/wait-for-sync" {
			require.Equal(t, "10000", req.URL.Query().Get("wait_ms"))
			require.Equal(t, "sandbox-secret", req.Header.Get("x-crixet-sandbox-token"))
		}
		return base.Do(req, "", 0, 1)
	}), OpenAIPrismTransportOptions{})
	state, err := transport.PrepareProject(context.Background(), &Account{ID: 1}, "access-secret", "", "Workspace title")
	require.NoError(t, err)
	require.Equal(t, []string{"/auth/session", OpenAIPrismProjectPath, OpenAIPrismProjectAccessPath, OpenAIPrismBackendNewPath, "/api/projects/project-created/sandbox/resources-token", "/s/sandboxes/proxy/resources-token", "/api/y", "/s/sandboxes/proxy/token", "/s/sandboxes/proxy/wait-for-sync"}, paths)
	require.Equal(t, "project-created", state.ProjectID)
	require.Equal(t, "sandbox-secret", state.SandboxToken)
	require.NotEmpty(t, state.ConversationID)
	require.NotEmpty(t, state.Cookies)
	require.Empty(t, state.ResponseID)
}

func TestOpenAIPrismBootstrapFailurePreventsTurn(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"/api/projects/project-created/sandbox/resources-token", `{"access_token":"resource-secret","resources_base_url":"https://other.example/resources"}`},
		{"/s/sandboxes/proxy/resources-token", `{"status":"error"}`},
		{"/api/y", `{"docId":"wrong-project","token":"document-secret","authorization":"authorization-secret"}`},
		{"/s/sandboxes/proxy/token", `{"success":false}`},
		{"/s/sandboxes/proxy/wait-for-sync", `{"status":"waiting"}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			base := &prismTestUpstream{}
			started := false
			transport := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == OpenAIPrismStartPath {
					started = true
				}
				if req.URL.Path == tc.path {
					return prismTestJSON(tc.body), nil
				}
				return base.Do(req, "", 0, 1)
			}), OpenAIPrismTransportOptions{})
			_, err := transport.Do(context.Background(), &Account{ID: 1}, "access-secret", OpenAIPrismConversationOptions{})
			require.Error(t, err)
			require.False(t, started)
		})
	}
}

func TestOpenAIPrismNativeProgressIsObservationAndPreservesDeltaFiles(t *testing.T) {
	base := &prismTestUpstream{}
	polls := 0
	transport := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == OpenAIPrismStatusPath {
			polls++
			if polls == 1 {
				return prismTestJSON(`{"status":"pending","codex_live_progress":{"transcriptCursor":12,"lineCount":3,"toolCalls":[{"name":"exec_command","call_id":"native-call","line_index":11,"status":null,"arguments_preview":"private arguments","source":"private source"}],"reasoningSummaries":["summary access-secret",{"raw":"private nested"}],"eventPreviews":[{"raw":"sandbox-secret private raw"}]}}`), nil
			}
			return prismTestJSON(`{"status":"completed","response":{"status":"success","payload":{"id":"response-native","output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}],"codexDeltaFiles":[{"file_path":"main.tex","status":"modified","diff":"safe diff"}]}}}`), nil
		}
		return base.Do(req, "", 0, 1)
	}), OpenAIPrismTransportOptions{PollInterval: time.Millisecond, PollLimit: 3})
	var observed []OpenAIPrismProgress
	resp, err := transport.Do(context.Background(), &Account{ID: 1}, "access-secret", OpenAIPrismConversationOptions{Request: &apicompat.ResponsesRequest{Stream: true}, OnProgress: func(progress OpenAIPrismProgress) { observed = append(observed, progress) }})
	require.NoError(t, err)
	require.Len(t, observed, 1)
	require.Equal(t, 12, observed[0].TranscriptCursor)
	require.Equal(t, "exec_command", observed[0].ToolCalls[0].Name)
	require.Nil(t, observed[0].ToolCalls[0].Status)
	encoded, err := json.Marshal(observed)
	require.NoError(t, err)
	for _, forbidden := range []string{"private", "access-secret", "sandbox-secret", "arguments_preview", "eventPreviews"} {
		require.NotContains(t, string(encoded), forbidden)
	}
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NotContains(t, string(data), "exec_command")
	require.NotContains(t, string(data), "function_call")
	require.NotContains(t, string(data), "safe diff")
	require.JSONEq(t, `[{"file_path":"main.tex","status":"modified","diff":"safe diff"}]`, string(transport.OpenAIPrismSessionState().DeltaFiles))
}

func TestOpenAIPrismRestoredCookiesRemainAccountIsolated(t *testing.T) {
	transport := NewOpenAIPrismTransportFromUpstream(nil, OpenAIPrismTransportOptions{})
	account := &Account{ID: 1}
	transport.RestoreCookies(account, "access-secret", []*http.Cookie{{Name: "prism_oai_access_token", Value: "refreshed-access", Domain: "other.example", Path: "/unsafe"}})
	cookies := transport.currentCookies(account, "access-secret")
	require.Len(t, cookies, 1)
	require.Equal(t, "refreshed-access", cookies[0].Value)
	transport.RestoreCookies(account, "access-secret", []*http.Cookie{{Name: "prism_oai_access_token", Value: "stale-access"}})
	require.Equal(t, "refreshed-access", transport.currentCookies(account, "access-secret")[0].Value)
	require.Equal(t, "access-secret", transport.currentCookies(&Account{ID: 2}, "access-secret")[0].Value)
	cookies[0].Value = "mutated-copy"
	require.Equal(t, "refreshed-access", transport.currentCookies(account, "access-secret")[0].Value)
}

func TestOpenAIPrismDeltaFilesWhitelistPreservesCompleteContent(t *testing.T) {
	content := strings.Repeat("long document line\n", 2000)
	raw := prismMustJSON([]map[string]any{{"file_path": "main.tex", "status": "modified", "diff": content + "sandbox-secret", "diff_truncated": false, "binary_body_b64": nil, "binary_bytes": 9007199254740993, "sandbox_token": "sandbox-secret", "debug": map[string]string{"authorization": "private-secret"}}})
	data := prismNormalizeDeltaFiles(raw, []string{"sandbox-secret"})
	require.NotContains(t, string(data), "sandbox_token")
	require.NotContains(t, string(data), "debug")
	require.NotContains(t, string(data), "private-secret")
	require.NotContains(t, string(data), "sandbox-secret")
	var result []struct {
		Diff        string `json:"diff"`
		BinaryBytes int64  `json:"binary_bytes"`
	}
	require.NoError(t, json.Unmarshal(data, &result))
	require.Equal(t, content+"[redacted]", result[0].Diff)
	require.Equal(t, int64(9007199254740993), result[0].BinaryBytes)
}

func TestOpenAIPrismNativeAuxiliaryProtocol(t *testing.T) {
	var paths []string
	transport := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.URL.Path)
		if req.URL.Path == "/auth/session" {
			return prismTestJSON(`{"user":{"app_metadata":{"user_id":"test-user"}}}`), nil
		}
		if strings.HasPrefix(req.URL.Path, "/s/sandboxes/proxy/") {
			require.Equal(t, "sandbox-secret", req.Header.Get("x-crixet-sandbox-token"))
		}
		switch req.URL.Path {
		case "/s/sandboxes/proxy/word-count":
			data, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.JSONEq(t, `{"mainDocument":"main.tex","includeBib":true}`, string(data))
		case "/api/projects/project-test/render-results/latest":
			require.Equal(t, "main.tex", req.URL.Query().Get("main_document"))
		case "/api/projects/project-test/version-history":
			require.Equal(t, "2", req.URL.Query().Get("page"))
			require.Equal(t, "25", req.URL.Query().Get("pageSize"))
		case OpenAIPrismConversationHistoryPath:
			data, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.JSONEq(t, `{"conversationId":"conversation-test","order":"desc","limit":50,"userId":"test-user","projectId":"project-test"}`, string(data))
		}
		return prismTestJSON(`{}`), nil
	}), OpenAIPrismTransportOptions{})
	ctx, account := context.Background(), &Account{ID: 1}
	_, _, err := transport.GetLogs(ctx, account, "access-secret", "sandbox-secret")
	require.NoError(t, err)
	_, _, err = transport.SyncTex(ctx, account, "access-secret", "sandbox-secret")
	require.NoError(t, err)
	_, _, err = transport.Heartbeat(ctx, account, "access-secret", "sandbox-secret")
	require.NoError(t, err)
	_, err = transport.WordCount(ctx, account, "access-secret", "sandbox-secret", "main.tex", true)
	require.NoError(t, err)
	_, _, err = transport.LatestRender(ctx, account, "access-secret", "project-test", "main.tex")
	require.NoError(t, err)
	_, err = transport.VersionHistory(ctx, account, "access-secret", "project-test", 2, 25)
	require.NoError(t, err)
	_, err = transport.ConversationHistory(ctx, account, "access-secret", "project-test", "conversation-test")
	require.NoError(t, err)
	require.Len(t, paths, 8)
}

func TestOpenAIPrismContinuationPreflightRefreshesOnlyExpiredSandbox(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusGone, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			base := &prismTestUpstream{}
			bootstraps, starts := 0, 0
			transport := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/s/sandboxes/proxy/heartbeat" {
					resp := prismTestJSON(`{"message":"sandbox unavailable"}`)
					resp.StatusCode = status
					return resp, nil
				}
				if req.URL.Path == OpenAIPrismBackendNewPath {
					bootstraps++
				}
				if req.URL.Path == OpenAIPrismStartPath {
					starts++
				}
				return base.Do(req, "", 0, 1)
			}), OpenAIPrismTransportOptions{PollInterval: time.Millisecond, PollLimit: 3})
			resp, err := transport.Do(context.Background(), &Account{ID: 1}, "access-secret", OpenAIPrismConversationOptions{ProjectID: "project-created", SandboxURL: "https://prism.openai.com/s/sandboxes/proxy", SandboxToken: "expired-sandbox"})
			if status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusGone {
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				require.Equal(t, 1, bootstraps)
				require.Equal(t, 1, starts)
			} else {
				require.Error(t, err)
				require.Zero(t, bootstraps)
				require.Zero(t, starts)
			}
		})
	}
}
