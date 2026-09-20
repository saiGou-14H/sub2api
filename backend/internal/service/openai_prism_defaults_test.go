package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

func TestPrismStartUsesCurrentDefaultsAndPreservesExplicitMapping(t *testing.T) {
	for _, tc := range []struct {
		name          string
		request       *apicompat.ResponsesRequest
		model, effort string
	}{
		{"default", nil, "gpt-5.6-sol", "medium"},
		{"empty", &apicompat.ResponsesRequest{Reasoning: &apicompat.ResponsesReasoning{}}, "gpt-5.6-sol", "medium"},
		{"explicit", &apicompat.ResponsesRequest{Model: "custom-prism-model", Reasoning: &apicompat.ResponsesReasoning{Effort: "xhigh"}}, "custom-prism-model", "xhigh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := &prismTestUpstream{}
			seen := false
			tr := NewOpenAIPrismTransportFromUpstream(prismUpstreamFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == OpenAIPrismStartPath {
					seen = true
					var start struct {
						Metadata struct {
							Model  string `json:"model"`
							Effort string `json:"reasoning_effort"`
						} `json:"metadata"`
					}
					require.NoError(t, json.NewDecoder(req.Body).Decode(&start))
					require.Equal(t, tc.model, start.Metadata.Model)
					require.Equal(t, tc.effort, start.Metadata.Effort)
				}
				return base.Do(req, "", 0, 1)
			}), OpenAIPrismTransportOptions{PollInterval: time.Millisecond, PollLimit: 2})
			resp, err := tr.Do(context.Background(), &Account{ID: 1}, "access-secret", OpenAIPrismConversationOptions{Request: tc.request})
			require.NoError(t, err)
			resp.Body.Close()
			require.True(t, seen)
		})
	}
}
