//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type codexWatchdogChunkReader struct {
	input string
	size  int
}

func (r *codexWatchdogChunkReader) Read(p []byte) (int, error) {
	if r.input == "" {
		return 0, io.EOF
	}
	n := len(p)
	if n > r.size {
		n = r.size
	}
	if n > len(r.input) {
		n = len(r.input)
	}
	copy(p, r.input[:n])
	r.input = r.input[n:]
	return n, nil
}
func (r *codexWatchdogChunkReader) Close() error { return nil }

func TestCodexTicketWatchdogTransparentCompletionSignals(t *testing.T) {
	matched := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"target\"}}\n\n"
	mismatch := strings.ReplaceAll(matched, "target", "different")
	state312 := "gAAAAA" + strings.Repeat("a", 306)
	for _, tc := range []struct{ name, body, header, want string }{
		{"matching", matched, "", ""},
		{"different_model", mismatch, "", "model_mismatch"},
		{"312", matched, state312, "state_312"},
		{"mismatch_precedes_312", mismatch, state312, "model_mismatch"},
		{"failed", "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"model\":\"different\"}}\n\n", state312, ""},
		{"incomplete", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"incomplete\",\"model\":\"different\"}}\n\n", state312, ""},
		{"event_conflict", strings.Replace(mismatch, "event: response.completed", "event: response.failed", 1), state312, ""},
		{"missing_model", strings.Replace(matched, ",\"model\":\"target\"", "", 1), state312, ""},
		{"truncated", "data: {\"type\":\"response.completed\",\"response\":", state312, ""},
		{"done_only", "data: [DONE]\n\n", state312, ""},
		{"invalid_header", matched, strings.Repeat("a", 312), ""},
		{"unsafe_header", matched, "gAAAAA" + strings.Repeat("a", 305) + "\n", ""},
		{"json", `{"status":"completed","model":"different"}`, "", "model_mismatch"},
		{"json_failed", `{"status":"failed","model":"different"}`, state312, ""},
		{"json_invalid", `{"status":"completed","model":"different"}tail`, state312, ""},
		{"comment_then_match", ":keepalive\r\n\r\n" + matched, "", ""},
		{"oversized_frame", "data: " + strings.Repeat("x", codexProbeFrameLimit+1) + "\n\n", state312, ""},
		{"oversized_then_valid", "data: " + strings.Repeat("x", codexProbeFrameLimit+1) + "\n\n" + mismatch, "", "model_mismatch"},
		{"oversized_json", `{"model":"different","status":"completed","padding":"` + strings.Repeat("x", codexProbeBodyLimit) + `"}`, state312, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, size := range []int{1, 17, 4096} {
				var reasons []string
				req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				receipt := &CodexTicketReceipt{Key: CodexTicketKey{AccountID: 1, Model: "target"}, state: "private-a"}
				attachCodexTicketReceipt(req, receipt)
				resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: &codexWatchdogChunkReader{input: tc.body, size: size}}
				if tc.header != "" {
					resp.Header.Set("X-Codex-Turn-State", tc.header)
				}
				watchCodexTicketResponse(req, resp, func(got CodexTicketReceipt, reason string) {
					require.Equal(t, receipt.Key, got.Key)
					require.Equal(t, "private-a", got.state)
					reasons = append(reasons, reason)
				})
				raw, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.Equal(t, tc.body, string(raw))
				require.NoError(t, resp.Body.Close())
				if tc.want == "" {
					require.Empty(t, reasons)
				} else {
					require.Equal(t, []string{tc.want}, reasons)
				}
			}
		})
	}
}

func TestCodexTicketWatchdogRequiresActualReceiptAndSuccess(t *testing.T) {
	for _, status := range []int{200, 401, 403, 429, 500} {
		for _, receipt := range []bool{false, true} {
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			req.Header.Set("X-Codex-Turn-State", "client-provided")
			if receipt {
				attachCodexTicketReceipt(req, &CodexTicketReceipt{Key: CodexTicketKey{AccountID: 2, Model: "target"}})
			}
			body := &codexWatchdogChunkReader{input: `{"status":"completed","model":"different"}`, size: 256}
			resp := &http.Response{StatusCode: status, Header: make(http.Header), Body: body}
			calls := 0
			watchCodexTicketResponse(req, resp, func(CodexTicketReceipt, string) { calls++ })
			_, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			want := 0
			if receipt && status == 200 {
				want = 1
			}
			require.Equal(t, want, calls)
			if want == 0 {
				require.Same(t, body, resp.Body)
			}
		}
	}
}

func TestCodexTicketWatchdogOnlyObservedBytesOnClose(t *testing.T) {
	line := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"different\"}}\n"
	calls := 0
	b := &codexTicketWatchdogBody{ReadCloser: &codexWatchdogChunkReader{input: line + "\nnever-read", size: len(line)}, model: "target", trigger: func(string) { calls++ }}
	p := make([]byte, len(line))
	n, err := b.Read(p)
	require.NoError(t, err)
	require.Equal(t, line, string(p[:n]))
	require.Zero(t, calls)
	require.NoError(t, b.Close())
	require.Equal(t, 1, calls)
	require.NoError(t, b.Close())
	require.Equal(t, 1, calls)
}

type codexWatchdogBrokenBody struct {
	sent    bool
	payload string
}

func (b *codexWatchdogBrokenBody) Read(p []byte) (int, error) {
	if b.sent {
		return 0, io.EOF
	}
	b.sent = true
	if b.payload == "" {
		b.payload = "partial"
	}
	return copy(p, b.payload), io.ErrUnexpectedEOF
}
func (b *codexWatchdogBrokenBody) Close() error { return io.ErrClosedPipe }
func TestCodexTicketWatchdogPreservesConsumerErrors(t *testing.T) {
	calls := 0
	b := &codexTicketWatchdogBody{ReadCloser: &codexWatchdogBrokenBody{}, model: "target", signal312: true, trigger: func(string) { calls++ }}
	p := make([]byte, 16)
	n, err := b.Read(p)
	require.Equal(t, "partial", string(p[:n]))
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.ErrorIs(t, b.Close(), io.ErrClosedPipe)
	require.Zero(t, calls)
}

func TestCodexTicketWatchdogCompleteLookingBytesWithReadErrorDoNotInvalidate(t *testing.T) {
	for _, payload := range []string{
		`{"status":"completed","model":"different"}`,
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"different\"}}\n\n",
	} {
		calls := 0
		b := &codexTicketWatchdogBody{ReadCloser: &codexWatchdogBrokenBody{payload: payload}, model: "target", signal312: true, trigger: func(string) { calls++ }}
		p := make([]byte, 1024)
		n, err := b.Read(p)
		require.Equal(t, payload, string(p[:n]))
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		require.ErrorIs(t, b.Close(), io.ErrClosedPipe)
		require.Zero(t, calls)
	}
}

func TestCodexTicketReceiptContextIsPerRequest(t *testing.T) {
	a := httptest.NewRequest(http.MethodPost, "/", nil)
	b := httptest.NewRequest(http.MethodPost, "/", nil)
	receipt := &CodexTicketReceipt{Key: CodexTicketKey{AccountID: 1, Model: "target"}, state: "private-a"}
	attachCodexTicketReceipt(a, receipt)
	require.Same(t, receipt, a.Context().Value(codexTicketReceiptContextKey{}))
	require.Nil(t, b.Context().Value(codexTicketReceiptContextKey{}))
	ctx, cancel := context.WithCancel(a.Context())
	cancel()
	require.True(t, errors.Is(ctx.Err(), context.Canceled))
	require.Same(t, receipt, ctx.Value(codexTicketReceiptContextKey{}))
}
