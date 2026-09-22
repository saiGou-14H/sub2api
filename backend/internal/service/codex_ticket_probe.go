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
func (s *OpenAIGatewayService) ProbeCodexTicket(ctx context.Context, accountID int64, model, proxyURL string) (CodexTicketProbeResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	task, ok := ctx.Value(codexTicketProbeTaskKey{}).(codexTicketProbeTask)
	fail := func(code string) (CodexTicketProbeResult, error) {
		return CodexTicketProbeResult{ErrorCode: code}, errors.New(code)
	}
	if !validCodexProbeProxy(proxyURL) {
		return fail("proxy_unavailable")
	}
	if !ok || task.VerificationGuard == nil || task.Key.AccountID != accountID || task.Key.Model != model || !codexTicketModelEnabled(task.Settings, model) {
		return fail("verification_failed")
	}
	if s == nil || s.accountRepo == nil {
		return fail("identity_unresolved")
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil || !CodexTicketAccountSupported(account) || account.ID != accountID || !account.CodexTurnStateEnabled() || CodexTicketIdentityScope(account) != task.Key.IdentityScope {
		return fail("identity_unresolved")
	}
	var policy, route string
	if s.codexTicketRuntime != nil {
		account, policy, route, err = s.codexTicketRuntime.resolveCodexTicketPolicy(ctx, account, time.Now())
	} else {
		account, policy, route, err = resolveCodexTicketAccountPolicy(ctx, nil, account, time.Now())
	}
	if err != nil || policy != task.Key.PolicyScope {
		return fail("proxy_unavailable")
	}
	cfg, err := CodexTicketAccountSettings(task.Settings, account)
	if err != nil || cfg.TargetLength != task.Settings.TargetLength {
		return fail("verification_failed")
	}
	capture, err := s.codexTicketProbeStage(ctx, account, model, proxyURL, "", nil)
	capture.PolicyScope = policy
	if err != nil {
		return capture, err
	}
	if validCodexProbeState(capture.State, 312) {
		capture.State = ""
		capture.Cookies = nil
		capture.Completed = false
		capture.ErrorCode = "state_312"
		return capture, errors.New("state_312")
	}
	if !validCodexProbeState(capture.State, cfg.TargetLength) {
		capture.State = ""
		capture.Cookies = nil
		capture.Completed = false
		capture.ErrorCode = "verification_failed"
		return capture, errors.New("verification_failed")
	}
	if CodexTicketCookiesRequired(cfg) && !CodexTicketCookiesValid(capture.Cookies, time.Now()) {
		capture.State = ""
		capture.Cookies = nil
		capture.Completed = false
		capture.ErrorCode = "cookie_missing"
		return capture, errors.New("cookie_missing")
	}
	candidate := capture.State
	fresh, businessRoute, err := task.VerificationGuard(ctx, true)
	if err != nil || !CodexTicketAccountSupported(fresh) || fresh.ID != accountID || !fresh.CodexTurnStateEnabled() || CodexTicketIdentityScope(fresh) != task.Key.IdentityScope || CodexTicketPolicyScope(fresh, time.Now()) != policy || businessRoute != route {
		return fail("verification_failed")
	}
	verification, err := s.codexTicketProbeStage(ctx, fresh, model, businessRoute, candidate, capture.Cookies)
	verification.IdentityScope = task.Key.IdentityScope
	verification.PolicyScope = policy
	if err != nil {
		return verification, err
	}
	if validCodexProbeState(verification.State, 312) {
		verification.State = ""
		verification.Cookies = nil
		verification.Completed = false
		verification.ErrorCode = "state_312"
		return verification, errors.New("state_312")
	}
	confirmed, confirmedRoute, err := task.VerificationGuard(ctx, false)
	if err != nil || !CodexTicketAccountSupported(confirmed) || confirmed.ID != accountID || !confirmed.CodexTurnStateEnabled() || CodexTicketIdentityScope(confirmed) != task.Key.IdentityScope || CodexTicketPolicyScope(confirmed, time.Now()) != policy || confirmedRoute != businessRoute {
		return fail("verification_failed")
	}
	capture.Verified = true
	capture.VerifiedAt = time.Now()
	capture.VerificationModel = verification.ActualModel
	// Verification proves the original bundle on the business route. Cookies
	// returned by verification belong to that response, never to candidate.
	if (CodexTicketCookiesRequired(cfg) || len(capture.Cookies) > 0) && !CodexTicketCookiesValid(capture.Cookies, time.Now()) {
		return fail("cookie_missing")
	}
	capture.State = candidate
	return capture, nil
}

func validCodexProbeState(state string, length int) bool {
	if len(state) != length || !strings.HasPrefix(state, "gAAAAA") {
		return false
	}
	for _, ch := range state {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '=') {
			return false
		}
	}
	return true
}

func (s *OpenAIGatewayService) codexTicketProbeStage(ctx context.Context, account *Account, model, proxyURL, injectedState string, injectedCookies []CodexTicketCookie) (result CodexTicketProbeResult, err error) {
	fail := func(code string) (CodexTicketProbeResult, error) {
		result.State = ""
		result.Cookies = nil
		result.Completed = false
		result.ErrorCode = codexProbeRuntimeErrorCode(code)
		return result, errors.New(code)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if proxyURL != "" && !validCodexProbeProxy(proxyURL) {
		return fail("invalid_proxy")
	}
	if strings.TrimSpace(model) == "" {
		return fail("invalid_model")
	}
	if s == nil || s.accountRepo == nil || s.httpUpstream == nil {
		return fail("probe_unavailable")
	}
	if !CodexTicketAccountSupported(account) {
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
	if injectedState != "" {
		req.Header.Set("X-Codex-Turn-State", injectedState)
	}
	// Validate and render once: a present pair must never silently turn into
	// a state-only verification when it expires between two clock reads.
	if len(injectedCookies) > 0 {
		cookieHeader := CodexTicketCookieHeader(injectedCookies, time.Now())
		if cookieHeader == "" {
			return fail("cookie_missing")
		}
		req.Header.Set("Cookie", cookieHeader)
	}
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
	// Snapshot the candidate at receipt of headers. Verification responses are
	// signals only: their cookies must never enter or renew the captured bundle.
	result.CapturedAt = time.Now()
	states := append([]string(nil), resp.Header.Values("X-Codex-Turn-State")...)
	if injectedState == "" {
		result.Cookies = CodexTicketCookiesFromResponse(resp, req.URL, result.CapturedAt)
	}
	if code := readCodexProbeCompletion(resp.Body, model); code != "" {
		return fail(code)
	}
	if len(states) > 1 {
		return fail("state_missing")
	}
	if len(states) == 1 {
		result.State = states[0]
	}
	if injectedState == "" && result.State == "" {
		return fail("state_missing")
	}
	result.ActualModel = model
	result.Completed = true
	return result, nil
}

func codexProbeRuntimeErrorCode(code string) string {
	switch code {
	case "model_mismatch", "verification_failed", "state_312", "cookie_missing":
		return code
	case "transport_unsupported":
		return "transport_unsupported"
	case "invalid_proxy", "transport_error":
		return "proxy_unavailable"
	case "account_unavailable", "account_disabled", "identity_unavailable":
		return "identity_unresolved"
	case "credential_unavailable":
		return "token_unavailable"
	case "quota_limited":
		return "quota_limited"
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
func readCodexProbeCompletion(r io.Reader, expectedModel ...string) string {
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
			failedEvent := eventName == "error" || eventName == "response.failed" || eventName == "response.incomplete"
			if len(data) != 0 {
				payload := bytes.TrimSpace(data)
				if bytes.Equal(payload, []byte("[DONE]")) {
					return "completion_missing"
				}
				var event struct {
					Type  string `json:"type"`
					Code  string `json:"code"`
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
					Response struct {
						Model  string `json:"model"`
						Status string `json:"status"`
						Error  struct {
							Code string `json:"code"`
						} `json:"error"`
					} `json:"response"`
				}
				if json.Unmarshal(payload, &event) != nil {
					return "sse_invalid"
				}
				if failedEvent || event.Type == "response.failed" || event.Type == "response.incomplete" || event.Type == "error" {
					for _, code := range []string{event.Response.Error.Code, event.Error.Code, event.Code} {
						if code == "rate_limit_exceeded" || code == "insufficient_quota" {
							return "quota_limited"
						}
					}
					return "response_failed"
				}
				switch event.Type {
				case "response.completed":
					if event.Response.Status != "completed" {
						return "response_failed"
					}
					if len(expectedModel) > 0 && (event.Response.Model == "" || event.Response.Model != expectedModel[0]) {
						return "model_mismatch"
					}
					return ""
				}
			}
			if failedEvent {
				return "response_failed"
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
