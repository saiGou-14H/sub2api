package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIPrismNativeProgressIsSeparateFromClientTools(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		c, recorder := prismGatewayTestContext("/v1/responses", "")
		if enabled {
			c.Request.Header.Set("X-Prism-Progress", "true")
		}
		ctx := withPrismStreamingProgress(context.Background(), c, true)
		callback := prismProgressCallback(ctx)
		require.NotNil(t, callback)
		progress := OpenAIPrismProgress{TranscriptCursor: 4, ToolCalls: []OpenAIPrismNativeTool{{Name: "exec_command", CallID: "native-call-test"}}}
		callback(progress)
		callback(progress)
		callback(OpenAIPrismProgress{}) // A heartbeat must not clear observed tools.
		if enabled {
			require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: prism.tool_progress"))
			require.Contains(t, recorder.Body.String(), `"execution":"upstream"`)
			require.Contains(t, recorder.Body.String(), "exec_command")
		} else {
			require.Equal(t, ": prism task pending\n\n", recorder.Body.String())
		}
		require.NotContains(t, recorder.Body.String(), "function_call")
		require.NotContains(t, recorder.Body.String(), "tool_use")
		require.True(t, writePrismStreamError(c, errors.New("poll failed"), "responses"))
		require.Contains(t, recorder.Body.String(), "event: response.failed")
		require.True(t, strings.HasSuffix(recorder.Body.String(), "data: [DONE]\n\n"))
		for _, line := range strings.Split(recorder.Body.String(), "\n") {
			if !strings.HasPrefix(line, "data: ") || !strings.Contains(line, `"type":"response.failed"`) {
				continue
			}
			var event struct {
				Response struct {
					ID        string `json:"id"`
					CreatedAt int64  `json:"created_at"`
					Output    []any  `json:"output"`
					Error     struct {
						Code string `json:"code"`
					} `json:"error"`
				} `json:"response"`
			}
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
			require.NotEmpty(t, event.Response.ID)
			require.Positive(t, event.Response.CreatedAt)
			require.NotNil(t, event.Response.Output)
			require.Equal(t, "upstream_error", event.Response.Error.Code)
		}
	}
	c, _ := prismGatewayTestContext("/v1/responses", "")
	require.Nil(t, prismProgressCallback(withPrismStreamingProgress(context.Background(), c, false)))
}

func TestOpenAIPrismFileSyncPublication(t *testing.T) {
	state := OpenAIPrismSessionState{DeltaSync: OpenAIPrismDeltaSync{Status: "partial", Files: []OpenAIPrismDeltaFileSync{
		{Path: "main.tex", Status: "synced"},
		{Path: "figure.png", Status: "unsynced", Reason: "unsupported_binary"},
	}}}
	for _, protocol := range []string{"responses", "chat/completions", "messages"} {
		for _, stream := range []bool{false, true} {
			for _, optIn := range []bool{false, true} {
				c, recorder := prismGatewayTestContext("/v1/"+protocol, "")
				if optIn {
					c.Request.Header.Set("X-Prism-Progress", "true")
				}
				publishPrismFileSync(c, state, stream)
				if !stream {
					require.Equal(t, "partial", recorder.Header().Get("X-Prism-Sync-Status"))
					require.Empty(t, recorder.Body.String())
					require.False(t, c.Writer.Written(), "metadata must not commit a non-stream response")
					continue
				}
				require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
				if optIn {
					require.Contains(t, recorder.Body.String(), "event: prism.file_sync\n")
					require.Contains(t, recorder.Body.String(), `"path":"main.tex"`)
					require.Contains(t, recorder.Body.String(), `"reason":"unsupported_binary"`)
				} else {
					require.Equal(t, ": prism file sync partial\n\n", recorder.Body.String())
				}
				require.NotContains(t, recorder.Body.String(), "function_call")
				require.NotContains(t, recorder.Body.String(), "tool_use")
			}
		}
	}
}

func TestOpenAIPrismFileSyncLegacyStateDoesNotClaimSuccess(t *testing.T) {
	for _, test := range []struct {
		delta  string
		status string
	}{{"", "not_required"}, {"[]", "not_required"}, {"null", "not_required"}, {`[{"filePath":"main.tex"}]`, "unsynced"}} {
		result := prismPublicDeltaSync(OpenAIPrismSessionState{DeltaFiles: json.RawMessage(test.delta)})
		require.Equal(t, test.status, result.Status)
		require.NotNil(t, result.Files)
	}
}
