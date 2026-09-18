package service

// Prism transport for the browser-backed Prism/Codex API.  Prism does not
// expose an OpenAI Responses HTTP endpoint: a turn is started with a JSON
// request and then observed through a status endpoint.  This adapter keeps
// that protocol isolated and returns a Responses-shaped SSE stream so the
// existing gateway response handlers can consume it.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/google/uuid"
)

const (
	OpenAIPrismDefaultBaseURL          = "https://prism.openai.com"
	OpenAIPrismStartPath               = "/api/llm/response_with_tools_start"
	OpenAIPrismStatusPath              = "/api/llm/response_with_tools_status"
	OpenAIPrismProjectPath             = "/api/projects"
	OpenAIPrismProjectAccessPath       = "/api/project-access"
	OpenAIPrismBackendNewPath          = "/api/backend/1/new"
	OpenAIPrismConversationHistoryPath = "/api/codex/conversation-history"
	OpenAIPrismDefaultPollInterval     = 500 * time.Millisecond
	OpenAIPrismDefaultPollLimit        = 240
)

// OpenAIPrismStateProvider lets the gateway persist project/conversation/
// response IDs without exposing Prism's sandbox token to downstream clients.
type OpenAIPrismStateProvider interface {
	OpenAIPrismConversationState() (projectID, conversationID, responseID string)
}

// OpenAIPrismSessionState is server-side continuation state. Snapshot may
// contain opaque Prism fields, including nulls and fields unknown to this
// version; it must never be copied to a downstream response.
type OpenAIPrismSessionState struct {
	ProjectID           string
	ConversationID      string
	ResponseID          string
	SandboxURL          string
	SandboxToken        string
	CodexListenSnapshot json.RawMessage
	DeltaFiles          json.RawMessage
	DeltaSync           OpenAIPrismDeltaSync
	Cookies             []*http.Cookie
}

type OpenAIPrismSessionStateProvider interface {
	OpenAIPrismSessionState() OpenAIPrismSessionState
}

type OpenAIPrismConversationOptions struct {
	Request             *apicompat.ResponsesRequest
	ProjectID           string
	ConversationID      string
	PreviousResponseID  string
	SandboxURL          string
	SandboxToken        string
	CodexListenSnapshot json.RawMessage
	OnProgress          func(OpenAIPrismProgress)
}

type OpenAIPrismTransportOptions struct {
	BaseURL      string
	PollInterval time.Duration
	PollLimit    int
}

type OpenAIPrismTransport struct {
	service    *OpenAIGatewayService
	upstream   HTTPUpstream
	options    OpenAIPrismTransportOptions
	stateMu    sync.RWMutex
	state      OpenAIPrismSessionState
	cookiesMu  sync.Mutex
	cookies    map[[32]byte]http.CookieJar
	attachFile func(context.Context, *Account, string, string, string, string) (string, error)
}

type OpenAIPrismHTTPError struct {
	StatusCode int
	Path       string
	Message    string
	Headers    http.Header
}

// OpenAIPrismStartedError means Prism accepted a turn before the operation
// failed. Retrying the complete request could execute remote work twice;
// callers must not automatically fail over by starting another turn.
type OpenAIPrismStartedError struct {
	RequestID string
	Err       error
}

func (e *OpenAIPrismStartedError) Error() string {
	if e == nil || e.Err == nil {
		return "prism turn failed after start"
	}
	return "prism turn failed after start: " + e.Err.Error()
}

func (e *OpenAIPrismStartedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *OpenAIPrismHTTPError) Error() string {
	if e == nil {
		return "prism upstream error"
	}
	return fmt.Sprintf("prism upstream %s: %s", e.Path, e.Message)
}

func (t *OpenAIPrismTransport) OpenAIPrismSessionState() OpenAIPrismSessionState {
	if t == nil {
		return OpenAIPrismSessionState{}
	}
	t.stateMu.RLock()
	defer t.stateMu.RUnlock()
	s := t.state
	s.CodexListenSnapshot = append(json.RawMessage(nil), s.CodexListenSnapshot...)
	s.DeltaFiles = append(json.RawMessage(nil), s.DeltaFiles...)
	s.DeltaSync.Files = append([]OpenAIPrismDeltaFileSync(nil), s.DeltaSync.Files...)
	s.Cookies = prismCloneCookies(s.Cookies)
	return s
}

func (t *OpenAIPrismTransport) OpenAIPrismConversationState() (string, string, string) {
	s := t.OpenAIPrismSessionState()
	return s.ProjectID, s.ConversationID, s.ResponseID
}

func NewOpenAIPrismTransport(service *OpenAIGatewayService, options OpenAIPrismTransportOptions) *OpenAIPrismTransport {
	if strings.TrimSpace(options.BaseURL) == "" {
		options.BaseURL = OpenAIPrismDefaultBaseURL
	}
	if options.PollInterval <= 0 {
		options.PollInterval = OpenAIPrismDefaultPollInterval
	}
	if options.PollLimit <= 0 {
		options.PollLimit = OpenAIPrismDefaultPollLimit
	}
	t := &OpenAIPrismTransport{service: service, options: options}
	if service != nil {
		t.upstream = service.httpUpstream
	}
	return t
}

func NewOpenAIPrismTransportFromUpstream(upstream HTTPUpstream, options OpenAIPrismTransportOptions) *OpenAIPrismTransport {
	t := NewOpenAIPrismTransport(nil, options)
	t.upstream = upstream
	return t
}

func (t *OpenAIPrismTransport) baseURL() string { return strings.TrimRight(t.options.BaseURL, "/") }

func prismCredential(account *Account, key string) string {
	if account == nil {
		return ""
	}
	return strings.TrimSpace(account.GetCredential(key))
}

func (t *OpenAIPrismTransport) doJSON(ctx context.Context, account *Account, token, method, path string, payload any, extraSecrets ...string) ([]byte, int, error) {
	if t == nil || t.upstream == nil {
		return nil, 0, errors.New("prism transport upstream is nil")
	}
	var body io.Reader
	secrets := prismSensitiveValues(account, token, extraSecrets...)
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		body = bytes.NewReader(encoded)
		secrets = append(secrets, prismJSONSecrets(encoded)...)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.baseURL()+path, body)
	if err != nil {
		return nil, 0, prismSafeError(err, secrets)
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	t.setCookies(req, token, account)
	for _, cookie := range req.Cookies() {
		secrets = append(secrets, cookie.Value)
	}
	proxyURL := ""
	if account != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := t.upstream.Do(req, proxyURL, accountID(account), accountConcurrency(account))
	if err != nil {
		return nil, 0, prismSafeError(err, secrets)
	}
	if resp == nil || resp.Body == nil {
		return nil, 0, errors.New("prism upstream returned no response")
	}
	t.captureCookies(req, resp, account, token)
	for _, cookie := range resp.Cookies() {
		secrets = append(secrets, cookie.Value)
	}
	defer func() { _ = resp.Body.Close() }()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, (32<<20)+1))
	if readErr != nil {
		return nil, resp.StatusCode, prismSafeError(readErr, secrets)
	}
	if len(data) > 32<<20 {
		return nil, resp.StatusCode, errors.New("prism JSON response exceeds size limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, &OpenAIPrismHTTPError{StatusCode: resp.StatusCode, Path: prismSafePath(path), Message: prismRedact(prismErrorMessage(data), secrets), Headers: prismSafeHeaders(resp.Header, secrets)}
	}
	return data, resp.StatusCode, nil
}

func (t *OpenAIPrismTransport) doRaw(ctx context.Context, account *Account, token, method, path string, body io.Reader, contentType, sandboxToken string, headers http.Header, extraSecrets ...string) ([]byte, int, http.Header, error) {
	if t == nil || t.upstream == nil {
		return nil, 0, nil, errors.New("prism transport upstream is nil")
	}
	secrets := prismSensitiveValues(account, token, append([]string{sandboxToken}, extraSecrets...)...)
	req, err := http.NewRequestWithContext(ctx, method, t.baseURL()+path, body)
	if err != nil {
		return nil, 0, nil, prismSafeError(err, secrets)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if headers != nil {
		for k, values := range headers {
			for _, v := range values {
				req.Header.Add(k, v)
			}
		}
	}
	if sandboxToken != "" {
		req.Header.Set("x-crixet-sandbox-token", sandboxToken)
	}
	t.setCookies(req, token, account)
	for _, cookie := range req.Cookies() {
		secrets = append(secrets, cookie.Value)
	}
	proxyURL := ""
	if account != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := t.upstream.Do(req, proxyURL, accountID(account), accountConcurrency(account))
	if err != nil {
		return nil, 0, nil, prismSafeError(err, secrets)
	}
	if resp == nil || resp.Body == nil {
		return nil, 0, nil, errors.New("prism upstream returned no response")
	}
	t.captureCookies(req, resp, account, token)
	for _, cookie := range resp.Cookies() {
		secrets = append(secrets, cookie.Value)
	}
	responseHeaders := prismSafeHeaders(resp.Header, secrets)
	defer func() { _ = resp.Body.Close() }()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, (64<<20)+1))
	if readErr != nil {
		return nil, resp.StatusCode, responseHeaders, prismSafeError(readErr, secrets)
	}
	if len(data) > 64<<20 {
		return nil, resp.StatusCode, responseHeaders, errors.New("prism response exceeds size limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, responseHeaders, &OpenAIPrismHTTPError{StatusCode: resp.StatusCode, Path: prismSafePath(path), Message: prismRedact(prismErrorMessage(data), secrets), Headers: responseHeaders.Clone()}
	}
	return data, resp.StatusCode, responseHeaders, nil
}

// ProjectAccess reads the current project permission object. It is useful when
// a caller reuses a project ID stored in account state before starting a turn.
func (t *OpenAIPrismTransport) ProjectAccess(ctx context.Context, account *Account, token, projectID string) ([]byte, error) {
	data, _, err := t.doJSON(ctx, account, token, http.MethodGet, OpenAIPrismProjectAccessPath+"?d="+url.QueryEscape(projectID), nil)
	if err != nil {
		return nil, err
	}
	if !prismProjectAccessible(data) {
		return nil, errors.New("prism project is not accessible")
	}
	return data, nil
}

func prismProjectAccessible(data []byte) bool {
	var v struct {
		Accessible *bool `json:"accessible"`
	}
	if json.Unmarshal(data, &v) != nil {
		return false
	}
	return v.Accessible == nil || *v.Accessible
}

// UploadProjectFile uploads an image or document using the browser's metadata
// headers. The file bytes are never logged or persisted by this adapter.
func (t *OpenAIPrismTransport) UploadProjectFile(ctx context.Context, account *Account, token, projectID, fileID, fileName, contentType string, data []byte) ([]byte, error) {
	h := http.Header{}
	h.Set("x-prism-project-id", projectID)
	h.Set("x-prism-file-id", fileID)
	h.Set("x-prism-file-name", fileName)
	h.Set("x-prism-file-size", fmt.Sprintf("%d", len(data)))
	h.Set("x-prism-require-project-edit-access", "true")
	body, _, _, err := t.doRaw(ctx, account, token, http.MethodPost, "/api/project-files/upload", bytes.NewReader(data), contentType, "", h)
	return body, err
}

func (t *OpenAIPrismTransport) SetProjectThumbnail(ctx context.Context, account *Account, token, projectID, thumbnailUUID string) ([]byte, error) {
	data, _, err := t.doJSON(ctx, account, token, http.MethodPatch, "/api/projects/"+url.PathEscape(projectID)+"/thumbnail", map[string]string{"thumbnail_uuid": thumbnailUUID})
	return data, err
}

func (t *OpenAIPrismTransport) RenderSandbox(ctx context.Context, account *Account, token, sandboxToken, mainDocument, clientStateVector, clientDeleteSetUpdate string) ([]byte, error) {
	payload := map[string]string{"mainDocument": mainDocument, "clientStateVector": clientStateVector, "clientDeleteSetUpdate": clientDeleteSetUpdate}
	body, _, _, err := t.doRaw(ctx, account, token, http.MethodPost, "/s/sandboxes/proxy/render?renderMode=async&renderStatusMode=json&renderResultMode=stream-v1", bytes.NewReader(prismMustJSON(payload)), "application/json", sandboxToken, nil)
	return body, err
}

func (t *OpenAIPrismTransport) RenderStatus(ctx context.Context, account *Account, token, sandboxToken, statusPath string) ([]byte, http.Header, error) {
	if strings.TrimSpace(statusPath) == "" {
		statusPath = "/s/sandboxes/proxy/render-status?waitMs=10000&renderResultMode=stream-v1&renderStatusMode=json"
	}
	parsed, err := url.Parse(statusPath)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.Fragment != "" || parsed.Path != "/s/sandboxes/proxy/render-status" {
		return nil, nil, errors.New("invalid Prism render status path")
	}
	body, _, headers, err := t.doRaw(ctx, account, token, http.MethodGet, statusPath, nil, "", sandboxToken, nil)
	return body, headers, err
}

func prismMustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func (t *OpenAIPrismTransport) setCookies(req *http.Request, token string, account *Account) {
	if req == nil || req.URL == nil {
		return
	}
	for _, cookie := range t.cookieJar(account, token, req.URL).Cookies(req.URL) {
		req.AddCookie(cookie)
	}
}

// A browser cookie jar is scoped to the upstream account and original login
// credentials. Refreshed cookies survive a turn, but cannot cross accounts or
// a credential replacement when a transport instance is reused.
func (t *OpenAIPrismTransport) cookieJar(account *Account, token string, endpoint *url.URL) http.CookieJar {
	rawCookies := prismCredential(account, "prism_cookie")
	key := prismCookieKey(account, token)
	t.cookiesMu.Lock()
	defer t.cookiesMu.Unlock()
	if jar := t.cookies[key]; jar != nil {
		return jar
	}
	jar, _ := cookiejar.New(nil)
	seedByName := make(map[string]*http.Cookie)
	imported := &http.Request{Header: http.Header{"Cookie": []string{rawCookies}}}
	for _, cookie := range imported.Cookies() {
		cookie.Path, cookie.Secure = "/", endpoint.Scheme == "https"
		seedByName[cookie.Name] = cookie
	}
	if v := strings.TrimSpace(token); v != "" {
		seedByName["prism_oai_access_token"] = &http.Cookie{Name: "prism_oai_access_token", Value: v, Path: "/", Secure: endpoint.Scheme == "https"}
	}
	if v := prismCredential(account, "prism_session_token"); v != "" {
		seedByName["prism_session_token"] = &http.Cookie{Name: "prism_session_token", Value: v, Path: "/", Secure: endpoint.Scheme == "https"}
	}
	seed := make([]*http.Cookie, 0, len(seedByName))
	for _, cookie := range seedByName {
		seed = append(seed, cookie)
	}
	jar.SetCookies(endpoint, seed)
	if t.cookies == nil {
		t.cookies = make(map[[32]byte]http.CookieJar)
	}
	t.cookies[key] = jar
	return jar
}

func prismCookieKey(account *Account, token string) [32]byte {
	return sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s\x00%s", accountID(account), token, prismCredential(account, "prism_session_token"), prismCredential(account, "prism_cookie"))))
}

func prismCloneCookies(cookies []*http.Cookie) []*http.Cookie {
	cloned := make([]*http.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie != nil {
			copyCookie := *cookie
			cloned = append(cloned, &copyCookie)
		}
	}
	return cloned
}

// RestoreCookies restores a server-side continuation only into a fresh jar.
// Host and path are fixed here so persisted state cannot widen cookie scope.
func (t *OpenAIPrismTransport) RestoreCookies(account *Account, token string, cookies []*http.Cookie) {
	if t == nil || len(cookies) == 0 {
		return
	}
	endpoint, err := url.Parse(t.baseURL())
	if err != nil || endpoint.Host == "" {
		return
	}
	key := prismCookieKey(account, token)
	t.cookiesMu.Lock()
	defer t.cookiesMu.Unlock()
	if t.cookies[key] != nil {
		return
	}
	jar, _ := cookiejar.New(nil)
	for _, cookie := range prismCloneCookies(cookies) {
		cookie.Domain, cookie.Path, cookie.Secure = "", "/", endpoint.Scheme == "https"
		if cookie.Valid() == nil {
			jar.SetCookies(endpoint, []*http.Cookie{cookie})
		}
	}
	if t.cookies == nil {
		t.cookies = make(map[[32]byte]http.CookieJar)
	}
	t.cookies[key] = jar
}

func (t *OpenAIPrismTransport) currentCookies(account *Account, token string) []*http.Cookie {
	endpoint, err := url.Parse(t.baseURL() + "/")
	if err != nil {
		return nil
	}
	return prismCloneCookies(t.cookieJar(account, token, endpoint).Cookies(endpoint))
}

func (t *OpenAIPrismTransport) captureCookies(req *http.Request, resp *http.Response, account *Account, token string) {
	if req != nil && req.URL != nil && resp != nil {
		t.cookieJar(account, token, req.URL).SetCookies(req.URL, resp.Cookies())
	}
}

func prismSensitiveValues(account *Account, token string, extra ...string) []string {
	values := append([]string{token}, extra...)
	if account != nil {
		for key := range account.Credentials {
			if IsSensitiveCredentialKey(key) {
				values = append(values, account.GetCredential(key))
			}
		}
	}
	return values
}

// Opaque turn-state and snapshot JSON can contain sandbox credentials. Extract
// only secret values for error redaction; the original JSON remains untouched.
func prismJSONSecrets(raw []byte) []string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	var result []string
	var walk func(any, int)
	walk = func(value any, depth int) {
		if depth > 16 {
			return
		}
		switch v := value.(type) {
		case map[string]any:
			for key, child := range v {
				lower := strings.ToLower(key)
				if text, ok := child.(string); ok && (strings.Contains(lower, "token") || strings.Contains(lower, "cookie") || strings.Contains(lower, "authorization")) {
					result = append(result, text)
				}
				walk(child, depth+1)
			}
		case []any:
			for _, child := range v {
				walk(child, depth+1)
			}
		case string:
			if strings.HasPrefix(strings.TrimSpace(v), "{") || strings.HasPrefix(strings.TrimSpace(v), "[") {
				var nested any
				if json.Unmarshal([]byte(v), &nested) == nil {
					walk(nested, depth+1)
				}
			}
		}
	}
	walk(value, 0)
	return result
}

func prismRedact(message string, secrets []string) string {
	return sanitizeUpstreamErrorMessage(prismReplaceSecrets(message, secrets))
}

func prismReplaceSecrets(message string, secrets []string) string {
	values := append([]string(nil), secrets...)
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, secret := range values {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	return message
}

type prismSanitizedError struct {
	cause   error
	message string
}

func (e *prismSanitizedError) Error() string { return e.message }
func (e *prismSanitizedError) Unwrap() error { return e.cause }
func prismSafeError(err error, secrets []string) error {
	if err == nil {
		return nil
	}
	return &prismSanitizedError{cause: err, message: prismRedact(err.Error(), secrets)}
}

func prismSafePath(path string) string {
	if parsed, err := url.Parse(path); err == nil {
		return parsed.Path
	}
	return "Prism API"
}

func prismSafeHeaders(headers http.Header, secrets []string) http.Header {
	result := make(http.Header)
	for key, values := range headers {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "cookie") || strings.Contains(lower, "token") || strings.Contains(lower, "authorization") {
			continue
		}
		for _, value := range values {
			result.Add(key, prismRedact(value, secrets))
		}
	}
	return result
}

func accountID(a *Account) int64 {
	if a == nil {
		return 0
	}
	return a.ID
}
func accountConcurrency(a *Account) int {
	if a == nil || a.Concurrency <= 0 {
		return 1
	}
	return a.Concurrency
}

func prismErrorMessage(body []byte) string {
	var v struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &v) == nil {
		if strings.TrimSpace(v.Error.Message) != "" {
			return v.Error.Message
		}
		if strings.TrimSpace(v.Message) != "" {
			return v.Message
		}
	}
	return "upstream request failed"
}

type prismBackendSession struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}
type prismProject struct {
	UUID  string `json:"uuid"`
	Title string `json:"title"`
}
type prismAuthSession struct {
	User struct {
		AppMetadata struct {
			UserID string `json:"user_id"`
		} `json:"app_metadata"`
	} `json:"user"`
	Policy struct {
		User struct {
			OpenAIUserID string `json:"openai_user_id"`
		} `json:"user"`
	} `json:"policy"`
}

// Start and status return the same turn envelope, including synchronous results.
type prismStartResponse = prismStatusResponse

type prismStatusResponse struct {
	ConversationID      string          `json:"conversation_id"`
	Status              string          `json:"status"`
	RequestID           string          `json:"request_id"`
	CodexAsyncJobID     string          `json:"codex_async_job_id,omitempty"`
	TurnState           json.RawMessage `json:"turn_state,omitempty"`
	CodexListenSnapshot json.RawMessage `json:"codex_listen_snapshot,omitempty"`
	Response            *struct {
		Status  string          `json:"status"`
		Payload json.RawMessage `json:"payload"`
	} `json:"response,omitempty"`
	CodexLiveProgress *prismLiveProgress `json:"codex_live_progress,omitempty"`
}

func (t *OpenAIPrismTransport) bootstrap(ctx context.Context, account *Account, token string) (string, string, error) {
	data, _, err := t.doJSON(ctx, account, token, http.MethodPost, OpenAIPrismBackendNewPath, nil)
	if err != nil {
		return "", "", err
	}
	var b prismBackendSession
	if err := json.Unmarshal(data, &b); err != nil {
		return "", "", err
	}
	if strings.TrimSpace(b.URL) == "" || strings.TrimSpace(b.Token) == "" {
		return "", "", errors.New("prism backend session missing url or token")
	}
	if err := t.validateSandboxURL(b.URL); err != nil {
		return "", "", err
	}
	return b.URL, b.Token, nil
}

func (t *OpenAIPrismTransport) validateSandboxURL(value string) error {
	base, baseErr := url.Parse(t.baseURL())
	target, targetErr := url.Parse(value)
	if baseErr != nil || targetErr != nil || base.Host == "" || target.Host == "" || target.User != nil ||
		!strings.EqualFold(base.Scheme, target.Scheme) || !strings.EqualFold(base.Host, target.Host) ||
		(target.Scheme != "https" && target.Scheme != "http") || target.RawQuery != "" || target.Fragment != "" ||
		strings.TrimRight(target.Path, "/") != "/s/sandboxes/proxy" {
		return errors.New("prism backend session URL is not a same-origin sandbox proxy")
	}
	return nil
}

func (t *OpenAIPrismTransport) authUserID(ctx context.Context, account *Account, token string) (string, error) {
	data, _, err := t.doJSON(ctx, account, token, http.MethodGet, "/auth/session", nil)
	if err != nil {
		return "", err
	}
	var a prismAuthSession
	if json.Unmarshal(data, &a) != nil {
		return "", errors.New("prism auth session returned invalid JSON")
	}
	if a.User.AppMetadata.UserID != "" {
		return a.User.AppMetadata.UserID, nil
	}
	if a.Policy.User.OpenAIUserID != "" {
		return a.Policy.User.OpenAIUserID, nil
	}
	return "", errors.New("prism auth session missing user id")
}

func (t *OpenAIPrismTransport) ensureProject(ctx context.Context, account *Account, token, projectID string) (string, error) {
	if strings.TrimSpace(projectID) != "" {
		return strings.TrimSpace(projectID), nil
	}
	return t.ProjectCreate(ctx, account, token, "sub2api")
}

func prismConversationID(id string) string {
	if strings.TrimSpace(id) != "" {
		return id
	}
	return "cdx1_" + uuid.NewString()
}

func (t *OpenAIPrismTransport) initializeConversation(ctx context.Context, account *Account, token, userID, projectID, conversationID string) {
	// Prism's UI performs this read before the first turn. It is deliberately
	// best effort: an empty history is a valid new conversation and a transient
	// history failure must not prevent response_with_tools_start from running.
	_, _, _ = t.doJSON(ctx, account, token, http.MethodPost, OpenAIPrismConversationHistoryPath, map[string]any{
		"conversationId": conversationID,
		"order":          "desc",
		"limit":          50,
		"userId":         userID,
		"projectId":      projectID,
	})
}

func prismSnapshotMetadata(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return string(raw)
}

func prismInput(req *apicompat.ResponsesRequest) json.RawMessage {
	if req == nil {
		return json.RawMessage(`[]`)
	}
	items := make([]json.RawMessage, 0)
	message := func(role, text string) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"type": "message", "role": role, "content": []map[string]string{{"type": "input_text", "text": text}}})
		return b
	}
	if strings.TrimSpace(req.Instructions) != "" {
		items = append(items, message("system", req.Instructions))
	}
	raw := bytes.TrimSpace(req.Input)
	if len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		if raw[0] == '[' {
			var input []json.RawMessage
			if json.Unmarshal(raw, &input) != nil {
				return raw
			}
			items = append(items, input...)
		} else {
			text := string(raw)
			if raw[0] == '"' {
				_ = json.Unmarshal(raw, &text)
			}
			items = append(items, message("user", text))
		}
	}
	b, _ := json.Marshal(items)
	return b
}

func (t *OpenAIPrismTransport) Do(ctx context.Context, account *Account, token string, options OpenAIPrismConversationOptions) (resp *http.Response, err error) {
	if t == nil {
		return nil, errors.New("prism transport is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" && prismCredential(account, "prism_session_token") == "" && prismCredential(account, "prism_cookie") == "" {
		return nil, errors.New("prism access token is required")
	}
	userID, err := t.authUserID(ctx, account, token)
	if err != nil {
		return nil, err
	}
	projectID, err := t.ensureProject(ctx, account, token, options.ProjectID)
	if err != nil {
		return nil, err
	}
	accessData, accessErr := t.ProjectAccess(ctx, account, token, projectID)
	if accessErr != nil || !prismProjectAccessible(accessData) {
		if accessErr != nil {
			return nil, accessErr
		}
		return nil, errors.New("prism project is not accessible")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sandboxURL, sandboxToken := strings.TrimSpace(options.SandboxURL), strings.TrimSpace(options.SandboxToken)
	if sandboxURL != "" || sandboxToken != "" {
		if sandboxURL == "" || sandboxToken == "" {
			return nil, errors.New("prism continuation missing sandbox url or token")
		}
		if err := t.validateSandboxURL(sandboxURL); err != nil {
			return nil, err
		}
		if _, _, heartbeatErr := t.Heartbeat(ctx, account, token, sandboxToken); heartbeatErr != nil {
			var upstreamErr *OpenAIPrismHTTPError
			if !errors.As(heartbeatErr, &upstreamErr) || (upstreamErr.StatusCode != http.StatusUnauthorized && upstreamErr.StatusCode != http.StatusForbidden && upstreamErr.StatusCode != http.StatusGone) {
				return nil, heartbeatErr
			}
			sandboxURL, sandboxToken, err = t.bootstrap(ctx, account, token)
			if err != nil {
				return nil, err
			}
			if err := t.prepareSandbox(ctx, account, token, projectID, sandboxToken); err != nil {
				return nil, err
			}
		}
	} else {
		sandboxURL, sandboxToken, err = t.bootstrap(ctx, account, token)
		if err != nil {
			return nil, err
		}
		if err := t.prepareSandbox(ctx, account, token, projectID, sandboxToken); err != nil {
			return nil, err
		}
	}
	conversationID := prismConversationID(options.ConversationID)
	t.initializeConversation(ctx, account, token, userID, projectID, conversationID)
	model, effort := OpenAIPrismDefaultModel, "medium"
	if options.Request != nil {
		if value := strings.TrimSpace(options.Request.Model); value != "" {
			model = value
		}
		if options.Request.Reasoning != nil {
			if value := strings.TrimSpace(options.Request.Reasoning.Effort); value != "" {
				effort = value
			}
		}
	}
	metadata := map[string]any{"projectId": projectID, "userId": userID, "model": model, "reasoning_effort": effort, "frontend_origin": t.baseURL(), "sandbox_url": strings.TrimRight(sandboxURL, "/") + "/", "sandbox_token": sandboxToken}
	if snapshot := prismSnapshotMetadata(options.CodexListenSnapshot); snapshot != "" {
		metadata["codex_listen_snapshot"] = snapshot
	}
	start := map[string]any{"input": json.RawMessage(prismInput(options.Request)), "metadata": metadata, "conversationId": conversationID}
	if options.PreviousResponseID != "" {
		start["previousResponseId"] = options.PreviousResponseID
	}
	startBytes, startStatus, err := t.doJSON(ctx, account, token, http.MethodPost, OpenAIPrismStartPath, start)
	if err != nil {
		if startStatus == 0 || (startStatus >= 200 && startStatus < 300) {
			return nil, &OpenAIPrismStartedError{Err: err}
		}
		return nil, err
	}
	var started prismStartResponse
	if err := json.Unmarshal(startBytes, &started); err != nil {
		return nil, &OpenAIPrismStartedError{Err: err}
	}
	if started.RequestID == "" {
		return nil, &OpenAIPrismStartedError{Err: errors.New("prism start response missing request_id")}
	}
	defer func() {
		if err != nil {
			err = &OpenAIPrismStartedError{RequestID: started.RequestID, Err: err}
		}
	}()
	state := append(json.RawMessage(nil), started.TurnState...)
	snapshot := append(json.RawMessage(nil), started.CodexListenSnapshot...)
	payload, err := prismTurnResult(started, OpenAIPrismStartPath)
	if err != nil {
		return nil, err
	}
	if len(payload) == 0 && !prismValidTurnState(state) {
		return nil, errors.New("prism start response missing valid non-empty turn_state object")
	}
	for i := 0; len(payload) == 0 && i < t.options.PollLimit; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		poll := map[string]any{"request_id": started.RequestID, "turn_state": state}
		pollSecrets := append([]string{sandboxToken}, prismJSONSecrets(snapshot)...)
		data, _, err := t.doJSON(ctx, account, token, http.MethodPost, OpenAIPrismStatusPath, poll, pollSecrets...)
		if err != nil {
			return nil, err
		}
		var status prismStatusResponse
		if err := json.Unmarshal(data, &status); err != nil {
			return nil, err
		}
		if len(status.CodexListenSnapshot) > 0 {
			snapshot = append(snapshot[:0], status.CodexListenSnapshot...)
		}
		payload, err = prismTurnResult(status, OpenAIPrismStatusPath)
		if err != nil {
			return nil, err
		}
		if next := bytes.TrimSpace(status.TurnState); len(payload) == 0 && len(next) > 0 && !bytes.Equal(next, []byte("null")) {
			if !prismValidTurnState(next) {
				return nil, errors.New("prism status response contains invalid turn_state object")
			}
			state = append(state[:0], next...)
		}
		if options.OnProgress != nil && status.CodexLiveProgress != nil {
			secrets := prismSensitiveValues(account, token, sandboxToken)
			secrets = append(secrets, prismJSONSecrets(state)...)
			secrets = append(secrets, prismJSONSecrets(snapshot)...)
			for _, cookie := range t.currentCookies(account, token) {
				secrets = append(secrets, cookie.Value)
			}
			options.OnProgress(status.CodexLiveProgress.public(secrets))
		} else if options.OnProgress != nil && strings.EqualFold(status.Status, "pending") {
			options.OnProgress(OpenAIPrismProgress{})
		}
		if len(payload) > 0 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(t.options.PollInterval):
		}
	}
	if len(payload) == 0 {
		return nil, errors.New("prism turn did not complete before poll limit")
	}
	responseID := started.RequestID
	var envelope struct {
		ID                  string          `json:"id"`
		CodexListenSnapshot json.RawMessage `json:"codexListenSnapshot"`
		DeltaFiles          json.RawMessage `json:"codexDeltaFiles"`
	}
	_ = json.Unmarshal(payload, &envelope)
	if envelope.ID != "" {
		responseID = envelope.ID
	}
	if len(envelope.CodexListenSnapshot) > 0 {
		snapshot = append(snapshot[:0], envelope.CodexListenSnapshot...)
	}
	deltaSecrets := prismSensitiveValues(account, token, sandboxToken)
	deltaSecrets = append(deltaSecrets, prismJSONSecrets(snapshot)...)
	for _, cookie := range t.currentCookies(account, token) {
		deltaSecrets = append(deltaSecrets, cookie.Value)
	}
	deltaFiles := prismNormalizeDeltaFiles(envelope.DeltaFiles, deltaSecrets)
	syncSecrets := append(append([]string(nil), deltaSecrets...), projectID, conversationID, started.ConversationID, sandboxURL)
	deltaSync := t.syncDeltaFiles(ctx, account, token, projectID, sandboxToken, envelope.DeltaFiles, syncSecrets)
	if strings.TrimSpace(started.ConversationID) == "" {
		started.ConversationID = conversationID
	}
	t.stateMu.Lock()
	t.state = OpenAIPrismSessionState{ProjectID: projectID, ConversationID: started.ConversationID, ResponseID: responseID, SandboxURL: sandboxURL, SandboxToken: sandboxToken, CodexListenSnapshot: append(json.RawMessage(nil), snapshot...), DeltaFiles: deltaFiles, DeltaSync: deltaSync, Cookies: t.currentCookies(account, token)}
	t.stateMu.Unlock()
	stream := options.Request != nil && options.Request.Stream
	return prismResponsesHTTP(prismNativeResponsesPayload(payload, deltaSecrets), started.RequestID, started.ConversationID, projectID, stream, model), nil
}

func prismStatusFailed(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error", "cancelled", "canceled":
		return true
	}
	return false
}

func prismStatusCompleted(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "complete", "succeeded", "success", "done":
		return true
	default:
		return false
	}
}

// prismResponsesObject copies only public Responses fields from Prism's payload.
// Internal debug metadata and sandbox snapshots never enter either output format.
func prismResponsesObject(payload []byte, requestID string, models ...string) map[string]any {
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	_ = decoder.Decode(&value)
	id, _ := value["id"].(string)
	if strings.TrimSpace(id) == "" {
		id = requestID
	}
	model := ""
	if len(models) > 0 {
		model = strings.TrimSpace(models[0])
	}
	if model == "" {
		model, _ = value["model"].(string)
	}
	if model == "" {
		model = OpenAIPrismDefaultModel
	}
	createdAt := time.Now().Unix()
	if n, ok := value["created_at"].(json.Number); ok {
		if timestamp, err := n.Int64(); err == nil && timestamp > 0 {
			createdAt = timestamp
		}
	}
	output := make([]any, 0)
	if items, ok := value["output"].([]any); ok {
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok || item == nil {
				continue
			}
			itemID, _ := item["id"].(string)
			if itemID == "" {
				item["id"] = fmt.Sprintf("%s_item_%d", id, len(output))
			}
			item["status"] = "completed"
			if item["type"] == "message" {
				if role, _ := item["role"].(string); role == "" {
					item["role"] = "assistant"
				}
				content, _ := item["content"].([]any)
				if content == nil {
					content = make([]any, 0)
				}
				for _, rawPart := range content {
					if part, ok := rawPart.(map[string]any); ok && part["type"] == "output_text" {
						if _, ok := part["text"].(string); !ok {
							part["text"] = ""
						}
						if _, ok := part["annotations"].([]any); !ok {
							part["annotations"] = []any{}
						}
						if _, ok := part["logprobs"].([]any); !ok {
							part["logprobs"] = []any{}
						}
					}
				}
				item["content"] = content
			}
			output = append(output, item)
		}
	}
	response := map[string]any{"id": id, "object": "response", "created_at": createdAt, "model": model, "status": "completed", "output": output}
	if usage, ok := value["usage"].(map[string]any); ok {
		response["usage"] = usage
	}
	return response
}

func prismResponsesHTTP(payload []byte, requestID string, conversationID, projectID string, stream bool, models ...string) *http.Response {
	if stream {
		return prismResponsesSSE(payload, requestID, conversationID, projectID, models...)
	}
	body, _ := json.Marshal(prismResponsesObject(payload, requestID, models...))
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{requestID}}, Body: io.NopCloser(bytes.NewReader(body))}
}

func prismResponsesSSE(payload []byte, requestID string, conversationID, projectID string, models ...string) *http.Response {
	response := prismResponsesObject(payload, requestID, models...)
	responseID, _ := response["id"].(string)
	var b bytes.Buffer
	sequence := 0
	write := func(event string, value map[string]any) {
		value["type"], value["sequence_number"] = event, sequence
		sequence++
		data, _ := json.Marshal(value)
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", event, data)
	}
	created := map[string]any{"id": responseID, "object": "response", "created_at": response["created_at"], "model": response["model"], "status": "in_progress", "output": []any{}}
	write("response.created", map[string]any{"response": created})
	output, _ := response["output"].([]any)
	for i, raw := range output {
		item, _ := raw.(map[string]any)
		itemID, _ := item["id"].(string)
		added := make(map[string]any, len(item))
		for key, value := range item {
			added[key] = value
		}
		added["status"] = "in_progress"
		switch item["type"] {
		case "message":
			added["content"] = []any{}
		case "function_call":
			added["arguments"] = ""
		case "custom_tool_call":
			added["input"] = ""
		case "reasoning":
			added["summary"] = []any{}
		}
		write("response.output_item.added", map[string]any{"output_index": i, "item": added})
		if content, ok := item["content"].([]any); ok {
			for j, rawPart := range content {
				part, ok := rawPart.(map[string]any)
				if !ok || part["type"] != "output_text" {
					continue
				}
				text, _ := part["text"].(string)
				write("response.content_part.added", map[string]any{"response_id": responseID, "item_id": itemID, "output_index": i, "content_index": j, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}, "logprobs": []any{}}})
				if text != "" {
					write("response.output_text.delta", map[string]any{"response_id": responseID, "item_id": itemID, "output_index": i, "content_index": j, "delta": text, "logprobs": []any{}})
				}
				write("response.output_text.done", map[string]any{"response_id": responseID, "item_id": itemID, "output_index": i, "content_index": j, "text": text, "logprobs": part["logprobs"]})
				write("response.content_part.done", map[string]any{"response_id": responseID, "item_id": itemID, "output_index": i, "content_index": j, "part": part})
			}
		}
		// Tool items can be produced by the gateway's request-scoped prompt tool
		// parser. Preserve their standard lifecycle for all protocol adapters.
		switch item["type"] {
		case "function_call", "custom_tool_call":
			prefix, field := "response.function_call_arguments", "arguments"
			if item["type"] == "custom_tool_call" {
				prefix, field = "response.custom_tool_call_input", "input"
			}
			value, _ := item[field].(string)
			if value != "" {
				write(prefix+".delta", map[string]any{"output_index": i, "item_id": itemID, "call_id": item["call_id"], "name": item["name"], "delta": value})
			}
			write(prefix+".done", map[string]any{"output_index": i, "item_id": itemID, "call_id": item["call_id"], "name": item["name"], field: value})
		}
		write("response.output_item.done", map[string]any{"output_index": i, "item": item})
	}
	write("response.completed", map[string]any{"response": response})
	_, _ = b.WriteString("data: [DONE]\n\n")
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{requestID}}, Body: io.NopCloser(bytes.NewReader(b.Bytes()))}
}
