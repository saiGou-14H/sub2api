package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type webHandoffTestUpstream struct {
	responses []*http.Response
}

func (u *webHandoffTestUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	if len(u.responses) == 0 {
		return nil, errors.New("no mocked response")
	}
	response := u.responses[0]
	u.responses = u.responses[1:]
	return response, nil
}

func (u *webHandoffTestUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

type webHandoffTestConn struct {
	frames [][]byte
	writes []any
	closed bool
}

func (c *webHandoffTestConn) WriteJSON(_ context.Context, value any) error {
	c.writes = append(c.writes, value)
	return nil
}

func (c *webHandoffTestConn) ReadMessage(_ context.Context) ([]byte, error) {
	if len(c.frames) == 0 {
		return nil, io.EOF
	}
	frame := c.frames[0]
	c.frames = c.frames[1:]
	return frame, nil
}

func (c *webHandoffTestConn) Ping(context.Context) error { return nil }

func (c *webHandoffTestConn) Close() error {
	c.closed = true
	return nil
}

type webHandoffTestDialer struct {
	conn    *webHandoffTestConn
	url     string
	headers http.Header
}

func (d *webHandoffTestDialer) Dial(_ context.Context, wsURL string, headers http.Header, _ string) (openAIWSClientConn, int, http.Header, error) {
	d.url = wsURL
	d.headers = headers.Clone()
	return d.conn, 0, nil, nil
}

func TestParseOpenAIWebConversationHandoff(t *testing.T) {
	frames := []openAIWebSSEFrame{
		{data: `{"type":"resume_conversation_token","kind":"topic","token":"opaque","conversation_id":"conv"}`},
		{data: `{"type":"stream_handoff","conversation_id":"conv","turn_exchange_id":"turn","options":[{"type":"resume_sse_endpoint","topic_id":"topic"},{"type":"subscribe_ws_topic","topic_id":"topic"}]}`},
		{data: "[DONE]"},
	}
	handoff, found, err := parseOpenAIWebConversationHandoff(frames)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, openAIWebConversationHandoff{ConversationID: "conv", TurnExchangeID: "turn", TopicID: "topic"}, handoff)
}

func TestOpenAIWebDirectStreamFrameTreatsBareV1AsProtocolPrelude(t *testing.T) {
	require.False(t, openAIWebDirectStreamFrame(openAIWebSSEFrame{data: `"v1"`}))
}

func TestOpenAIWebTopicBodyUnwrapsEncodedItemsAndDone(t *testing.T) {
	encodedItem := "event: delta_encoding\ndata: \"v1\"\n\nevent: delta\ndata: {\"p\":\"/message/content/parts/0\",\"o\":\"append\",\"v\":\"OK\"}\n\n"
	frame, err := json.Marshal([]any{map[string]any{
		"type":     "message",
		"topic_id": "topic",
		"payload": map[string]any{
			"type": "conversation-turn-stream",
			"payload": map[string]any{
				"type":           "stream-item",
				"stream_item_id": "item-1",
				"encoded_item":   encodedItem,
			},
		},
	}})
	require.NoError(t, err)
	doneFrame, err := json.Marshal([]any{map[string]any{
		"type":     "message",
		"topic_id": "topic",
		"payload": map[string]any{
			"type":    "conversation-turn-stream",
			"payload": map[string]any{"type": "done"},
		},
	}})
	require.NoError(t, err)
	conn := &webHandoffTestConn{frames: [][]byte{frame, doneFrame}}
	body := &openAIWebTopicBody{ctx: context.Background(), conn: conn, topic: "topic", seen: map[string]struct{}{}}
	converted := NewOpenAIWebResponsesBody(body, "gpt-5.6-sol-wm")
	result, err := io.ReadAll(converted)
	require.NoError(t, err)
	require.Contains(t, string(result), `"delta":"OK"`)
	require.Contains(t, string(result), `"status":"completed"`)
	require.True(t, conn.closed == false)
}

func TestOpenAIWebTopicBodyAcceptsObjectFramesAndCamelCaseFields(t *testing.T) {
	encodedItem := "event: delta_encoding\ndata: \"v1\"\n\nevent: delta\ndata: {\"p\":\"/message/content/parts/0\",\"o\":\"append\",\"v\":\"OK\"}\n\n"
	streamFrame, err := json.Marshal(map[string]any{
		"type":    "message",
		"topicId": "topic",
		"payload": map[string]any{
			"type": "conversation-turn-stream",
			"payload": map[string]any{
				"type":         "stream-item",
				"streamItemId": "item-1",
				"encodedItem":  encodedItem,
			},
		},
	})
	require.NoError(t, err)
	doneFrame, err := json.Marshal(map[string]any{
		"type":    "message",
		"topicId": "topic",
		"payload": map[string]any{
			"type":    "conversation-turn-stream",
			"payload": map[string]any{"type": "stream_end"},
		},
	})
	require.NoError(t, err)
	conn := &webHandoffTestConn{frames: [][]byte{streamFrame, doneFrame}}
	body := &openAIWebTopicBody{ctx: context.Background(), conn: conn, topic: "topic", seen: map[string]struct{}{}}
	converted := NewOpenAIWebResponsesBody(body, "gpt-6-astra-wm")
	result, err := io.ReadAll(converted)
	require.NoError(t, err)
	require.Contains(t, string(result), `"delta":"OK"`)
	require.Contains(t, string(result), `"status":"completed"`)
}

type webBlockingTopicConn struct{}

func (webBlockingTopicConn) WriteJSON(context.Context, any) error { return nil }

func (webBlockingTopicConn) ReadMessage(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (webBlockingTopicConn) Ping(context.Context) error { return nil }

func (webBlockingTopicConn) Close() error { return nil }

func TestOpenAIWebTopicBodyStopsIdleReads(t *testing.T) {
	body := &openAIWebTopicBody{
		ctx:         context.Background(),
		conn:        webBlockingTopicConn{},
		topic:       "topic",
		readTimeout: 10 * time.Millisecond,
		seen:        map[string]struct{}{},
	}
	_, err := io.ReadAll(body)
	require.Error(t, err)
	require.Contains(t, err.Error(), "web websocket read timeout")
}

func TestOpenAIWebTopicBodyUsesArraySubscribeCommand(t *testing.T) {
	upstream := &webHandoffTestUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"websocket_url":"wss://ws.chatgpt.com/p6/ws/user/test?verify=opaque"}`)),
	}}}
	dialer := &webHandoffTestDialer{conn: &webHandoffTestConn{}}
	transport := NewOpenAIWebTransportFromUpstream(upstream, OpenAIWebTransportOptions{BaseURL: "https://chatgpt.com", SkipBootstrap: true})
	transport.wsDialer = dialer
	prefix := &strings.Builder{}
	_, err := transport.newOpenAIWebTopicBody(context.Background(), &Account{ID: 1}, "access-token", openAIWebConversationHandoff{TopicID: "conversation-turn-topic"}, bytes.NewBufferString(prefix.String()))
	require.NoError(t, err)
	require.Equal(t, "wss://ws.chatgpt.com/p6/ws/user/test?verify=opaque", dialer.url)
	require.Len(t, dialer.conn.writes, 1)
	command, ok := dialer.conn.writes[0].([]map[string]any)
	require.True(t, ok)
	require.Equal(t, 1, command[0]["id"])
	require.Equal(t, "subscribe", command[0]["command"].(map[string]any)["type"])
	require.Equal(t, "conversation-turn-topic", command[0]["command"].(map[string]any)["topic_id"])
}

func TestOpenAIWebTransportDoSwitchesHandoffToUserTopic(t *testing.T) {
	encodedItem := "event: delta_encoding\ndata: \"v1\"\n\nevent: delta\ndata: {\"p\":\"/message/content/parts/0\",\"o\":\"append\",\"v\":\"OK\"}\n\n"
	streamItem, err := json.Marshal([]any{map[string]any{
		"type":     "message",
		"topic_id": "topic",
		"payload": map[string]any{
			"type": "conversation-turn-stream",
			"payload": map[string]any{
				"type":           "stream-item",
				"stream_item_id": "item-1",
				"encoded_item":   encodedItem,
			},
		},
	}})
	require.NoError(t, err)
	streamDone, err := json.Marshal([]any{map[string]any{
		"type":     "message",
		"topic_id": "topic",
		"payload": map[string]any{
			"type":    "conversation-turn-stream",
			"payload": map[string]any{"type": "done"},
		},
	}})
	require.NoError(t, err)
	upstream := &webHandoffTestUpstream{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"conduit_token":"conduit"}`))},
		{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"prepare_token":"prepare"}`))},
		{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"token":"requirements"}`))},
		{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))},
		{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("event: delta_encoding\ndata: \"v1\"\n\ndata: {\"type\":\"resume_conversation_token\",\"conversation_id\":\"conv\"}\n\ndata: {\"type\":\"stream_handoff\",\"conversation_id\":\"conv\",\"turn_exchange_id\":\"turn\",\"options\":[{\"type\":\"resume_sse_endpoint\",\"topic_id\":\"topic\"},{\"type\":\"subscribe_ws_topic\",\"topic_id\":\"topic\"}]}\n\ndata: [DONE]\n\n"))},
		{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"websocket_url":"wss://ws.chatgpt.com/p6/ws/user/test"}`))},
	}}
	conn := &webHandoffTestConn{frames: [][]byte{streamItem, streamDone}}
	dialer := &webHandoffTestDialer{conn: conn}
	transport := NewOpenAIWebTransportFromUpstream(upstream, OpenAIWebTransportOptions{BaseURL: "https://chatgpt.com", SkipBootstrap: true})
	transport.wsDialer = dialer
	resp, err := transport.Do(context.Background(), &Account{ID: 1}, "access-token", OpenAIWebConversationOptions{Request: &apicompat.ChatCompletionsRequest{
		Model:    "gpt-5.6-sol-wm",
		Messages: []apicompat.ChatMessage{{Role: "user", Content: []byte(`"hello"`)}},
	}})
	require.NoError(t, err)
	converted, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Contains(t, string(converted), `"delta":"OK"`)
	require.Contains(t, string(converted), `"type":"response.completed"`)
	require.NotContains(t, string(converted), `"code":"upstream_empty_response"`)
	require.Len(t, dialer.conn.writes, 1)
}
