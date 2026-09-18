package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

// Exercise real document WebSocket synchronization through the gateway, then
// restore the saved project on another instance before reading its artifacts.
// All authorization and document data are synthetic and stay on localhost.
func TestOpenAIPrismGeneratedFilePersistsAcrossGatewayInstances(t *testing.T) {
	for _, tc := range []struct {
		name, failure string
		bridge        bool
	}{
		{"native", "", false},
		{"with_client_tool_bridge", "", true},
		{"native_missing_ack", "readback_conflict", false},
		{"bridge_missing_ack", "readback_conflict", true},
		{"native_sandbox_not_synced", "sandbox_not_synced", false},
		{"bridge_sandbox_not_synced", "sandbox_not_synced", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge := tc.bridge
			var documentMu sync.Mutex
			var documentGroups []byte
			var documentClients uint64
			var document []byte = []byte{0, 0}
			writes, syncsAfterWrite := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/y/d/test/ws" {
					t.Errorf("unexpected local endpoint %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				for {
					_, frame, err := conn.Read(r.Context())
					if err != nil {
						return
					}
					reader := &prismYReader{data: frame}
					require.Equal(t, uint64(0), reader.uint())
					subtype, payload := reader.uint(), reader.bytes()
					require.NoError(t, reader.err)
					switch subtype {
					case 0:
						documentMu.Lock()
						current := append([]byte(nil), document...)
						if writes > 0 && tc.failure == "readback_conflict" {
							current = []byte{0, 0} // Server never acknowledges the update.
						}
						documentMu.Unlock()
						if err := conn.Write(r.Context(), websocket.MessageBinary, prismYSyncFrame(1, current)); err != nil {
							return
						}
					case 2:
						// This scenario creates nodes with fresh clients and no delete
						// set. Merge their v1 client sections into the server snapshot.
						decoded, err := prismReadYUpdate(payload)
						require.NoError(t, err)
						require.Empty(t, decoded.deleted)
						update := &prismYReader{data: payload}
						clients := update.uint()
						require.NoError(t, update.err)
						require.Zero(t, payload[len(payload)-1])
						documentMu.Lock()
						documentClients += clients
						documentGroups = append(documentGroups, payload[update.pos:len(payload)-1]...)
						var merged prismYWriter
						merged.uint(documentClients)
						merged = append(merged, documentGroups...)
						merged.uint(0)
						document = merged
						writes++
						documentMu.Unlock()
					}
				}
			}))
			defer server.Close()
			ctx := context.Background()
			store := &prismProjectFlowStore{prismWorkspaceTestStore: &prismWorkspaceTestStore{}}
			first, key, _, repo := prismWorkspaceFixture(t, store.prismWorkspaceTestStore)
			first.cache = store
			account := &repo.accounts[0]
			account.Extra["prism_prompt_tool_bridge"] = bridge
			upstream := &prismGatewayUpstream{tool: bridge}
			const generated = "\\documentclass{article}\n\\begin{document}\nSynthetic paper\n\\end{document}"
			const patch = "--- render/main.tex\n+++ codex/main.tex\n@@ -0,0 +1,4 @@\n+\\documentclass{article}\n+\\begin{document}\n+Synthetic paper\n+\\end{document}"
			factory := func() *OpenAIPrismTransport {
				return NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
					switch req.URL.Path {
					case OpenAIPrismBackendNewPath:
						return prismTestJSON(string(prismMustJSON(map[string]string{"url": server.URL + "/s/sandboxes/proxy", "token": "sandbox-test-secret"}))), nil
					case "/s/sandboxes/proxy/wait-for-sync":
						documentMu.Lock()
						sandboxFailure := writes > 0 && tc.failure == "sandbox_not_synced"
						if writes > 0 {
							syncsAfterWrite++
						}
						documentMu.Unlock()
						if sandboxFailure {
							return prismTestJSON(`{"status":"pending"}`), nil
						}
						return prismTestJSON(`{"status":"synced"}`), nil
					case "/s/sandboxes/proxy/render":
						documentMu.Lock()
						current := append([]byte(nil), document...)
						documentMu.Unlock()
						doc, err := prismReadYUpdate(current)
						require.NoError(t, err)
						var textNodeID string
						for _, item := range doc.items {
							require.NoError(t, doc.resolve(item, 0))
							if !item.deleted && item.key == "filename" && item.value == "main.tex" && item.parent != nil {
								textNodeID = doc.items[*item.parent].key
							}
						}
						require.NotEmpty(t, textNodeID)
						text, err := doc.textContent(textNodeID)
						require.NoError(t, err)
						require.Equal(t, generated, text.Text)
						return prismTestJSON(`{"status":"pending"}`), nil
					}
					response, err := upstream.Do(req, "", account.ID, 1)
					if err != nil || req.URL.Path != OpenAIPrismStatusPath {
						return response, err
					}
					var status map[string]any
					defer response.Body.Close()
					require.NoError(t, json.NewDecoder(response.Body).Decode(&status))
					payload := status["response"].(map[string]any)["payload"].(map[string]any)
					payload["codexDeltaFiles"] = []any{
						map[string]any{"file_path": "main.tex", "status": "added", "diff": patch},
						map[string]any{"file_path": "AGENTS.md", "status": "added", "diff": "sandbox helper"},
						map[string]any{"file_path": "preview.pdf", "status": "added", "diff": nil},
					}
					response.Body = io.NopCloser(strings.NewReader(string(prismMustJSON(status))))
					return response, nil
				}), OpenAIPrismTransportOptions{BaseURL: server.URL, PollInterval: time.Millisecond, PollLimit: 4})
			}
			first.openAIPrismTransportFactory = factory
			project, err := first.CreatePrismProject(ctx, key, PrismProjectCreateRequest{Title: "Synthetic paper"})
			require.NoError(t, err)
			request := map[string]any{"model": OpenAIPrismDefaultModel, "input": "prepare the project"}
			if bridge {
				request["tools"] = []any{map[string]any{"type": "function", "name": "list_files", "parameters": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]string{"type": "string"}}, "required": []string{"path"}}}}
			}
			body := prismMustJSON(request)
			c, recorder := prismGatewayTestContext("/v1/responses", string(body))
			c.Set("api_key", key)
			c.Request = c.Request.WithContext(WithPrismProject(ctx, project))
			result, err := first.forwardResponsesViaOpenAIPrism(c.Request.Context(), c, account, body)
			require.NoError(t, err)
			require.NotEmpty(t, result.ResponseID)
			require.Len(t, upstream.starts, 1)
			expectedStatus, expectedFileStatus := "partial", "synced"
			if tc.failure != "" {
				expectedStatus, expectedFileStatus = "unsynced", "unsynced"
			}
			require.Equal(t, expectedStatus, recorder.Header().Get("X-Prism-Sync-Status"))
			if bridge {
				require.Contains(t, recorder.Body.String(), "function_call")
				require.Contains(t, recorder.Body.String(), "list_files")
			} else {
				require.Contains(t, recorder.Body.String(), "hello")
				require.NotContains(t, recorder.Body.String(), "function_call")
			}
			documentMu.Lock()
			observedWrites, observedSyncs := writes, syncsAfterWrite
			documentMu.Unlock()
			require.Positive(t, observedWrites)
			if tc.failure == "readback_conflict" {
				require.Zero(t, observedSyncs)
			} else {
				require.Positive(t, observedSyncs)
			}
			second := &OpenAIGatewayService{cache: store, accountRepo: repo, openAIPrismTransportFactory: factory}
			status, err := second.OperatePrismProject(ctx, key, project.Handle, PrismProjectOperation{Name: "sync-status"})
			require.NoError(t, err)
			var outcome OpenAIPrismDeltaSync
			require.NoError(t, json.Unmarshal(status.Body, &outcome))
			require.Equal(t, expectedStatus, outcome.Status)
			statuses := map[string]string{}
			for _, file := range outcome.Files {
				statuses[file.Path] = file.Status
				if file.Path == "main.tex" {
					require.Equal(t, tc.failure, file.Reason)
				}
			}
			require.Equal(t, map[string]string{"main.tex": expectedFileStatus, "AGENTS.md": "skipped", "preview.pdf": "unsynced"}, statuses)
			if tc.failure == "" {
				_, err = second.OperatePrismProject(ctx, key, project.Handle, PrismProjectOperation{Name: "render", MainDocument: "main.tex"})
				require.NoError(t, err)
			}
			require.Len(t, upstream.starts, 1)
		})
	}
}
