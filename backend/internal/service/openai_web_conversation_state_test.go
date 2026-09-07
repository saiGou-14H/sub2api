package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type testOpenAIWebStateBody struct {
	conversationID       string
	parentID             string
	assistantText        string
	promptToolSignatures []string
}

func (b *testOpenAIWebStateBody) Read([]byte) (int, error) { return 0, io.EOF }

func (b *testOpenAIWebStateBody) OpenAIWebConversationState() (string, string) {
	return b.conversationID, b.parentID
}

func (b *testOpenAIWebStateBody) OpenAIWebAssistantText() string {
	return b.assistantText
}

func (b *testOpenAIWebStateBody) OpenAIWebPromptToolCallSignatures() []string {
	return append([]string(nil), b.promptToolSignatures...)
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
		assistantText:  "answer",
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

func TestPrepareOpenAIWebContinuationReusesLatestAttachmentMessage(t *testing.T) {
	service := &OpenAIGatewayService{openaiWSStateStore: NewOpenAIWSStateStore(nil)}
	account := &Account{ID: 103}
	c := testOpenAIWebContinuationContext(t, "session-attachment")
	first := &apicompat.ChatCompletionsRequest{
		Model:    "auto",
		Messages: []apicompat.ChatMessage{{Role: "user", Content: []byte(`"first"`)}},
	}
	_, continuation := service.prepareOpenAIWebContinuation(context.Background(), c, account, "auto", nil, first)
	service.commitOpenAIWebContinuation(context.Background(), c, account, "auto", first, "resp_attachment_1", &testOpenAIWebStateBody{
		conversationID: "conv-attachment-1",
		parentID:       "msg-attachment-1",
		assistantText:  "answer",
	}, continuation)

	attachment := &apicompat.ChatCompletionsRequest{
		Model: "auto",
		Messages: []apicompat.ChatMessage{
			{Role: "user", Content: []byte(`"first"`)},
			{Role: "assistant", Content: []byte(`"answer"`)},
			{Role: "user", Content: []byte(`[{"type":"file","file":{"file_id":"file-1","filename":"report.pdf","mime_type":"application/pdf"}}]`)},
		},
	}
	transportReq, next := service.prepareOpenAIWebContinuation(context.Background(), c, account, "auto", nil, attachment)
	require.True(t, next.reused)
	require.Len(t, transportReq.Messages, 1)
	require.Equal(t, attachment.Messages[2], transportReq.Messages[0])
}

func TestPrepareOpenAIWebContinuationCompactsToolResultAgainstCursor(t *testing.T) {
	service := &OpenAIGatewayService{openaiWSStateStore: NewOpenAIWSStateStore(nil)}
	account := &Account{ID: 104}
	c := testOpenAIWebContinuationContext(t, "session-tool-result")
	first := &apicompat.ChatCompletionsRequest{
		Model:    "auto",
		Messages: []apicompat.ChatMessage{{Role: "user", Content: json.RawMessage(`"run the command"`)}},
	}
	_, firstContinuation := service.prepareOpenAIWebContinuation(context.Background(), c, account, "auto", nil, first)
	service.commitOpenAIWebContinuation(context.Background(), c, account, "auto", first, "resp_tool_1", &testOpenAIWebStateBody{
		conversationID: "conv-tool-1",
		parentID:       "msg-tool-1",
		assistantText:  `{"protocol":"sub2api.prompt_tool.v1","calls":[{"name":"exec_command"}]}`,
	}, firstContinuation)

	second := &apicompat.ChatCompletionsRequest{
		Model: "auto",
		Messages: []apicompat.ChatMessage{
			{Role: "user", Content: json.RawMessage(`"run the command"`)},
			{Role: "assistant", ToolCalls: []apicompat.ChatToolCall{{ID: "call_exec", Type: "function", Function: apicompat.ChatFunctionCall{Name: "exec_command", Arguments: `{"cmd":"dir"}`}}}},
			{Role: "tool", ToolCallID: "call_exec", Content: json.RawMessage(`"ok"`)},
		},
	}
	transportReq, continuation := service.prepareOpenAIWebContinuation(context.Background(), c, account, "auto", nil, second)
	require.True(t, continuation.reused)
	require.Equal(t, "conv-tool-1", continuation.state.ConversationID)
	require.Len(t, transportReq.Messages, 1)
	require.Equal(t, "tool", transportReq.Messages[0].Role)
	require.Equal(t, "call_exec", transportReq.Messages[0].ToolCallID)
	service.commitOpenAIWebContinuation(context.Background(), c, account, "auto", second, "resp_tool_2", &testOpenAIWebStateBody{
		conversationID: "conv-tool-1",
		parentID:       "msg-tool-2",
		assistantText:  "command finished",
	}, continuation)

	third := &apicompat.ChatCompletionsRequest{
		Model: "auto",
		Messages: []apicompat.ChatMessage{
			{Role: "user", Content: json.RawMessage(`"run the command"`)},
			{Role: "assistant", ToolCalls: []apicompat.ChatToolCall{{ID: "call_exec", Type: "function", Function: apicompat.ChatFunctionCall{Name: "exec_command", Arguments: `{"cmd":"dir"}`}}}},
			{Role: "tool", ToolCallID: "call_exec", Content: json.RawMessage(`"ok"`)},
			{Role: "assistant", Content: json.RawMessage(`"command finished"`)},
			{Role: "user", Content: json.RawMessage(`"what next?"`)},
		},
	}
	transportReq, continuation = service.prepareOpenAIWebContinuation(context.Background(), c, account, "auto", nil, third)
	require.True(t, continuation.reused)
	require.Len(t, transportReq.Messages, 1)
	require.Equal(t, `"what next?"`, string(transportReq.Messages[0].Content))
	service.commitOpenAIWebContinuation(context.Background(), c, account, "auto", third, "resp_tool_3", &testOpenAIWebStateBody{
		conversationID: "conv-tool-1",
		parentID:       "msg-tool-3",
		assistantText:  "next answer",
	}, continuation)

	toolOutputAndUser := &apicompat.ChatCompletionsRequest{
		Model: "auto",
		Messages: []apicompat.ChatMessage{
			{Role: "tool", ToolCallID: "call_exec", Content: json.RawMessage(`"ok"`)},
			{Role: "user", Content: json.RawMessage(`"what next after that?"`)},
		},
	}
	transportReq, continuation = service.prepareOpenAIWebContinuation(context.Background(), c, account, "auto", []byte(`{"previous_response_id":"resp_tool_3"}`), toolOutputAndUser)
	require.True(t, continuation.reused)
	require.Len(t, transportReq.Messages, 2)
	require.Equal(t, "tool", transportReq.Messages[0].Role)
	require.Equal(t, "user", transportReq.Messages[1].Role)
}

func TestCommitOpenAIWebContinuationStoresPromptToolCallSignatures(t *testing.T) {
	service := &OpenAIGatewayService{openaiWSStateStore: NewOpenAIWSStateStore(nil)}
	account := &Account{ID: 105}
	c := testOpenAIWebContinuationContext(t, "session-tool-signature")
	first := &apicompat.ChatCompletionsRequest{
		Model:    "auto",
		Messages: []apicompat.ChatMessage{{Role: "user", Content: json.RawMessage(`"write the file"`)}},
	}
	_, continuation := service.prepareOpenAIWebContinuation(context.Background(), c, account, "auto", nil, first)
	call := OpenAIWebPromptToolCall{
		Name: "write", Type: "function", Arguments: json.RawMessage(`{"content":"hello","file_path":"C:\\work\\a.txt"}`),
	}
	service.commitOpenAIWebContinuation(context.Background(), c, account, "auto", first, "resp_tool_signature", &testOpenAIWebStateBody{
		conversationID:       "conv-tool-signature",
		parentID:             "msg-tool-signature",
		assistantText:        "tool call",
		promptToolSignatures: []string{openAIWebPromptToolCallSignature(call)},
	}, continuation)

	key := openAIWebStorageKey(c, account.ID, "auto", openAIWebSessionKeyHash(c, "session-tool-signature"))
	state, found, err := service.openaiWSStateStore.GetWebConversationState(context.Background(), 7, key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []string{openAIWebPromptToolCallSignature(call)}, state.LastPromptToolCallSignatures)
}

func TestPrepareOpenAIWebContinuationRejectsEditedOrReorderedHistory(t *testing.T) {
	service := &OpenAIGatewayService{openaiWSStateStore: NewOpenAIWSStateStore(nil)}
	account := &Account{ID: 101}

	for _, test := range []struct {
		name     string
		messages []apicompat.ChatMessage
	}{
		{
			name: "edited assistant",
			messages: []apicompat.ChatMessage{
				{Role: "user", Content: []byte(`"first"`)},
				{Role: "assistant", Content: []byte(`"edited"`)},
				{Role: "user", Content: []byte(`"second"`)},
			},
		},
		{
			name: "reordered history",
			messages: []apicompat.ChatMessage{
				{Role: "assistant", Content: []byte(`"answer"`)},
				{Role: "user", Content: []byte(`"first"`)},
				{Role: "user", Content: []byte(`"second"`)},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := testOpenAIWebContinuationContext(t, "session-drift-"+test.name)
			first := &apicompat.ChatCompletionsRequest{
				Model:    "auto",
				Messages: []apicompat.ChatMessage{{Role: "user", Content: []byte(`"first"`)}},
			}
			_, continuation := service.prepareOpenAIWebContinuation(context.Background(), c, account, "auto", nil, first)
			service.commitOpenAIWebContinuation(context.Background(), c, account, "auto", first, "resp_drift_1", &testOpenAIWebStateBody{
				conversationID: "conv-drift-1",
				parentID:       "msg-drift-1",
				assistantText:  "answer",
			}, continuation)

			transportReq, next := service.prepareOpenAIWebContinuation(context.Background(), c, account, "auto", nil, &apicompat.ChatCompletionsRequest{
				Model:    "auto",
				Messages: test.messages,
			})
			require.False(t, next.reused)
			require.Nil(t, next.state)
			require.Equal(t, test.messages, transportReq.Messages)
		})
	}
}

func TestOpenAIWebContinuationPreviousResponseAliasKeepsCanonicalSessionState(t *testing.T) {
	service := &OpenAIGatewayService{openaiWSStateStore: NewOpenAIWSStateStore(nil)}
	account := &Account{ID: 102}
	c := testOpenAIWebContinuationContext(t, "session-alias")
	first := &apicompat.ChatCompletionsRequest{
		Model:    "auto",
		Messages: []apicompat.ChatMessage{{Role: "user", Content: []byte(`"first"`)}},
	}
	_, firstContinuation := service.prepareOpenAIWebContinuation(context.Background(), c, account, "auto", nil, first)
	service.commitOpenAIWebContinuation(context.Background(), c, account, "auto", first, "resp_alias_1", &testOpenAIWebStateBody{
		conversationID: "conv-alias-1",
		parentID:       "msg-alias-1",
		assistantText:  "answer-1",
	}, firstContinuation)

	second := &apicompat.ChatCompletionsRequest{
		Model:    "auto",
		Messages: []apicompat.ChatMessage{{Role: "user", Content: []byte(`"second"`)}},
	}
	secondBody := []byte(`{"model":"auto","previous_response_id":"resp_alias_1","messages":[{"role":"user","content":"second"}]}`)
	transportReq, secondContinuation := service.prepareOpenAIWebContinuation(context.Background(), c, account, "auto", secondBody, second)
	require.True(t, secondContinuation.reused)
	require.Equal(t, openAIWebResponseAliasKey(c, account.ID, "auto", "resp_alias_1"), secondContinuation.responseAliasKey)
	require.NotEqual(t, secondContinuation.responseAliasKey, secondContinuation.stateKey)
	require.Len(t, transportReq.Messages, 1)

	service.commitOpenAIWebContinuation(context.Background(), c, account, "auto", second, "resp_alias_2", &testOpenAIWebStateBody{
		conversationID: "conv-alias-2",
		parentID:       "msg-alias-2",
		assistantText:  "answer-2",
	}, secondContinuation)
	canonicalKey := openAIWebStorageKey(c, account.ID, "auto", openAIWebSessionKeyHash(c, "session-alias"))
	state, found, err := service.openaiWSStateStore.GetWebConversationState(context.Background(), 7, canonicalKey)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "conv-alias-2", state.ConversationID)
	require.Equal(t, "msg-alias-2", state.ParentMessageID)
}

func TestOpenAIWebConversationLockHonorsCancellationAndRelease(t *testing.T) {
	store := NewOpenAIWSStateStore(nil).(*defaultOpenAIWSStateStore)
	firstRelease, acquired := store.AcquireOpenAIWebConversationLock(context.Background(), 7, "lock-test")
	require.True(t, acquired)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	secondRelease, acquired := store.AcquireOpenAIWebConversationLock(ctx, 7, "lock-test")
	require.False(t, acquired)
	require.Nil(t, secondRelease)

	firstRelease()
	thirdRelease, acquired := store.AcquireOpenAIWebConversationLock(context.Background(), 7, "lock-test")
	require.True(t, acquired)
	thirdRelease()
}
