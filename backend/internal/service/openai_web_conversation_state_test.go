package service

import (
	"context"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type testOpenAIWebStateBody struct {
	conversationID string
	parentID       string
}

func (b *testOpenAIWebStateBody) Read([]byte) (int, error) { return 0, io.EOF }

func (b *testOpenAIWebStateBody) OpenAIWebConversationState() (string, string) {
	return b.conversationID, b.parentID
}

func testOpenAIWebContinuationContext(t *testing.T, sessionID string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	groupID := int64(7)
	c.Set("api_key", &APIKey{ID: 42, GroupID: &groupID})
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Request.Header.Set("session-id", sessionID)
	return c
}

func TestPrepareOpenAIWebContinuationReusesOnlyLatestUserMessage(t *testing.T) {
	service := &OpenAIGatewayService{openaiWSStateStore: NewOpenAIWSStateStore(nil)}
	account := &Account{ID: 101}
	first := &apicompat.ChatCompletionsRequest{
		Model: "auto",
		Messages: []apicompat.ChatMessage{{
			Role:    "user",
			Content: []byte(`"first"`),
		}},
	}
	body := []byte(`{"model":"auto","prompt_cache_key":"session-1","messages":[{"role":"user","content":"first"}]}`)
	c := testOpenAIWebContinuationContext(t, "session-1")
	transportReq, continuation := service.prepareOpenAIWebContinuation(context.Background(), c, account, "auto", body, first)
	require.Same(t, first, transportReq)
	require.True(t, continuation.eligible)

	service.commitOpenAIWebContinuation(context.Background(), c, account, "auto", first, "resp_public_1", &testOpenAIWebStateBody{
		conversationID: "conv-web-1",
		parentID:       "msg-web-1",
	}, continuation)

	second := &apicompat.ChatCompletionsRequest{
		Model: "auto",
		Messages: []apicompat.ChatMessage{
			{Role: "user", Content: []byte(`"first"`)},
			{Role: "assistant", Content: []byte(`"answer"`)},
			{Role: "user", Content: []byte(`"second"`)},
		},
	}
	secondBody := []byte(`{"model":"auto","prompt_cache_key":"session-1","messages":[{"role":"user","content":"first"},{"role":"assistant","content":"answer"},{"role":"user","content":"second"}]}`)
	transportReq, continuation = service.prepareOpenAIWebContinuation(context.Background(), c, account, "auto", secondBody, second)
	require.True(t, continuation.reused)
	require.Len(t, transportReq.Messages, 1)
	require.Equal(t, `"second"`, string(transportReq.Messages[0].Content))
	require.Equal(t, "conv-web-1", continuation.state.ConversationID)
	require.Equal(t, "msg-web-1", continuation.state.ParentMessageID)
}
