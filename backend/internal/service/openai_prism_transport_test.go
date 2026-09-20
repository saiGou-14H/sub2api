package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type prismTestUpstream struct {
	requests []*http.Request
	polls    int
}

func (u *prismTestUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.requests = append(u.requests, req)
	if resp, handled, err := prismTestBootstrapResponse(req); handled {
		return resp, err
	}
	path := req.URL.Path
	var body string
	switch path {
	case "/api/backend/1/new":
		body = `{"url":"https://prism.openai.com/s/sandboxes/proxy","token":"sandbox-secret"}`
	case "/api/projects":
		body = `{"uuid":"project-created","title":"sub2api"}`
	case OpenAIPrismProjectAccessPath:
		body = `{"accessible":true,"project":{"uuid":"project-created"},"userRole":"owner"}`
	case "/auth/session":
		body = `{"user":{"app_metadata":{"user_id":"user-1"}},"policy":{"user":{"openai_user_id":"oai-1"}}}`
	case OpenAIPrismConversationHistoryPath:
		body = `{"conversationId":"cdx1_test","items":[],"hasMore":false,"backendConversationFound":false,"waitingForSandbox":false}`
	case OpenAIPrismStartPath:
		body = `{"status":"started","request_id":"req-1","conversation_id":"cdx1_test","turn_state":{"version":1,"conversation_id":"cdx1_test","transcript_cursor":0}}`
	case OpenAIPrismStatusPath:
		u.polls++
		if u.polls == 1 {
			body = `{"status":"pending","request_id":"req-1","turn_state":{"version":1,"conversation_id":"cdx1_test","transcript_cursor":1}}`
		} else {
			body = `{"status":"completed","request_id":"req-1","response":{"status":"success","payload":{"id":"resp-1","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello"}]}]}}}`
		}
	default:
		return nil, io.ErrUnexpectedEOF
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func (u *prismTestUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestOpenAIPrismTransportDynamicBootstrapAndStatus(t *testing.T) {
	u := &prismTestUpstream{}
	account := &Account{ID: 7, Credentials: map[string]any{"prism_session_token": "session-secret"}}
	transport := NewOpenAIPrismTransportFromUpstream(u, OpenAIPrismTransportOptions{BaseURL: "https://prism.openai.com", PollInterval: time.Millisecond, PollLimit: 4})
	req := &apicompat.ResponsesRequest{Model: "gpt-6-astra", Instructions: "system", Stream: true, Input: []byte(`[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]`)}
	resp, err := transport.Do(context.Background(), account, "access-secret", OpenAIPrismConversationOptions{Request: req})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	data, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(data), "response.completed")
	require.Contains(t, string(data), "hello")
	require.GreaterOrEqual(t, len(u.requests), 7)
	for _, r := range u.requests {
		cookie := r.Header.Get("Cookie")
		require.Contains(t, cookie, "prism_oai_access_token=access-secret")
		require.Contains(t, cookie, "prism_session_token=session-secret")
	}
}

func TestOpenAIPrismTransportNonStreamingResponse(t *testing.T) {
	u := &prismTestUpstream{}
	account := &Account{ID: 8, Credentials: map[string]any{"prism_session_token": "session-secret"}}
	transport := NewOpenAIPrismTransportFromUpstream(u, OpenAIPrismTransportOptions{BaseURL: "https://prism.openai.com", PollInterval: time.Millisecond, PollLimit: 4})
	req := &apicompat.ResponsesRequest{Model: "gpt-6-astra", Input: []byte(`"hi"`), Stream: false}
	resp, err := transport.Do(context.Background(), account, "access-secret", OpenAIPrismConversationOptions{Request: req})
	require.NoError(t, err)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	data, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	var value map[string]any
	require.NoError(t, json.Unmarshal(data, &value))
	require.Equal(t, "resp-1", value["id"])
	require.Equal(t, "completed", value["status"])
}

func TestOpenAIPrismTransportCancellation(t *testing.T) {
	u := &prismTestUpstream{}
	transport := NewOpenAIPrismTransportFromUpstream(u, OpenAIPrismTransportOptions{PollInterval: time.Second, PollLimit: 4})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := transport.Do(ctx, &Account{Credentials: map[string]any{"prism_session_token": "s"}}, "a", OpenAIPrismConversationOptions{Request: &apicompat.ResponsesRequest{Model: "gpt-6-astra"}})
	require.ErrorIs(t, err, context.Canceled)
	var startedErr *OpenAIPrismStartedError
	require.False(t, errors.As(err, &startedErr))
}

type prismUpstreamFunc func(*http.Request) (*http.Response, error)

func prismTestBootstrapResponse(req *http.Request) (*http.Response, bool, error) {
	origin := req.URL.Scheme + "://" + req.URL.Host
	switch {
	case strings.HasSuffix(req.URL.Path, "/sandbox/resources-token"):
		return prismTestJSON(string(prismMustJSON(map[string]any{"access_token": "resource-secret", "resources_base_url": origin + "/api/resources", "expires_at": 1900000000, "max_age_seconds": 3600, "scopes": []string{"project_files:read", "render_results:write"}}))), true, nil
	case req.URL.Path == "/s/sandboxes/proxy/resources-token":
		return prismTestJSON(`{"status":"success"}`), true, nil
	case req.URL.Path == "/api/y":
		var body map[string]string
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, true, err
		}
		ws := "wss://" + req.URL.Host
		if req.URL.Scheme == "http" {
			ws = "ws://" + req.URL.Host
		}
		return prismTestJSON(string(prismMustJSON(map[string]string{"url": ws + "/y/d/test/ws", "baseUrl": origin + "/y/d/test", "docId": body["docId"], "token": "document-secret", "authorization": "authorization-secret"}))), true, nil
	case req.URL.Path == "/s/sandboxes/proxy/token":
		return prismTestJSON(`{"success":true}`), true, nil
	case req.URL.Path == "/s/sandboxes/proxy/wait-for-sync":
		return prismTestJSON(`{"status":"synced","readinessCapabilities":["current_y_sweet_provider"],"tokens":{"hasCurrentYSweetToken":true}}`), true, nil
	case req.URL.Path == "/s/sandboxes/proxy/heartbeat":
		return prismTestJSON(`{"status":"ok"}`), true, nil
	}
	return nil, false, nil
}

func (f prismUpstreamFunc) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return f(req)
}

func (f prismUpstreamFunc) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return f(req)
}

func prismTestJSON(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestOpenAIPrismTransportContinuationRetainsSandboxAndOpaqueState(t *testing.T) {
	const opaque = `{"version":1,"cursor":0,"unknown":null,"large":9007199254740993123,"nested":{"sandbox_token":"opaque-secret"}}`
	const snapshot = `{"cursor":0,"unknown":null,"large":9007199254740993123,"sandbox_token":"snapshot-secret"}`
	base := &prismTestUpstream{}
	starts, polls, bootstraps, projects := 0, 0, 0, 0
	upstream := prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case OpenAIPrismProjectPath:
			projects++
		case OpenAIPrismBackendNewPath:
			bootstraps++
		case OpenAIPrismStartPath:
			starts++
			polls = 0
			var start struct {
				PreviousResponseID string                     `json:"previousResponseId"`
				ConversationID     string                     `json:"conversationId"`
				Metadata           map[string]json.RawMessage `json:"metadata"`
			}
			require.NoError(t, json.NewDecoder(req.Body).Decode(&start))
			var sandboxToken string
			require.NoError(t, json.Unmarshal(start.Metadata["sandbox_token"], &sandboxToken))
			require.Equal(t, "sandbox-secret", sandboxToken)
			if starts == 2 {
				require.Equal(t, "response-1", start.PreviousResponseID)
				require.Equal(t, "cdx1_continuation", start.ConversationID)
				var metadataSnapshot string
				require.NoError(t, json.Unmarshal(start.Metadata["codex_listen_snapshot"], &metadataSnapshot))
				require.JSONEq(t, snapshot, metadataSnapshot)
			}
			return prismTestJSON(`{"status":"started","request_id":"request-current","conversation_id":"cdx1_continuation","turn_state":` + opaque + `}`), nil
		case OpenAIPrismStatusPath:
			polls++
			var poll struct {
				TurnState json.RawMessage `json:"turn_state"`
			}
			require.NoError(t, json.NewDecoder(req.Body).Decode(&poll))
			if polls == 1 {
				require.JSONEq(t, opaque, string(poll.TurnState))
				require.Contains(t, string(poll.TurnState), "9007199254740993123")
				return prismTestJSON(`{"status":"pending","turn_state":null,"response":{"status":"success","payload":{"id":"stale-pending","output":[]}}}`), nil
			}
			require.JSONEq(t, opaque, string(poll.TurnState))
			return prismTestJSON(fmt.Sprintf(`{"status":"completed","response":{"status":"success","payload":{"id":"response-%d","output":[],"codexListenSnapshot":%s}}}`, starts, snapshot)), nil
		}
		return base.Do(req, "", 0, 1)
	})
	transport := NewOpenAIPrismTransportFromUpstream(upstream, OpenAIPrismTransportOptions{PollInterval: time.Millisecond, PollLimit: 3})
	account := &Account{ID: 1, Credentials: map[string]any{"prism_session_token": "session-secret"}}
	resp, err := transport.Do(context.Background(), account, "access-secret", OpenAIPrismConversationOptions{Request: &apicompat.ResponsesRequest{Model: "gpt-6-astra"}})
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	state := transport.OpenAIPrismSessionState()
	require.Equal(t, "response-1", state.ResponseID)
	require.Equal(t, "https://prism.openai.com/s/sandboxes/proxy", state.SandboxURL)
	require.Equal(t, "sandbox-secret", state.SandboxToken)
	require.JSONEq(t, snapshot, string(state.CodexListenSnapshot))
	resp, err = transport.Do(context.Background(), account, "access-secret", OpenAIPrismConversationOptions{
		Request: &apicompat.ResponsesRequest{Model: "gpt-6-astra"}, ProjectID: state.ProjectID, ConversationID: state.ConversationID,
		PreviousResponseID: state.ResponseID, SandboxURL: state.SandboxURL, SandboxToken: state.SandboxToken, CodexListenSnapshot: state.CodexListenSnapshot,
	})
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, 1, bootstraps)
	require.Equal(t, 1, projects)
	require.Equal(t, 2, starts)
	require.Equal(t, "response-2", transport.OpenAIPrismSessionState().ResponseID)
	state.CodexListenSnapshot[0] = '['
	require.JSONEq(t, snapshot, string(transport.OpenAIPrismSessionState().CodexListenSnapshot))
}

func TestOpenAIPrismTransportCookieRefreshAndAccountIsolation(t *testing.T) {
	var received []map[string]string
	upstream := prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
		cookies := make(map[string]string)
		for _, cookie := range req.Cookies() {
			_, duplicate := cookies[cookie.Name]
			require.False(t, duplicate, "duplicate cookie %s", cookie.Name)
			cookies[cookie.Name] = cookie.Value
		}
		received = append(received, cookies)
		resp := prismTestJSON(`{}`)
		if len(received) == 1 {
			resp.Header.Add("Set-Cookie", "prism_session_token=refreshed-session; Path=/; Secure; HttpOnly")
			resp.Header.Add("Set-Cookie", "__cf_bm=account-a-only; Path=/; Secure; HttpOnly")
		}
		return resp, nil
	})
	transport := NewOpenAIPrismTransportFromUpstream(upstream, OpenAIPrismTransportOptions{})
	accountA := &Account{ID: 11, Credentials: map[string]any{"prism_session_token": "explicit-session", "prism_cookie": "prism_oai_access_token=old-access; prism_session_token=old-session; extra=imported"}}
	accountB := &Account{ID: 12, Credentials: map[string]any{"prism_session_token": "explicit-session"}}
	for _, input := range []struct {
		account *Account
		token   string
	}{{accountA, "explicit-access"}, {accountA, "explicit-access"}, {accountB, "explicit-access"}, {accountA, "replacement-access"}} {
		_, _, err := transport.doJSON(context.Background(), input.account, input.token, http.MethodGet, "/auth/session", nil)
		require.NoError(t, err)
	}
	require.Equal(t, "explicit-access", received[0]["prism_oai_access_token"])
	require.Equal(t, "explicit-session", received[0]["prism_session_token"])
	require.Equal(t, "imported", received[0]["extra"])
	require.Equal(t, "refreshed-session", received[1]["prism_session_token"])
	require.Equal(t, "account-a-only", received[1]["__cf_bm"])
	require.NotContains(t, received[2], "__cf_bm")
	require.NotContains(t, received[2], "extra")
	require.NotContains(t, received[3], "__cf_bm")
	require.Equal(t, "explicit-session", received[3]["prism_session_token"])
	require.Equal(t, "replacement-access", received[3]["prism_oai_access_token"])
}

func TestOpenAIPrismTransportErrorsRedactSecrets(t *testing.T) {
	account := &Account{ID: 1, Credentials: map[string]any{"access_token": "access-secret", "prism_session_token": "session-secret"}}
	upstream := prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{
			"Retry-After": []string{"5"}, "Set-Cookie": []string{"__cf_bm=response-secret; Path=/"}, "X-Sandbox-Token": []string{"sandbox-secret"},
			"X-Detail": []string{"session-secret"},
		}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"access-secret session-secret sandbox-secret snapshot-secret response-secret"}}`))}, nil
	})
	transport := NewOpenAIPrismTransportFromUpstream(upstream, OpenAIPrismTransportOptions{})
	_, status, err := transport.doJSON(context.Background(), account, "access-secret", http.MethodPost, "/api/failure?token=session-secret", map[string]any{"metadata": map[string]any{"sandbox_token": "sandbox-secret", "codex_listen_snapshot": `{"sandbox_token":"snapshot-secret"}`}})
	require.Equal(t, http.StatusTooManyRequests, status)
	var upstreamErr *OpenAIPrismHTTPError
	require.ErrorAs(t, err, &upstreamErr)
	require.Equal(t, "/api/failure", upstreamErr.Path)
	require.Equal(t, "5", upstreamErr.Headers.Get("Retry-After"))
	require.Empty(t, upstreamErr.Headers.Get("Set-Cookie"))
	require.Empty(t, upstreamErr.Headers.Get("X-Sandbox-Token"))
	for _, secret := range []string{"access-secret", "session-secret", "sandbox-secret", "snapshot-secret", "response-secret"} {
		require.NotContains(t, err.Error(), secret)
		require.NotContains(t, fmt.Sprint(upstreamErr.Headers), secret)
	}
	transport.upstream = prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("access-secret session-secret: %w", context.Canceled)
	})
	_, _, err = transport.doJSON(context.Background(), account, "access-secret", http.MethodGet, "/auth/session", nil)
	require.ErrorIs(t, err, context.Canceled)
	require.NotContains(t, err.Error(), "access-secret")
	require.NotContains(t, err.Error(), "session-secret")
}

func TestOpenAIPrismTransportPrerequisiteFailuresPreventStart(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		status           int
	}{
		{"auth rejected", "/auth/session", `{"error":{"message":"unauthorized"}}`, http.StatusUnauthorized},
		{"auth missing user", "/auth/session", `{}`, http.StatusOK},
		{"project inaccessible", OpenAIPrismProjectAccessPath, `{"accessible":false}`, http.StatusOK},
		{"unsafe sandbox", OpenAIPrismBackendNewPath, `{"url":"https://other.example/s/sandboxes/proxy","token":"sandbox-secret"}`, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := &prismTestUpstream{}
			started := false
			transport := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == OpenAIPrismStartPath {
					started = true
				}
				if req.URL.Path == tc.path {
					resp := prismTestJSON(tc.body)
					resp.StatusCode = tc.status
					return resp, nil
				}
				return base.Do(req, "", 0, 1)
			}), OpenAIPrismTransportOptions{PollInterval: time.Millisecond, PollLimit: 2})
			_, err := transport.Do(context.Background(), &Account{ID: 1}, "access-secret", OpenAIPrismConversationOptions{})
			require.Error(t, err)
			var startedErr *OpenAIPrismStartedError
			require.False(t, errors.As(err, &startedErr))
			require.False(t, started)
		})
	}
}

func TestOpenAIPrismTransportTerminalFailuresAndPollLimit(t *testing.T) {
	for _, tc := range []struct{ name, status, want string }{
		{"failed", `{"status":"failed"}`, "turn failed"},
		{"cancelled", `{"status":"cancelled"}`, "turn failed"},
		{"nested failure", `{"status":"completed","response":{"status":"error","payload":{}}}`, "turn failed"},
		{"invalid payload", `{"status":"completed","response":{"status":"success","payload":[]}}`, "not a JSON object"},
		{"empty payload object", `{"status":"completed","response":{"status":"success","payload":{}}}`, "missing output array"},
		{"null output", `{"status":"completed","response":{"status":"success","payload":{"output":null}}}`, "missing output array"},
		{"pending limit", `{"status":"pending","response":{"status":"success","payload":{"id":"not-finished"}}}`, "poll limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := &prismTestUpstream{}
			transport := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == OpenAIPrismStatusPath {
					return prismTestJSON(tc.status), nil
				}
				return base.Do(req, "", 0, 1)
			}), OpenAIPrismTransportOptions{PollInterval: time.Millisecond, PollLimit: 2})
			_, err := transport.Do(context.Background(), &Account{ID: 1}, "access-secret", OpenAIPrismConversationOptions{})
			require.ErrorContains(t, err, tc.want)
			var startedErr *OpenAIPrismStartedError
			require.ErrorAs(t, err, &startedErr)
			require.Equal(t, "req-1", startedErr.RequestID)
			require.Empty(t, transport.OpenAIPrismSessionState().ResponseID)
		})
	}
}

func TestOpenAIPrismTransportCancellationWhilePolling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	base := &prismTestUpstream{}
	polls := 0
	transport := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == OpenAIPrismStatusPath {
			polls++
			cancel()
			return prismTestJSON(`{"status":"pending"}`), nil
		}
		return base.Do(req, "", 0, 1)
	}), OpenAIPrismTransportOptions{PollInterval: time.Hour, PollLimit: 10})
	_, err := transport.Do(ctx, &Account{ID: 1}, "access-secret", OpenAIPrismConversationOptions{})
	require.ErrorIs(t, err, context.Canceled)
	var startedErr *OpenAIPrismStartedError
	require.ErrorAs(t, err, &startedErr)
	require.Equal(t, 1, polls)
}

func TestOpenAIPrismTransportPollFailureRedactsSandbox(t *testing.T) {
	base := &prismTestUpstream{}
	transport := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case OpenAIPrismStartPath:
			return prismTestJSON(`{"status":"started","request_id":"request-test","turn_state":{"opaque":"synthetic-state"}}`), nil
		case OpenAIPrismStatusPath:
			resp := prismTestJSON(`{"error":{"message":"sandbox-secret expired"}}`)
			resp.StatusCode = http.StatusUnauthorized
			return resp, nil
		}
		return base.Do(req, "", 0, 1)
	}), OpenAIPrismTransportOptions{PollInterval: time.Millisecond, PollLimit: 2})
	_, err := transport.Do(context.Background(), &Account{ID: 1}, "access-secret", OpenAIPrismConversationOptions{})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "sandbox-secret")
	var upstreamErr *OpenAIPrismHTTPError
	require.ErrorAs(t, err, &upstreamErr)
	require.Equal(t, http.StatusUnauthorized, upstreamErr.StatusCode)
	var startedErr *OpenAIPrismStartedError
	require.ErrorAs(t, err, &startedErr)
	require.Equal(t, "request-test", startedErr.RequestID)
}

func TestOpenAIPrismTransportStartResponseWithoutIDIsMarkedUncertain(t *testing.T) {
	base := &prismTestUpstream{}
	transport := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == OpenAIPrismStartPath {
			return prismTestJSON(`{"status":"started"}`), nil
		}
		return base.Do(req, "", 0, 1)
	}), OpenAIPrismTransportOptions{})
	_, err := transport.Do(context.Background(), &Account{ID: 1}, "access-secret", OpenAIPrismConversationOptions{})
	require.ErrorContains(t, err, "missing request_id")
	var startedErr *OpenAIPrismStartedError
	require.ErrorAs(t, err, &startedErr)
}

func TestOpenAIPrismTransportStartNetworkErrorIsMarkedUncertain(t *testing.T) {
	base := &prismTestUpstream{}
	transport := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == OpenAIPrismStartPath {
			return nil, io.ErrUnexpectedEOF
		}
		return base.Do(req, "", 0, 1)
	}), OpenAIPrismTransportOptions{})
	_, err := transport.Do(context.Background(), &Account{ID: 1}, "access-secret", OpenAIPrismConversationOptions{})
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	var startedErr *OpenAIPrismStartedError
	require.ErrorAs(t, err, &startedErr)
}

func TestOpenAIPrismTransportPendingWithoutProgressSignalsHeartbeat(t *testing.T) {
	base := &prismTestUpstream{}
	transport := NewOpenAIPrismTransportFromUpstream(base, OpenAIPrismTransportOptions{PollInterval: time.Millisecond, PollLimit: 3})
	progressCount := 0
	resp, err := transport.Do(context.Background(), &Account{ID: 1}, "access-secret", OpenAIPrismConversationOptions{OnProgress: func(OpenAIPrismProgress) { progressCount++ }})
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, 1, progressCount)
}

func TestOpenAIPrismTransportPollNetworkErrorPreservesCause(t *testing.T) {
	base := &prismTestUpstream{}
	transport := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == OpenAIPrismStatusPath {
			return nil, io.ErrUnexpectedEOF
		}
		return base.Do(req, "", 0, 1)
	}), OpenAIPrismTransportOptions{})
	_, err := transport.Do(context.Background(), &Account{ID: 1}, "access-secret", OpenAIPrismConversationOptions{})
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	var startedErr *OpenAIPrismStartedError
	require.ErrorAs(t, err, &startedErr)
	require.Equal(t, "req-1", startedErr.RequestID)
}

func TestOpenAIPrismTransportFileAndRenderProtocol(t *testing.T) {
	calls := 0
	transport := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		var data []byte
		if req.Body != nil {
			var err error
			data, err = io.ReadAll(req.Body)
			require.NoError(t, err)
		}
		switch calls {
		case 1:
			require.Equal(t, "/api/project-files/upload", req.URL.Path)
			require.Equal(t, http.MethodPost, req.Method)
			require.Equal(t, []byte{0, 1, 2, 255}, data)
			require.Equal(t, "application/octet-stream", req.Header.Get("Content-Type"))
			require.Equal(t, "project-test", req.Header.Get("x-prism-project-id"))
			require.Equal(t, "file-test", req.Header.Get("x-prism-file-id"))
			require.Equal(t, "diagram.png", req.Header.Get("x-prism-file-name"))
			require.Equal(t, "4", req.Header.Get("x-prism-file-size"))
			require.Equal(t, "true", req.Header.Get("x-prism-require-project-edit-access"))
		case 2:
			require.Equal(t, "/api/projects/project-test/thumbnail", req.URL.Path)
			require.Equal(t, http.MethodPatch, req.Method)
			require.JSONEq(t, `{"thumbnail_uuid":"thumbnail-test"}`, string(data))
		case 3:
			require.Equal(t, "/s/sandboxes/proxy/render", req.URL.Path)
			require.Equal(t, http.MethodPost, req.Method)
			require.Equal(t, "async", req.URL.Query().Get("renderMode"))
			require.Equal(t, "json", req.URL.Query().Get("renderStatusMode"))
			require.Equal(t, "stream-v1", req.URL.Query().Get("renderResultMode"))
			require.Equal(t, "sandbox-secret", req.Header.Get("x-crixet-sandbox-token"))
			require.JSONEq(t, `{"mainDocument":"main.tex","clientStateVector":"vector-test","clientDeleteSetUpdate":"delete-test"}`, string(data))
			resp := prismTestJSON(`{"status":"pending","statusPath":"/s/sandboxes/proxy/render-status?jobId=job-test"}`)
			resp.StatusCode = http.StatusAccepted
			return resp, nil
		case 4:
			require.Equal(t, "/s/sandboxes/proxy/render-status", req.URL.Path)
			require.Equal(t, "job-test", req.URL.Query().Get("jobId"))
			require.Equal(t, http.MethodGet, req.Method)
			require.Equal(t, "sandbox-secret", req.Header.Get("x-crixet-sandbox-token"))
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/pdf"}, "Set-Cookie": []string{"__cf_bm=render-cookie; Path=/"}}, Body: io.NopCloser(strings.NewReader("%PDF-1.7\n"))}, nil
		default:
			t.Fatalf("unexpected call %d", calls)
		}
		return prismTestJSON(`{}`), nil
	}), OpenAIPrismTransportOptions{})
	ctx, account := context.Background(), &Account{ID: 1}
	_, err := transport.UploadProjectFile(ctx, account, "access-secret", "project-test", "file-test", "diagram.png", "application/octet-stream", []byte{0, 1, 2, 255})
	require.NoError(t, err)
	_, err = transport.SetProjectThumbnail(ctx, account, "access-secret", "project-test", "thumbnail-test")
	require.NoError(t, err)
	data, err := transport.RenderSandbox(ctx, account, "access-secret", "sandbox-secret", "main.tex", "vector-test", "delete-test")
	require.NoError(t, err)
	var render struct {
		StatusPath string `json:"statusPath"`
	}
	require.NoError(t, json.Unmarshal(data, &render))
	data, headers, err := transport.RenderStatus(ctx, account, "access-secret", "sandbox-secret", render.StatusPath)
	require.NoError(t, err)
	require.Equal(t, "%PDF-1.7\n", string(data))
	require.Equal(t, "application/pdf", headers.Get("Content-Type"))
	require.Empty(t, headers.Get("Set-Cookie"))
	for _, path := range []string{"https://other.example/s/sandboxes/proxy/render-status", "//other.example/s/sandboxes/proxy/render-status", "/auth/session", "/s/sandboxes/proxy/render-status#fragment"} {
		_, _, err := transport.RenderStatus(ctx, account, "access-secret", "sandbox-secret", path)
		require.ErrorContains(t, err, "invalid Prism render status path")
	}
	require.Equal(t, 4, calls)
}

func TestOpenAIPrismInputRetainsInstructionsAndOpaqueItems(t *testing.T) {
	input := prismInput(&apicompat.ResponsesRequest{Instructions: "follow the rules", Input: json.RawMessage(`"hello"`)})
	var messages []map[string]any
	require.NoError(t, json.Unmarshal(input, &messages))
	require.Len(t, messages, 2)
	require.Equal(t, "system", messages[0]["role"])
	require.Equal(t, "user", messages[1]["role"])
	require.Contains(t, string(input), "follow the rules")
	input = prismInput(&apicompat.ResponsesRequest{Instructions: "rules", Input: json.RawMessage(`[{"type":"future_type","large":9007199254740993123,"unknown":null}]`)})
	require.Contains(t, string(input), "9007199254740993123")
	require.Contains(t, string(input), `"unknown":null`)
}

func TestOpenAIPrismResponsesEventContract(t *testing.T) {
	payload := []byte(`{"id":"response-test","created_at":1800000000,"codexDebug":{"sandbox_token":"do-not-expose"},"codexListenSnapshot":{"secret":"do-not-expose"},"output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]},{"type":"function_call","call_id":"call-test","name":"lookup","arguments":"{\"q\":\"hello\"}"}]}`)
	resp := prismResponsesHTTP(payload, "request-test", "conversation-private", "project-private", true, "model-test")
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NotContains(t, string(data), "do-not-expose")
	require.NotContains(t, fmt.Sprint(resp.Header), "private")
	var events []map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "data: {") {
			var event map[string]any
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
			require.Equal(t, float64(len(events)), event["sequence_number"])
			events = append(events, event)
		}
	}
	require.NotEmpty(t, events)
	created, ok := events[0]["response"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "response", created["object"])
	require.Equal(t, "model-test", created["model"])
	require.Equal(t, float64(1800000000), created["created_at"])
	completed, ok := events[len(events)-1]["response"].(map[string]any)
	require.True(t, ok)
	output, ok := completed["output"].([]any)
	require.True(t, ok)
	doneItems := make(map[float64]any)
	for _, event := range events {
		switch event["type"] {
		case "response.output_item.added":
			item, ok := event["item"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, "in_progress", item["status"])
			if item["type"] == "message" {
				require.Empty(t, item["content"])
			}
			if item["type"] == "function_call" {
				require.Equal(t, "", item["arguments"])
			}
		case "response.output_text.delta", "response.output_text.done":
			require.Equal(t, "response-test_item_0", event["item_id"])
			require.NotNil(t, event["logprobs"])
		case "response.output_item.done":
			index, ok := event["output_index"].(float64)
			require.True(t, ok)
			doneItems[index] = event["item"]
		}
	}
	require.Equal(t, output[0], doneItems[0])
	require.Equal(t, output[1], doneItems[1])
	resp = prismResponsesHTTP(payload, "request-test", "conversation-private", "project-private", false, "model-test")
	data, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	var nonstream map[string]any
	require.NoError(t, json.Unmarshal(data, &nonstream))
	require.Equal(t, completed, nonstream)
	require.Len(t, resp.Header, 2)
	require.NotContains(t, string(data), "codexDebug")
	require.NotContains(t, string(data), "codexListenSnapshot")
	empty := prismResponsesObject([]byte(`{"output":null}`), "fallback-id")
	require.Equal(t, "fallback-id", empty["id"])
	require.NotNil(t, empty["output"])
	require.Empty(t, empty["output"])
}
