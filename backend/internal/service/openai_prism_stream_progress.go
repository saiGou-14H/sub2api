package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type prismProgressContextKey struct{}

func prismProgressCallback(ctx context.Context) func(OpenAIPrismProgress) {
	callback, _ := ctx.Value(prismProgressContextKey{}).(func(OpenAIPrismProgress))
	return callback
}

// Native tool observations are a separate opt-in event namespace. They never
// become function_call/tool_use, which would ask the client to execute again.
// SSE comments keep ordinary streaming clients connected while Prism polls.
func withPrismStreamingProgress(ctx context.Context, c *gin.Context, stream bool) context.Context {
	if !stream || c == nil {
		return ctx
	}
	includeProgress := strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Prism-Progress")), "true")
	var lastPayload string
	var lastHeartbeat time.Time
	callback := func(progress OpenAIPrismProgress) {
		if ctx.Err() != nil {
			return
		}
		encoded, err := json.Marshal(map[string]any{"type": "prism.tool_progress", "execution": "upstream", "progress": progress})
		if err != nil {
			return
		}
		observed := progress.ToolCalls != nil || progress.ReasoningSummaries != nil || progress.TranscriptCursor != 0 || progress.LineCount != 0
		changed := includeProgress && observed && string(encoded) != lastPayload
		if !changed && !lastHeartbeat.IsZero() && time.Since(lastHeartbeat) < 15*time.Second {
			return
		}
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("X-Accel-Buffering", "no")
		MarkResponseCommitted(c)
		if changed {
			_, _ = fmt.Fprintf(c.Writer, "event: prism.tool_progress\ndata: %s\n\n", encoded)
			lastPayload = string(encoded)
		} else {
			_, _ = fmt.Fprint(c.Writer, ": prism task pending\n\n")
		}
		lastHeartbeat = time.Now()
		c.Writer.Flush()
	}
	return context.WithValue(ctx, prismProgressContextKey{}, callback)
}

// Empty legacy state is not evidence that an existing delta was synchronized.
// The public result carries only the typed per-file outcome, never document
// contents, credentials or the private Yjs state.
func prismPublicDeltaSync(state OpenAIPrismSessionState) OpenAIPrismDeltaSync {
	result := state.DeltaSync
	switch result.Status {
	case "synced", "partial", "unsynced", "not_required":
	default:
		result.Status = "unsynced"
		raw := bytes.TrimSpace(state.DeltaFiles)
		if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("[]")) {
			result.Status = "not_required"
		}
	}
	if result.Files == nil {
		result.Files = []OpenAIPrismDeltaFileSync{}
	}
	return result
}

// All three client protocols share the completed transport path. Native file
// synchronization is diagnostic metadata, never a client-executable tool call.
func publishPrismFileSync(c *gin.Context, state OpenAIPrismSessionState, stream bool) {
	if c == nil {
		return
	}
	result := prismPublicDeltaSync(state)
	if !stream {
		c.Header("X-Prism-Sync-Status", result.Status)
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	MarkResponseCommitted(c)
	if strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Prism-Progress")), "true") {
		encoded, err := json.Marshal(map[string]any{"type": "prism.file_sync", "sync": result})
		if err != nil {
			return
		}
		_, _ = fmt.Fprintf(c.Writer, "event: prism.file_sync\ndata: %s\n\n", encoded)
	} else {
		_, _ = fmt.Fprintf(c.Writer, ": prism file sync %s\n\n", result.Status)
	}
	c.Writer.Flush()
}

func writePrismStreamError(c *gin.Context, err error, protocol string) bool {
	if c == nil || !c.Writer.Written() || !strings.Contains(c.Writer.Header().Get("Content-Type"), "text/event-stream") {
		return false
	}
	MarkResponseCommitted(c)
	errorValue := map[string]any{"type": "upstream_error", "message": prismGatewayErrorMessage(err)}
	event := "error"
	payload := map[string]any{"error": errorValue}
	if protocol == "responses" {
		event = "response.failed"
		// Match the gateway's terminal Responses schema. The native progress
		// namespace has no Responses sequence, so do not invent a sequence number.
		payload = map[string]any{"type": event, "response": map[string]any{
			"id":     "resp_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			"object": "response", "created_at": time.Now().Unix(), "status": "failed", "output": []any{},
			"error": map[string]any{"code": "upstream_error", "message": prismGatewayErrorMessage(err)},
		}}
	} else if protocol == "anthropic" {
		payload["type"] = "error"
	}
	encoded, _ := json.Marshal(payload)
	if protocol == "chat" {
		_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", encoded)
	} else {
		_, _ = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, encoded)
	}
	if protocol != "anthropic" {
		_, _ = fmt.Fprint(c.Writer, "data: [DONE]\n\n")
	}
	c.Writer.Flush()
	return true
}
