package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type codexTicketReceiptContextKey struct{}

// The receipt is attached only by the actual injection path, never inferred
// from a client header. It is immutable for the lifetime of one upstream attempt.
func attachCodexTicketReceipt(req *http.Request, receipt *CodexTicketReceipt) {
	if req != nil && receipt != nil {
		*req = *req.WithContext(context.WithValue(req.Context(), codexTicketReceiptContextKey{}, receipt))
	}
}

func (s *OpenAIGatewayService) observeCodexTicketResponse(req *http.Request, resp *http.Response) {
	if s == nil || s.codexTicketRuntime == nil {
		return
	}
	watchCodexTicketResponse(req, resp, func(receipt CodexTicketReceipt, reason string) {
		// A completed response can outlive the caller's cancellation. Bound the
		// final bookkeeping without spawning an unowned background task.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(req.Context()), 200*time.Millisecond)
		defer cancel()
		s.codexTicketRuntime.InvalidateReceipt(ctx, receipt, reason)
	})
}

func watchCodexTicketResponse(req *http.Request, resp *http.Response, invalidate func(CodexTicketReceipt, string)) {
	if req == nil || resp == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 || resp.Body == nil || invalidate == nil {
		return
	}
	receipt, _ := req.Context().Value(codexTicketReceiptContextKey{}).(*CodexTicketReceipt)
	if receipt == nil {
		return
	}
	values := resp.Header.Values("X-Codex-Turn-State")
	signal312 := len(values) == 1 && codexTicketExperimental312(values[0])
	resp.Body = &codexTicketWatchdogBody{
		ReadCloser: resp.Body, model: receipt.Key.Model, signal312: signal312,
		trigger: func(reason string) { invalidate(*receipt, reason) },
	}
}

// A length is only an experimental signal. It is considered here only alongside
// a complete successful response, not on 429/error/truncated bodies.
func codexTicketExperimental312(state string) bool {
	if len(state) != 312 || !strings.HasPrefix(state, "gAAAAA") {
		return false
	}
	for _, c := range state {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '=') {
			return false
		}
	}
	return true
}

// This observer reads no bytes ahead of the existing consumer, mutates no body
// bytes or errors, and never retries a business request. Buffers are bounded
// independently of the total (possibly very long) response stream.
type codexTicketWatchdogBody struct {
	io.ReadCloser
	mu                                            sync.Mutex
	model                                         string
	signal312                                     bool
	trigger                                       func(string)
	mode                                          byte
	line, data                                    []byte
	eventName                                     string
	frameBytes                                    int
	discardFrame, discardLine, overflow, finished bool
}

func (b *codexTicketWatchdogBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil && err != io.EOF {
		b.finished = true
		b.line, b.data = nil, nil
		return n, err
	}
	if !b.finished {
		if n > 0 {
			b.consume(p[:n])
		}
		if err == io.EOF {
			b.finish()
		}
	}
	return n, err
}

func (b *codexTicketWatchdogBody) Close() error {
	// Close the underlying body first so it can unblock a concurrent Read.
	err := b.ReadCloser.Close()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.finish()
	return err
}

func (b *codexTicketWatchdogBody) consume(p []byte) {
	if b.mode == 0 {
		trimmed := bytes.TrimSpace(p)
		if len(trimmed) == 0 {
			return
		}
		b.mode = 's'
		if trimmed[0] == '{' {
			b.mode = 'j'
		}
	}
	if b.mode == 'j' {
		if !b.overflow && len(b.data)+len(p) <= codexProbeBodyLimit {
			b.data = append(b.data, p...)
		} else {
			b.overflow = true
			b.data = nil
		}
		return
	}
	for len(p) > 0 && !b.finished {
		end := bytes.IndexByte(p, '\n')
		part := p
		if end >= 0 {
			part = p[:end+1]
		}
		if !b.discardFrame {
			b.frameBytes += len(part)
			if b.frameBytes > codexProbeFrameLimit {
				b.discardFrame = true
				b.data = nil
			}
		}
		if !b.discardLine {
			if len(b.line)+len(part) > codexProbeFrameLimit {
				b.discardLine = true
				b.line = nil
			} else {
				b.line = append(b.line, part...)
			}
		}
		if end < 0 {
			return
		}
		if !b.discardLine {
			b.consumeLine(b.line)
		}
		b.line = b.line[:0]
		b.discardLine = false
		p = p[end+1:]
	}
}

func (b *codexTicketWatchdogBody) consumeLine(raw []byte) {
	line := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if line == "" {
		b.flushFrame()
		b.frameBytes = 0
		b.discardFrame = false
		b.eventName = ""
		b.data = b.data[:0]
		return
	}
	if b.discardFrame {
		return
	}
	if strings.HasPrefix(line, "event:") {
		b.eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
	}
	if strings.HasPrefix(line, "data:") {
		b.data = append(b.data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")...)
		b.data = append(b.data, '\n')
	}
}

func (b *codexTicketWatchdogBody) finish() {
	if b.finished {
		return
	}
	if b.mode == 'j' && !b.overflow {
		b.inspectCompletion(b.data, false)
	} else if b.mode == 's' {
		// Existing SSE consumers may stop after a complete data line without
		// consuming the trailing delimiter. Parse only bytes already observed.
		if len(b.line) > 0 && !b.discardLine {
			b.consumeLine(b.line)
		}
		b.flushFrame()
	}
	b.finished = true
	b.line, b.data = nil, nil
}

func (b *codexTicketWatchdogBody) flushFrame() {
	if b.finished || b.discardFrame || (b.eventName != "" && b.eventName != "response.completed") {
		return
	}
	b.inspectCompletion(b.data, true)
}

func (b *codexTicketWatchdogBody) inspectCompletion(raw []byte, sse bool) {
	var event struct {
		Type     string `json:"type"`
		Status   string `json:"status"`
		Model    string `json:"model"`
		Response struct {
			Status string `json:"status"`
			Model  string `json:"model"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &event) != nil {
		return
	}
	model, status := event.Model, event.Status
	if sse || event.Type != "" {
		if event.Type != "response.completed" {
			return
		}
		model, status = event.Response.Model, event.Response.Status
	}
	if status != "completed" || model == "" {
		return
	}
	b.finished = true
	b.line, b.data = nil, nil
	if model != b.model {
		b.trigger("model_mismatch")
	} else if b.signal312 {
		b.trigger("state_312")
	}
}
