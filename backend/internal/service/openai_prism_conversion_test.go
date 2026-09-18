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

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type prismDelayedFirstReadBody struct {
	io.ReadCloser
	started bool
}

func (b *prismDelayedFirstReadBody) Read(p []byte) (int, error) {
	if !b.started {
		b.started = true
		// Ensure an incorrectly inherited, already expired native timer fires
		// before the local response reader can win the select by chance.
		time.Sleep(5 * time.Millisecond)
	}
	return b.ReadCloser.Read(p)
}

func TestOpenAIPrismCompletedStreamIgnoresExpiredNativeFirstOutputDeadline(t *testing.T) {
	for _, progressStarted := range []bool{false, true} {
		t.Run(fmt.Sprintf("progress=%v", progressStarted), func(t *testing.T) {
			s := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
				OpenAIFirstOutputTimeoutSeconds: 1,
				MaxLineSize:                     defaultMaxLineSize,
			}}}
			c, recorder := prismGatewayTestContext("/v1/responses", "")
			if progressStarted {
				progressCtx := withPrismStreamingProgress(c.Request.Context(), c, true)
				prismProgressCallback(progressCtx)(OpenAIPrismProgress{})
			}
			resp := prismResponsesSSE([]byte(`{"id":"resp_completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"completed result"}]}]}`), "req_completed", "", "", OpenAIPrismDefaultModel)
			resp.Body = &prismDelayedFirstReadBody{ReadCloser: resp.Body}
			defer resp.Body.Close()
			result, err := s.handleStreamingResponseWithReasoning(c.Request.Context(), resp, c, prismGatewayTestAccount(), time.Now().Add(-2*time.Minute), OpenAIPrismDefaultModel, OpenAIPrismDefaultModel, "")
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Contains(t, recorder.Body.String(), "completed result")
			require.Contains(t, recorder.Body.String(), "event: response.completed")
			require.NotContains(t, recorder.Body.String(), "first_output_timeout")
		})
	}
}

func TestOpenAIPrismConversionFailureNeverEscapesAsFailover(t *testing.T) {
	for _, tc := range []struct {
		protocol string
		write    func(*gin.Context, error)
	}{
		{protocol: "responses", write: writePrismResponsesError},
		{protocol: "chat", write: writePrismChatError},
		{protocol: "anthropic", write: writePrismAnthropicError},
	} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", tc.protocol, streaming), func(t *testing.T) {
				c, recorder := prismGatewayTestContext("/v1/"+tc.protocol, "")
				if streaming {
					ctx := withPrismStreamingProgress(context.Background(), c, true)
					prismProgressCallback(ctx)(OpenAIPrismProgress{})
				}
				cause := fmt.Errorf("local adapter: %w", &UpstreamFailoverError{
					StatusCode: http.StatusGatewayTimeout, SafeToFailoverAfterWrite: true,
					ResponseBody: []byte(`{"error":{"message":"private-adapter-detail"}}`),
				})
				err := finishPrismConversionError(c, cause, tc.write)
				var started *OpenAIPrismStartedError
				require.ErrorAs(t, err, &started)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover), "a completed turn must never be replayed through errors.As")
				require.True(t, IsResponseCommitted(c), "the outer handler must not append a second error")
				require.Contains(t, recorder.Body.String(), "Prism response conversion failed")
				require.NotContains(t, recorder.Body.String(), "private-adapter-detail")
				if !streaming {
					require.Equal(t, http.StatusBadGateway, recorder.Code)
					require.True(t, json.Valid(recorder.Body.Bytes()))
					return
				}
				require.Equal(t, http.StatusOK, recorder.Code)
				switch tc.protocol {
				case "responses":
					require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.failed"))
				case "chat":
					require.NotContains(t, recorder.Body.String(), "event:")
				case "anthropic":
					require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: error"))
					require.NotContains(t, recorder.Body.String(), "[DONE]")
				}
			})
		}
	}
}

func TestOpenAIPrismAdapterFailureCannotReplayCompletedTurn(t *testing.T) {
	for _, protocol := range []string{"responses", "chat", "anthropic"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", protocol, stream), func(t *testing.T) {
				s := &OpenAIGatewayService{}
				c, recorder := prismGatewayTestContext("/v1/"+protocol, "")
				a := prismGatewayTestAccount()
				// Exercise the actual adapter's retry classification, as would
				// happen if a locally transformed completed turn becomes invalid.
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_conversion\",\"status\":\"failed\",\"output\":[],\"error\":{\"code\":\"server_error\",\"message\":\"server overloaded\"}}}\n\n"))}
				defer resp.Body.Close()
				var err error
				var write func(*gin.Context, error)
				switch protocol {
				case "responses":
					write = writePrismResponsesError
					if stream {
						_, err = s.handleStreamingResponseWithReasoning(c.Request.Context(), resp, c, a, time.Now(), OpenAIPrismDefaultModel, OpenAIPrismDefaultModel, "")
					} else {
						_, err = s.handleNonStreamingResponse(c.Request.Context(), resp, c, a, OpenAIPrismDefaultModel, OpenAIPrismDefaultModel)
					}
				case "chat":
					write = writePrismChatError
					if stream {
						_, err = s.handleChatStreamingResponse(resp, c, a, OpenAIPrismDefaultModel, OpenAIPrismDefaultModel, OpenAIPrismDefaultModel, time.Now(), 0)
					} else {
						_, err = s.handleChatBufferedStreamingResponse(resp, c, a, OpenAIPrismDefaultModel, OpenAIPrismDefaultModel, OpenAIPrismDefaultModel, time.Now())
					}
				case "anthropic":
					write = writePrismAnthropicError
					if stream {
						_, err = s.handleAnthropicStreamingResponse(resp, c, a, OpenAIPrismDefaultModel, OpenAIPrismDefaultModel, OpenAIPrismDefaultModel, time.Now())
					} else {
						_, err = s.handleAnthropicBufferedStreamingResponse(resp, c, a, OpenAIPrismDefaultModel, OpenAIPrismDefaultModel, OpenAIPrismDefaultModel, time.Now())
					}
				}
				var failover *UpstreamFailoverError
				require.ErrorAs(t, err, &failover, "the fixture must exercise an adapter failover")
				err = finishPrismConversionError(c, err, write)
				require.False(t, errors.As(err, &failover))
				require.True(t, IsResponseCommitted(c))
				require.Contains(t, recorder.Body.String(), "Prism response conversion failed")
			})
		}
	}
}
