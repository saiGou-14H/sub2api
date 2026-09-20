package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	codexProbeBodyLimit   = 1 << 20
	codexProbeFrameLimit  = 256 << 10
	codexProbeHeaderLimit = 64 << 10
)

// ProbeCodexTicket is an isolated transport probe, excluded from user usage
// billing records. It can still consume upstream quota. It intentionally
// does not use forwarding request builders, scheduling, or user session state.
// Error messages contain only stable codes, never upstream errors or payloads.
func (s *OpenAIGatewayService) ProbeCodexTicket(ctx context.Context, accountID int64, model, proxyURL string) (result CodexTicketProbeResult, err error) {
	fail := func(code string) (CodexTicketProbeResult, error) {
		result.State = ""
		result.Completed = false
		result.ErrorCode = codexProbeRuntimeErrorCode(code)
		return result, errors.New(code)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !validCodexProbeProxy(proxyURL) {
		return fail("invalid_proxy")
	}
	if strings.TrimSpace(model) == "" {
		return fail("invalid_model")
	}
	if s == nil || s.accountRepo == nil || s.httpUpstream == nil {
		return fail("probe_unavailable")
	}
	account, e := s.accountRepo.GetByID(ctx, accountID)
	if e != nil || account == nil {
		return fail("account_unavailable")
	}
	if !account.CodexTurnStateEnabled() {
		return fail("account_disabled")
	}
	result.IdentityScope = CodexTicketIdentityScope(account)
	if result.IdentityScope == "" {
		return fail("identity_unavailable")
	}
	// Only accounts routed to the plugin are blocked: plugin transport cannot
	// guarantee this probe's dedicated HTTP/1 policy, even when unavailable.
	if s.pluginManager != nil && s.pluginManager.ShouldRouteOpenAIOAuth(account) {
		return fail("transport_unsupported")
	}
	token, _, e := s.getRequestCredential(ctx, nil, account)
	if e != nil || token == "" {
		return fail("credential_unavailable")
	}
	session := uuid.NewString()
	body, e := json.Marshal(map[string]any{
		"model": model, "stream": true, "store": false,
		"instructions": "Reply with OK only.",
		"input":        []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Reply OK."}}}},
	})
	if e != nil {
		return fail("request_invalid")
	}
	ctx = WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileCodexHarvest))
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, chatgptCodexURL, bytes.NewReader(body))
	if e != nil {
		return fail("request_invalid")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("ChatGPT-Account-ID", account.GetChatGPTAccountID())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	applyOpenAICodexProbeHeaders(req.Header)
	req.Header.Set("session_id", session)
	req.Header.Set("conversation_id", session)
	resp, e := s.httpUpstream.Do(req, proxyURL, account.ID, 4)
	if e != nil {
		return fail("transport_error")
	}
	if resp == nil || resp.Body == nil {
		return fail("response_invalid")
	}
	defer resp.Body.Close()
	result.HTTPStatus = resp.StatusCode
	if !codexProbeHeadersBounded(resp.Header) {
		return fail("headers_too_large")
	}
	result.RetryAfter = resp.Header.Get("Retry-After")
	if resp.StatusCode != http.StatusOK {
		return fail("http_status")
	}
	if code := readCodexProbeCompletion(resp.Body); code != "" {
		return fail(code)
	}
	states := resp.Header.Values("X-Codex-Turn-State")
	if len(states) != 1 || states[0] == "" {
		return fail("state_missing")
	}
	result.State = states[0]
	result.Completed = true
	return result, nil
}

func codexProbeRuntimeErrorCode(code string) string {
	switch code {
	case "transport_unsupported":
		return "transport_unsupported"
	case "invalid_proxy", "transport_error":
		return "proxy_unavailable"
	case "account_unavailable", "account_disabled", "identity_unavailable":
		return "identity_unresolved"
	case "credential_unavailable":
		return "token_unavailable"
	case "http_status":
		return "" // Let runtime classify HTTPStatus (including 401/429/503).
	default:
		return "stream_failed"
	}
}

func validCodexProbeProxy(raw string) bool {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h") || u.Hostname() == "" || u.Opaque != "" || u.Fragment != "" || u.RawQuery != "" || (u.Path != "" && u.Path != "/") {
		return false
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return !strings.HasSuffix(u.Host, ":")
}

func codexProbeHeadersBounded(h http.Header) bool {
	size := 0
	for key, values := range h {
		for _, value := range values {
			size += len(key) + len(value) + 4
			if size > codexProbeHeaderLimit {
				return false
			}
		}
	}
	return true
}

// Only a fully delimited completed event with matching response status is
// successful. EOF and [DONE] are not evidence of completion. Bounds apply to
// all bytes (including comments) and to each frame, not just individual lines.
func readCodexProbeCompletion(r io.Reader) string {
	limited := &io.LimitedReader{R: r, N: codexProbeBodyLimit + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), codexProbeFrameLimit+1)
	// Preserve CR bytes in accounting; ScanLines would silently remove them.
	scanner.Split(func(b []byte, eof bool) (int, []byte, error) {
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			return i + 1, b[:i+1], nil
		}
		if eof && len(b) > 0 {
			return len(b), b, nil
		}
		return 0, nil, nil
	})
	var data []byte
	frameBytes, total := 0, 0
	eventName := ""
	for scanner.Scan() {
		raw := scanner.Text()
		line := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
		total += len(raw)
		frameBytes += len(raw)
		if total > codexProbeBodyLimit {
			return "body_too_large"
		}
		if frameBytes > codexProbeFrameLimit {
			return "frame_too_large"
		}
		if line == "" {
			if !strings.HasSuffix(raw, "\n") {
				return "completion_missing"
			}
			if eventName == "error" || eventName == "response.failed" || eventName == "response.incomplete" {
				return "response_failed"
			}
			if len(data) != 0 {
				payload := bytes.TrimSpace(data)
				if bytes.Equal(payload, []byte("[DONE]")) {
					return "completion_missing"
				}
				var event struct {
					Type     string `json:"type"`
					Response struct {
						Status string `json:"status"`
					} `json:"response"`
				}
				if json.Unmarshal(payload, &event) != nil {
					return "sse_invalid"
				}
				switch event.Type {
				case "response.completed":
					if event.Response.Status != "completed" {
						return "response_failed"
					}
					return ""
				case "response.failed", "response.incomplete", "error":
					return "response_failed"
				}
			}
			data = data[:0]
			frameBytes = 0
			eventName = ""
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")...)
			data = append(data, '\n')
		}
	}
	if limited.N <= 0 {
		return "body_too_large"
	}
	if scanner.Err() != nil {
		return "sse_read_error"
	}
	return "completion_missing"
}
