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

type prismProjectFlowStore struct {
	*prismWorkspaceTestStore
	sessions map[string][]byte
}

func (s *prismProjectFlowStore) SetPrismSession(_ context.Context, key string, data []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[string][]byte)
	}
	s.sessions[key] = append([]byte(nil), data...)
	return nil
}

func (s *prismProjectFlowStore) GetPrismSession(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.sessions[key]...), nil
}

// Exercise project ownership, native files, gateway response state and workspace
// artifacts together, including a continuation restored on another instance.
func TestOpenAIPrismProjectChatArtifactsAndRestart(t *testing.T) {
	for _, bridge := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "optional_bridge"}[bridge], func(t *testing.T) {
			ctx := context.Background()
			store := &prismProjectFlowStore{prismWorkspaceTestStore: &prismWorkspaceTestStore{}}
			first, key, _, repo := prismWorkspaceFixture(t, store.prismWorkspaceTestStore)
			first.cache = store
			account := &repo.accounts[0]
			account.Extra["prism_prompt_tool_bridge"] = bridge
			upstream := &prismGatewayUpstream{}
			var startCookies []string
			factory := func() *OpenAIPrismTransport {
				transport := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
					switch req.URL.Path {
					case "/api/project-files/upload":
						return prismTestJSON(`{"fileUuid":"test-resource-id"}`), nil
					case "/s/sandboxes/proxy/render":
						return prismTestJSON(`{"status":"pending"}`), nil
					case "/s/sandboxes/proxy/render-status":
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/pdf"}}, Body: io.NopCloser(strings.NewReader("%PDF-1.7 test artifact"))}, nil
					case "/s/sandboxes/proxy/heartbeat":
						return prismTestJSON(`{"status":"success"}`), nil
					case OpenAIPrismStartPath:
						startCookies = append(startCookies, req.Header.Get("Cookie"))
					}
					response, err := upstream.Do(req, "", account.ID, 1)
					if err != nil || req.URL.Path != OpenAIPrismStatusPath {
						return response, err
					}
					var status map[string]any
					defer response.Body.Close()
					require.NoError(t, json.NewDecoder(response.Body).Decode(&status))
					payload := status["response"].(map[string]any)["payload"].(map[string]any)
					payload["codexListenSnapshot"] = map[string]any{"cursor": "test-cursor"}
					payload["codexDeltaFiles"] = []any{map[string]any{"file_path": "main.tex", "status": "modified", "diff": "+test document"}}
					encoded, _ := json.Marshal(status)
					response.Body = io.NopCloser(strings.NewReader(string(encoded)))
					return response, nil
				}), OpenAIPrismTransportOptions{PollInterval: time.Millisecond, PollLimit: 4})
				transport.attachFile = func(_ context.Context, _ *Account, _, _, _, fileName string) (string, error) { return fileName, nil }
				return transport
			}
			first.openAIPrismTransportFactory = factory
			project, err := first.CreatePrismProject(ctx, key, PrismProjectCreateRequest{Title: "Example"})
			require.NoError(t, err)
			upload, err := first.OperatePrismProject(ctx, key, project.Handle, PrismProjectOperation{Name: "upload", FileName: "figure.png", ContentType: "image/png", Data: []byte("synthetic resource")})
			require.NoError(t, err)
			require.Contains(t, string(upload.Body), "prism_file_")
			body := `{"model":"gpt-5.6-sol","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"review the project"},{"type":"input_file","filename":"main.tex","project_path":"main.tex"}]}]}`
			c, recorder := prismGatewayTestContext("/v1/responses", body)
			c.Set("api_key", key)
			c.Request = c.Request.WithContext(WithPrismProject(ctx, project))
			result, err := first.forwardResponsesViaOpenAIPrism(c.Request.Context(), c, account, []byte(body))
			require.NoError(t, err)
			require.Contains(t, recorder.Body.String(), "hello")
			require.Equal(t, "unsynced", recorder.Header().Get("X-Prism-Sync-Status"))
			require.Len(t, upstream.starts, 1)
			require.NotEmpty(t, result.ResponseID)
			require.Equal(t, project.State.ProjectID, upstream.starts[0]["metadata"].(map[string]any)["projectId"])
			input, _ := json.Marshal(upstream.starts[0]["input"])
			require.Contains(t, string(input), `"project_path":"main.tex"`)

			// A different instance refreshes the authorization after response 1.
			second := &OpenAIGatewayService{cache: store, accountRepo: repo, openAIPrismTransportFactory: factory}
			updated, err := second.LoadPrismProject(ctx, key, project.Handle)
			require.NoError(t, err)
			require.Equal(t, "unsynced", updated.State.DeltaSync.Status)
			require.Len(t, updated.State.DeltaSync.Files, 1)
			require.Equal(t, "invalid_diff", updated.State.DeltaSync.Files[0].Reason)
			release, err := second.LockPrismProject(ctx, updated)
			require.NoError(t, err)
			state := updated.State
			state.SandboxToken = "new-sandbox-authorization"
			state.Cookies = []*http.Cookie{{Name: "prism_session_token", Value: "new-cookie-authorization"}}
			require.NoError(t, second.SavePrismProjectState(ctx, updated, state))
			release()

			// Omitting the project header still restores ownership and takes its
			// lock. The old response cursor must not restore stale sandbox auth.
			continuation := &apicompat.ResponsesRequest{Model: OpenAIPrismDefaultModel, Input: json.RawMessage(`"continue"`), PreviousResponseID: result.ResponseID}
			for _, gateway := range []*OpenAIGatewayService{first, second} {
				next, _ := prismGatewayTestContext("/v1/responses", "")
				next.Set("api_key", key)
				response, err := gateway.doOpenAIPrism(ctx, next, account, continuation)
				require.NoError(t, err)
				_ = response.Body.Close()
				last := upstream.starts[len(upstream.starts)-1]
				require.Equal(t, project.State.ProjectID, last["metadata"].(map[string]any)["projectId"])
				require.Equal(t, "new-sandbox-authorization", last["metadata"].(map[string]any)["sandbox_token"])
				require.Contains(t, startCookies[len(startCookies)-1], "new-cookie-authorization")
				require.Equal(t, continuation.PreviousResponseID, last["previousResponseId"])
			}
			delta, err := second.OperatePrismProject(ctx, key, project.Handle, PrismProjectOperation{Name: "delta-files"})
			require.NoError(t, err)
			require.Contains(t, string(delta.Body), "+test document")
			syncStatus, err := second.OperatePrismProject(ctx, key, project.Handle, PrismProjectOperation{Name: "sync-status"})
			require.NoError(t, err)
			require.Contains(t, string(syncStatus.Body), `"status":"unsynced"`)
			require.Contains(t, string(syncStatus.Body), `"reason":"invalid_diff"`)
			require.Len(t, upstream.starts, 3) // One start per requested turn, no replay after sync failure.
			_, err = second.OperatePrismProject(ctx, key, project.Handle, PrismProjectOperation{Name: "render", MainDocument: "main.tex"})
			require.NoError(t, err)
			pdf, err := second.OperatePrismProject(ctx, key, project.Handle, PrismProjectOperation{Name: "pdf"})
			require.NoError(t, err)
			require.Equal(t, "application/pdf", pdf.ContentType)
			require.Equal(t, "%PDF-1.7 test artifact", string(pdf.Body))
		})
	}
}
