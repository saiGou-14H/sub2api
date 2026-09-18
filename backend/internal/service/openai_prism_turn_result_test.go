package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPrismStartSynchronousSuccessDoesNotPoll(t *testing.T) {
	base := &prismTestUpstream{}
	polls := 0
	tr := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == OpenAIPrismStartPath {
			return prismTestJSON(`{"status":"completed","request_id":"req-sync","conversation_id":"conversation-sync","response":{"status":"success","payload":{"id":"resp-sync","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"SYNC_OK"}]}],"codexListenSnapshot":{"cursor":9007199254740993}}}}`), nil
		}
		if req.URL.Path == OpenAIPrismStatusPath {
			polls++
		}
		return base.Do(req, "", 0, 1)
	}), OpenAIPrismTransportOptions{})
	resp, err := tr.Do(context.Background(), &Account{ID: 1}, "access-secret", OpenAIPrismConversationOptions{})
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Zero(t, polls)
	state := tr.OpenAIPrismSessionState()
	require.Equal(t, "resp-sync", state.ResponseID)
	require.Equal(t, "conversation-sync", state.ConversationID)
	require.JSONEq(t, `{"cursor":9007199254740993}`, string(state.CodexListenSnapshot))
}

func TestPrismTerminalBusinessErrorNeverPollsOrLeaksDebug(t *testing.T) {
	for _, path := range []string{OpenAIPrismStartPath, OpenAIPrismStatusPath} {
		t.Run(path, func(t *testing.T) {
			base := &prismTestUpstream{}
			polls := 0
			body := `{"status":"completed","request_id":"req-error","response":{"status":"error","payload":{"httpStatus":400,"message":"message-private-secret","rootCause":"root-private-secret","codexRequestDebug":{"error":{"bodyText":"{\"error\":{\"message\":\"400: Unsupported assistant model\"}}","requestHeaders":{"authorization":"Bearer auth-private-secret","x-crixet-sandbox-token":"sandbox-private-secret"}}}}}}`
			tr := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == OpenAIPrismStatusPath {
					polls++
				}
				if req.URL.Path == path {
					return prismTestJSON(body), nil
				}
				return base.Do(req, "", 0, 1)
			}), OpenAIPrismTransportOptions{PollInterval: time.Millisecond, PollLimit: 2})
			_, err := tr.Do(context.Background(), &Account{ID: 1}, "access-secret", OpenAIPrismConversationOptions{})
			require.ErrorContains(t, err, "Unsupported assistant model")
			require.NotContains(t, err.Error(), "private-secret")
			require.NotContains(t, err.Error(), "turn_state is required")
			var started *OpenAIPrismStartedError
			require.ErrorAs(t, err, &started)
			var upstream *OpenAIPrismHTTPError
			require.ErrorAs(t, err, &upstream)
			require.Equal(t, http.StatusBadRequest, upstream.StatusCode)
			require.Equal(t, path, upstream.Path)
			if path == OpenAIPrismStartPath {
				require.Zero(t, polls)
			} else {
				require.Equal(t, 1, polls)
			}
			require.Empty(t, tr.OpenAIPrismSessionState().ResponseID)
		})
	}
}

func TestPrismStartRejectsUnusableStateBeforePolling(t *testing.T) {
	for _, state := range []string{"", `null`, `{}`, `[]`, `"opaque"`, `0`, `true`} {
		t.Run("state="+state, func(t *testing.T) {
			base := &prismTestUpstream{}
			polls := 0
			tr := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == OpenAIPrismStartPath {
					body := `{"status":"started","request_id":"req-invalid"`
					if state != "" {
						body += `,"turn_state":` + state
					}
					body += `}`
					return prismTestJSON(body), nil
				}
				if req.URL.Path == OpenAIPrismStatusPath {
					polls++
				}
				return base.Do(req, "", 0, 1)
			}), OpenAIPrismTransportOptions{})
			_, err := tr.Do(context.Background(), &Account{ID: 1}, "access-secret", OpenAIPrismConversationOptions{})
			require.ErrorContains(t, err, "missing valid non-empty turn_state")
			var started *OpenAIPrismStartedError
			require.ErrorAs(t, err, &started)
			require.Equal(t, "req-invalid", started.RequestID)
			require.Zero(t, polls)
		})
	}
}

func TestPrismPendingStateAbsentOrNullRetainsOpaqueState(t *testing.T) {
	const opaque = `{"cursor":9007199254740993123,"unknown":null,"nested":{"sandbox_token":"synthetic-secret"}}`
	for _, state := range []string{"", `,"turn_state":null`} {
		t.Run("pending"+state, func(t *testing.T) {
			base := &prismTestUpstream{}
			polls := 0
			tr := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case OpenAIPrismStartPath:
					return prismTestJSON(`{"status":"started","request_id":"req-opaque","turn_state":` + opaque + `}`), nil
				case OpenAIPrismStatusPath:
					polls++
					var poll struct {
						TurnState json.RawMessage `json:"turn_state"`
					}
					require.NoError(t, json.NewDecoder(req.Body).Decode(&poll))
					require.JSONEq(t, opaque, string(poll.TurnState))
					require.Contains(t, string(poll.TurnState), "9007199254740993123")
					if polls == 1 {
						return prismTestJSON(`{"status":"pending"` + state + `}`), nil
					}
					return prismTestJSON(`{"status":"completed","response":{"status":"success","payload":{"id":"resp-opaque","output":[]}}}`), nil
				}
				return base.Do(req, "", 0, 1)
			}), OpenAIPrismTransportOptions{PollInterval: time.Millisecond, PollLimit: 2})
			resp, err := tr.Do(context.Background(), &Account{ID: 1}, "access-secret", OpenAIPrismConversationOptions{})
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, 2, polls)
		})
	}
}

func TestPrismPendingInvalidStateStopsBeforeNextPoll(t *testing.T) {
	for _, state := range []string{`{}`, `[]`, `"not-an-object"`} {
		t.Run(state, func(t *testing.T) {
			base := &prismTestUpstream{}
			polls := 0
			tr := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == OpenAIPrismStatusPath {
					polls++
					return prismTestJSON(`{"status":"pending","turn_state":` + state + `}`), nil
				}
				return base.Do(req, "", 0, 1)
			}), OpenAIPrismTransportOptions{PollInterval: time.Millisecond, PollLimit: 2})
			_, err := tr.Do(context.Background(), &Account{ID: 1}, "access-secret", OpenAIPrismConversationOptions{})
			require.ErrorContains(t, err, "invalid turn_state")
			require.Equal(t, 1, polls)
		})
	}
}
