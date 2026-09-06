package service

// This file contains an isolated adapter for the authenticated ChatGPT Web
// conversation endpoint.  It deliberately does not change gateway routing;
// callers can opt in by constructing the adapter and then passing the
// translated response through the existing Responses SSE handlers.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	mathrand "math/rand"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
	"github.com/google/uuid"
	"github.com/imroc/req/v3"
	"golang.org/x/crypto/sha3"
)

const (
	// OpenAIWebDefaultBaseURL is the first-party ChatGPT web origin.
	OpenAIWebDefaultBaseURL = "https://chatgpt.com"
	// OpenAIWebTestModel is kept as the compatibility name used by the
	// account-test path. It is also the Web UI's Default selector.
	OpenAIWebTestModel               = "auto"
	OpenAIWebConversationPath        = "/backend-api/f/conversation"
	OpenAIWebConversationPreparePath = OpenAIWebConversationPath + "/prepare"
	OpenAIWebUserWebsocketPath       = "/backend-api/celsius/ws/user"
	// The Plus web picker requests this manifest with the feature flags below.
	// It returns the authenticated Web entitlement slugs (including future
	// entries), whereas the older history-only query can return a different
	// general-purpose catalog.
	OpenAIWebModelsPath               = "/backend-api/models?iim=false&is_gizmo=false&supports_model_picker_upgrade_presets=true"
	OpenAIWebModelsRoute              = "/backend-api/models"
	OpenAIWebRequirementsPath         = "/backend-api/sentinel/chat-requirements"
	OpenAIWebSentinelPingPath         = "/backend-api/sentinel/ping"
	OpenAIWebDefaultClientVersion     = "prod-a194cd50d4416d3c0b47c740f206b12ce60f5887"
	OpenAIWebDefaultClientBuildNumber = "6708908"
	openAIWebDefaultUserAgent         = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0"
	openAIWebMaxBootstrapBytes        = 32 << 20
	// ChatGPT occasionally returns large HTML/JSON challenge pages through a
	// proxy. Keep the bound finite, but large enough to preserve useful error
	// context instead of misclassifying it as a transport failure.
	openAIWebMaxResponseErrorBytes = 1 << 20
	openAIWebMaxAttachmentBytes    = 32 << 20
	openAIWebMaxAttachmentCount    = 16
	openAIWebDefaultPowAttempts    = 100_000
	openAIWebDefaultSecCHUA        = `"Microsoft Edge";v="143", "Chromium";v="143", "Not A(Brand";v="24"`
	// OpenAIWebTransportExtraKey selects the classic ChatGPT Web protocol for
	// an OpenAI OAuth-like account. Missing or unknown values remain on Codex.
	OpenAIWebTransportExtraKey = "openai_transport"
)

// openAIWebModelCatalog is the stable fallback order for selectors observed in
// the current ChatGPT Web UI. Account-specific availability must come from
// DiscoverModels; this list is not an entitlement claim for every account.
var openAIWebModelCatalog = [...]string{
	"auto",
	"gpt-5.6-sol",
	"gpt-5.6-terra",
	"gpt-5.6-luna",
	"gpt-5.5",
}

// OpenAIWebModels returns the known ChatGPT Web selectors in UI order. It is a
// protocol catalog only; callers exposing account capabilities should prefer
// DiscoverModels and fall back to auto when discovery is unavailable.
func OpenAIWebModels() []string {
	return append([]string(nil), openAIWebModelCatalog[:]...)
}

var openAIWebModelSlugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,127}$`)

// NormalizeOpenAIWebModel trims and canonicalizes a Web selector. ChatGPT can
// add or remove account-specific models without a Sub2API release, so this
// validates the wire-safe slug shape instead of enforcing the fallback catalog.
func NormalizeOpenAIWebModel(value string) (string, bool) {
	normalized := strings.TrimSpace(value)
	if strings.EqualFold(normalized, OpenAIWebTestModel) {
		return OpenAIWebTestModel, true
	}
	if !openAIWebModelSlugRE.MatchString(normalized) {
		return "", false
	}
	return normalized, true
}

// isOpenAIWebModelCandidate filters obvious cross-provider/legacy aliases
// while the account has not completed its authenticated model discovery. Once
// a catalog is known, IsModelSupported uses that exact catalog instead.
func isOpenAIWebModelCandidate(model string) bool {
	model = strings.TrimSpace(model)
	if model == "gpt-5.6" {
		return false
	}
	for _, prefix := range []string{"claude-", "deepseek-", "gemini-", "grok-"} {
		if strings.HasPrefix(model, prefix) {
			return false
		}
	}
	return true
}

// OpenAIWebTransportOptions controls the browser-like identity and endpoint
// used by OpenAIWebTransport.  BaseURL is intentionally configurable for
// httptest callers; production callers should leave it empty.
type OpenAIWebTransportOptions struct {
	BaseURL           string
	UserAgent         string
	ClientVersion     string
	ClientBuildNumber string
	DeviceID          string
	SessionID         string
	Timezone          string
	TimezoneOffsetMin int
	PowMaxAttempts    int
	SkipBootstrap     bool
	// TopicReadTimeout bounds an idle read from the user WebSocket. A Web
	// model that never emits a topic frame must not hold a gateway request
	// until the generic stream timeout; zero uses the 60-second default.
	TopicReadTimeout time.Duration
}

// OpenAIWebRequirements contains the short-lived sentinel values returned by
// the prepare/finalize handshake.
type OpenAIWebRequirements struct {
	Token          string
	PrepareToken   string
	ProofToken     string
	TurnstileToken string
	SOToken        string
	ConduitToken   string
}

// OpenAIWebChallengeError indicates that the upstream requires an interactive
// challenge that this server-side adapter cannot solve safely.
type OpenAIWebChallengeError struct {
	Kind     string
	Endpoint string
}

func (e *OpenAIWebChallengeError) Error() string {
	if e == nil {
		return "chatgpt web challenge required"
	}
	kind := strings.TrimSpace(e.Kind)
	if kind == "" {
		kind = "unknown"
	}
	return fmt.Sprintf("chatgpt web challenge required: %s", kind)
}

// OpenAIWebHTTPError is a bounded, credential-free upstream error.
type OpenAIWebHTTPError struct {
	Endpoint   string
	StatusCode int
	Message    string
}

// OpenAIWebRequestError identifies a downstream request field that cannot be
// represented by the classic ChatGPT Web conversation protocol. Gateway
// callers should expose it as an OpenAI invalid_request_error with HTTP 400.
type OpenAIWebRequestError struct {
	Param   string
	Message string
}

func (e *OpenAIWebRequestError) Error() string {
	if e == nil {
		return "invalid ChatGPT web request"
	}
	if strings.TrimSpace(e.Message) != "" {
		return strings.TrimSpace(e.Message)
	}
	if strings.TrimSpace(e.Param) != "" {
		return fmt.Sprintf("parameter %q is not supported by ChatGPT web transport", strings.TrimSpace(e.Param))
	}
	return "invalid ChatGPT web request"
}

func openAIWebUnsupportedParam(param string) error {
	return &OpenAIWebRequestError{Param: param}
}

func openAIWebInvalidParam(param, message string) error {
	return &OpenAIWebRequestError{Param: param, Message: message}
}

func validateOpenAIWebModel(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		// The gateway reports a missing model separately. Keep the transport
		// validator composable for callers that build the request incrementally.
		return nil
	}
	if _, ok := NormalizeOpenAIWebModel(trimmed); !ok {
		return openAIWebInvalidParam("model", fmt.Sprintf("model %q is not supported by ChatGPT web transport", trimmed))
	}
	return nil
}

// ValidateOpenAIWebChatCompletionsRequest rejects only constraints that cannot
// be represented by the Web conversation protocol. Advisory compatibility
// fields are validated and then omitted from the private payload.
func ValidateOpenAIWebChatCompletionsRequest(req *apicompat.ChatCompletionsRequest) error {
	return ValidateOpenAIWebChatCompletionsRequestWithPromptTools(req, false)
}

// ValidateOpenAIWebChatCompletionsRequestWithPromptTools applies the regular
// Web compatibility checks and optionally enables the prompt-based tool bridge.
func ValidateOpenAIWebChatCompletionsRequestWithPromptTools(req *apicompat.ChatCompletionsRequest, promptToolsEnabled bool) error {
	if req == nil {
		return openAIWebInvalidParam("", "Chat Completions request is nil")
	}
	if err := validateOpenAIWebModel(req.Model); err != nil {
		return err
	}
	// ChatGPT Web has no sampling controls. These fields are intentionally
	// accepted for OpenAI-client compatibility and omitted from the private
	// payload; reject only values that are invalid under the public contract.
	if err := validateOpenAISamplingParameter("temperature", req.Temperature, 0, 2); err != nil {
		return err
	}
	if err := validateOpenAISamplingParameter("top_p", req.TopP, 0, 1); err != nil {
		return err
	}
	if err := validateOpenAIPositiveIntegerParameter("max_tokens", req.MaxTokens); err != nil {
		return err
	}
	if err := validateOpenAIPositiveIntegerParameter("max_completion_tokens", req.MaxCompletionTokens); err != nil {
		return err
	}
	if openAIWebRawJSONHasValue(req.Stop) && !openAIWebJSONIsEmptyArray(req.Stop) {
		return openAIWebUnsupportedParam("stop")
	}
	if len(req.Tools) > 0 {
		if !promptToolsEnabled {
			return openAIWebUnsupportedParam("tools")
		}
		if _, err := NewOpenAIWebPromptToolsFromChatRequest(req); err != nil {
			return openAIWebInvalidParam("tools", err.Error())
		}
	}
	if !openAIWebNoOpToolChoice(req.ToolChoice) {
		if !promptToolsEnabled {
			return openAIWebUnsupportedParam("tool_choice")
		}
		if _, _, err := normalizeOpenAIWebPromptToolChoice(req.ToolChoice, nil); err != nil {
			return openAIWebInvalidParam("tool_choice", err.Error())
		}
	}
	// parallel_tool_calls is a no-op when tools are absent (tools themselves
	// are rejected below), so either boolean value can be safely ignored.
	if len(req.Functions) > 0 {
		if !promptToolsEnabled {
			return openAIWebUnsupportedParam("functions")
		}
		if len(req.Tools) == 0 {
			if _, err := NewOpenAIWebPromptToolsFromChatRequest(req); err != nil {
				return openAIWebInvalidParam("functions", err.Error())
			}
		}
	}
	if !openAIWebNoOpToolChoice(req.FunctionCall) {
		if !promptToolsEnabled {
			return openAIWebUnsupportedParam("function_call")
		}
		if _, _, err := normalizeOpenAIWebPromptToolChoice(nil, req.FunctionCall); err != nil {
			return openAIWebInvalidParam("function_call", err.Error())
		}
	}
	if !openAIWebPlainTextFormat(req.ResponseFormat) {
		return openAIWebUnsupportedParam("response_format")
	}
	if !openAIWebDefaultServiceTier(req.ServiceTier) {
		return openAIWebUnsupportedParam("service_tier")
	}
	if !openAIWebReasoningEffortSupported(req.ReasoningEffort) {
		return openAIWebInvalidParam("reasoning_effort", "reasoning_effort is not supported by ChatGPT web transport")
	}
	for index, message := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		switch role {
		case "", "system", "developer", "user", "assistant":
		case "tool", "function":
			if !promptToolsEnabled {
				return openAIWebUnsupportedParam(fmt.Sprintf("messages[%d].role", index))
			}
		default:
			return openAIWebInvalidParam(fmt.Sprintf("messages[%d].role", index), fmt.Sprintf("unsupported Chat Completions message role %q", role))
		}
		if len(message.ToolCalls) > 0 && !promptToolsEnabled {
			return openAIWebUnsupportedParam(fmt.Sprintf("messages[%d].tool_calls", index))
		}
		if message.FunctionCall != nil {
			if !promptToolsEnabled {
				return openAIWebUnsupportedParam(fmt.Sprintf("messages[%d].function_call", index))
			}
		}
		if strings.TrimSpace(message.ToolCallID) != "" {
			if !promptToolsEnabled {
				return openAIWebUnsupportedParam(fmt.Sprintf("messages[%d].tool_call_id", index))
			}
		}
	}
	return nil
}

// ValidateOpenAIWebResponsesRequest performs the same compatibility check
// before a Responses request is bridged to Chat Completions.
func ValidateOpenAIWebResponsesRequest(req *apicompat.ResponsesRequest) error {
	return ValidateOpenAIWebResponsesRequestWithPromptTools(req, false)
}

// ValidateOpenAIWebResponsesRequestWithPromptTools applies the regular Web
// compatibility checks and optionally enables the prompt-based tool bridge.
func ValidateOpenAIWebResponsesRequestWithPromptTools(req *apicompat.ResponsesRequest, promptToolsEnabled bool) error {
	if req == nil {
		return openAIWebInvalidParam("", "Responses request is nil")
	}
	if err := validateOpenAIWebModel(req.Model); err != nil {
		return err
	}
	// Sampling controls are not part of the classic Web conversation payload.
	// Preserve OpenAI compatibility by accepting valid values and dropping them
	// during the Responses -> Chat -> Web conversion.
	if err := validateOpenAISamplingParameter("temperature", req.Temperature, 0, 2); err != nil {
		return err
	}
	if err := validateOpenAISamplingParameter("top_p", req.TopP, 0, 1); err != nil {
		return err
	}
	if err := validateOpenAIPositiveIntegerParameter("max_output_tokens", req.MaxOutputTokens); err != nil {
		return err
	}
	effectiveTools, effectiveErr := apicompat.EffectiveResponsesTools(req)
	if effectiveErr != nil {
		return openAIWebInvalidParam("tools", effectiveErr.Error())
	}
	if len(effectiveTools) > 0 {
		if !promptToolsEnabled {
			return openAIWebUnsupportedParam("tools")
		}
		if _, err := NewOpenAIWebPromptToolsFromResponsesRequest(req); err != nil {
			return openAIWebInvalidParam("tools", err.Error())
		}
	}
	if !openAIWebNoOpToolChoice(req.ToolChoice) {
		if !promptToolsEnabled {
			return openAIWebUnsupportedParam("tool_choice")
		}
		choiceRaw := normalizeOpenAIWebResponsesPromptToolChoice(req.ToolChoice, effectiveTools)
		if _, _, err := normalizeOpenAIWebPromptToolChoice(choiceRaw, nil); err != nil {
			return openAIWebInvalidParam("tool_choice", err.Error())
		}
	}
	// With no Web tool bridge, parallel_tool_calls has no observable effect.
	if req.Store != nil && *req.Store {
		return openAIWebUnsupportedParam("store")
	}
	if !openAIWebDefaultServiceTier(req.ServiceTier) {
		return openAIWebUnsupportedParam("service_tier")
	}
	// Responses previous_response_id is consumed by the gateway's Web
	// conversation state bridge. It is deliberately not serialized into the
	// private ChatGPT Web payload; the bridge maps it to the stored
	// conversation_id/parent_message_id pair instead.
	if req.Reasoning != nil {
		if !openAIWebReasoningEffortSupported(req.Reasoning.Effort) {
			return openAIWebInvalidParam("reasoning.effort", "reasoning.effort is not supported by ChatGPT web transport")
		}
		if !openAIWebReasoningSummarySupported(req.Reasoning.Summary) {
			return openAIWebInvalidParam("reasoning.summary", "reasoning.summary must be auto, concise, or detailed")
		}
	}
	if req.Text != nil {
		if !openAIWebPlainTextFormat(req.Text.Format) {
			return openAIWebUnsupportedParam("text.format")
		}
		if !openAIWebTextVerbositySupported(req.Text.Verbosity) {
			return openAIWebInvalidParam("text.verbosity", "text.verbosity must be low, medium, or high")
		}
	}
	return nil
}

func openAIWebRawJSONHasValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func validateOpenAISamplingParameter(param string, value *float64, min, max float64) error {
	if value == nil {
		return nil
	}
	if math.IsNaN(*value) || math.IsInf(*value, 0) || *value < min || *value > max {
		return openAIWebInvalidParam(param, fmt.Sprintf("%s must be between %g and %g", param, min, max))
	}
	return nil
}

func validateOpenAIPositiveIntegerParameter(param string, value *int) error {
	if value == nil {
		return nil
	}
	if *value <= 0 {
		return openAIWebInvalidParam(param, fmt.Sprintf("%s must be greater than 0", param))
	}
	return nil
}

func openAIWebNoOpToolChoice(raw json.RawMessage) bool {
	if !openAIWebRawJSONHasValue(raw) {
		return true
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "auto", "none":
		return true
	default:
		return false
	}
}

func openAIWebJSONIsEmptyArray(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("[]"))
}

func openAIWebPlainTextFormat(raw json.RawMessage) bool {
	if !openAIWebRawJSONHasValue(raw) {
		return true
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil || len(value) != 1 {
		return false
	}
	var formatType string
	if err := json.Unmarshal(value["type"], &formatType); err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(formatType), "text")
}

func openAIWebDefaultServiceTier(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto", "default":
		return true
	default:
		return false
	}
}

func openAIWebReasoningEffortSupported(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "extended":
		return true
	default:
		return false
	}
}

func openAIWebReasoningSummarySupported(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto", "concise", "detailed":
		return true
	default:
		return false
	}
}

func openAIWebTextVerbositySupported(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "low", "medium", "high":
		return true
	default:
		return false
	}
}

func (e *OpenAIWebHTTPError) Error() string {
	if e == nil {
		return "chatgpt web upstream request failed"
	}
	endpoint := strings.TrimSpace(e.Endpoint)
	if endpoint == "" {
		endpoint = "chatgpt web"
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = http.StatusText(e.StatusCode)
	}
	if message == "" {
		message = "request failed"
	}
	return fmt.Sprintf("%s: HTTP %d: %s", endpoint, e.StatusCode, message)
}

// OpenAIWebTransport is a deliberately small transport layer.  It owns the
// sentinel handshake and body translation, while HTTPUpstream still owns
// proxy selection, connection pooling, and TLS behavior.
type OpenAIWebTransport struct {
	service   *OpenAIGatewayService
	upstream  HTTPUpstream
	options   OpenAIWebTransportOptions
	jar       http.CookieJar
	mu        sync.Mutex
	bootstrap map[string]OpenAIWebBootstrap
	clients   map[string]*req.Client
	wsDialer  openAIWSClientDialer
}

// UsesOpenAIWebProtocol reports whether an account explicitly opts into the
// classic ChatGPT Web conversation endpoint. Keep the opt-in narrow: OAuth
// and setup-token accounts without the marker must retain the established
// Codex Responses behavior.
func UsesOpenAIWebProtocol(account *Account) bool {
	return account != nil && account.IsOpenAIWebTransport()
}

// OpenAIWebBootstrap contains only non-sensitive data extracted from the web
// shell.  It is used when creating the legacy requirements proof token.
type OpenAIWebBootstrap struct {
	ScriptSources []string
	DataBuild     string
}

// NewOpenAIWebTransport creates an adapter backed by the gateway's existing
// HTTPUpstream.  The service may be nil when callers only need pure payload or
// SSE conversion helpers; network methods then require an explicit upstream.
func NewOpenAIWebTransport(service *OpenAIGatewayService, options OpenAIWebTransportOptions) *OpenAIWebTransport {
	if strings.TrimSpace(options.BaseURL) == "" {
		options.BaseURL = OpenAIWebDefaultBaseURL
	}
	if strings.TrimSpace(options.UserAgent) == "" {
		options.UserAgent = openAIWebDefaultUserAgent
	}
	if strings.TrimSpace(options.ClientVersion) == "" {
		options.ClientVersion = OpenAIWebDefaultClientVersion
	}
	if strings.TrimSpace(options.ClientBuildNumber) == "" {
		options.ClientBuildNumber = OpenAIWebDefaultClientBuildNumber
	}
	if strings.TrimSpace(options.DeviceID) == "" {
		options.DeviceID = uuid.NewString()
	}
	if strings.TrimSpace(options.SessionID) == "" {
		options.SessionID = uuid.NewString()
	}
	if strings.TrimSpace(options.Timezone) == "" {
		options.Timezone = "Asia/Shanghai"
	}
	if options.TimezoneOffsetMin == 0 {
		options.TimezoneOffsetMin = -480
	}
	if options.PowMaxAttempts <= 0 {
		options.PowMaxAttempts = openAIWebDefaultPowAttempts
	}
	if options.TopicReadTimeout <= 0 {
		options.TopicReadTimeout = 60 * time.Second
	}
	transport := &OpenAIWebTransport{
		service:   service,
		options:   options,
		bootstrap: make(map[string]OpenAIWebBootstrap),
		clients:   make(map[string]*req.Client),
		wsDialer:  newDefaultOpenAIWSClientDialer(),
	}
	transport.jar, _ = cookiejar.New(nil)
	if service != nil {
		transport.upstream = service.httpUpstream
	}
	return transport
}

// NewOpenAIWebTransportFromUpstream is useful for tests and small embedders
// that do not construct a full gateway service.
func NewOpenAIWebTransportFromUpstream(upstream HTTPUpstream, options OpenAIWebTransportOptions) *OpenAIWebTransport {
	transport := NewOpenAIWebTransport(nil, options)
	transport.upstream = upstream
	return transport
}

func (s *OpenAIGatewayService) newOpenAIWebTransport() *OpenAIWebTransport {
	if s != nil && s.openAIWebTransportFactory != nil {
		return s.openAIWebTransportFactory()
	}
	return NewOpenAIWebTransport(s, OpenAIWebTransportOptions{})
}

func (t *OpenAIWebTransport) baseURL() string {
	if t == nil {
		return OpenAIWebDefaultBaseURL
	}
	base := strings.TrimRight(strings.TrimSpace(t.options.BaseURL), "/")
	if base == "" {
		return OpenAIWebDefaultBaseURL
	}
	return base
}

func (t *OpenAIWebTransport) proxyFor(account *Account) string {
	if account == nil || account.ProxyID == nil || account.Proxy == nil {
		return ""
	}
	return account.Proxy.URL()
}

func (t *OpenAIWebTransport) concurrencyFor(account *Account) int {
	if account == nil || account.Concurrency <= 0 {
		return 1
	}
	return account.Concurrency
}

func (t *OpenAIWebTransport) endpoint(path string) (string, error) {
	base := t.baseURL()
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid ChatGPT web base URL")
	}
	if !strings.EqualFold(parsed.Scheme, "https") && !strings.EqualFold(parsed.Scheme, "http") {
		return "", fmt.Errorf("unsupported ChatGPT web URL scheme")
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/"), nil
}

func (t *OpenAIWebTransport) commonHeaders(ctx context.Context, account *Account, token, path string) (http.Header, error) {
	headers := make(http.Header)
	headers.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8,en-US;q=0.7")
	headers.Set("Cache-Control", "no-cache")
	headers.Set("Pragma", "no-cache")
	headers.Set("Priority", "u=1, i")
	headers.Set("Origin", t.baseURL())
	headers.Set("Referer", t.baseURL()+"/")
	headers.Set("User-Agent", t.options.UserAgent)
	headers.Set("Sec-Ch-Ua", openAIWebDefaultSecCHUA)
	headers.Set("Sec-Ch-Ua-Arch", `"x86"`)
	headers.Set("Sec-Ch-Ua-Bitness", `"64"`)
	headers.Set("Sec-Ch-Ua-Full-Version", `"143.0.3650.96"`)
	headers.Set("Sec-Ch-Ua-Full-Version-List", `"Microsoft Edge";v="143.0.3650.96", "Chromium";v="143.0.7499.147", "Not A(Brand";v="24.0.0.0"`)
	headers.Set("Sec-Ch-Ua-Mobile", "?0")
	headers.Set("Sec-Ch-Ua-Model", `""`)
	headers.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	headers.Set("Sec-Ch-Ua-Platform-Version", `"19.0.0"`)
	headers.Set("Sec-Fetch-Dest", "empty")
	headers.Set("Sec-Fetch-Mode", "cors")
	headers.Set("Sec-Fetch-Site", "same-origin")
	headers.Set("OAI-Device-Id", t.options.DeviceID)
	headers.Set("OAI-Session-Id", t.options.SessionID)
	headers.Set("OAI-Language", "zh-CN")
	headers.Set("OAI-Client-Version", t.options.ClientVersion)
	headers.Set("OAI-Client-Build-Number", t.options.ClientBuildNumber)
	headers.Set("X-OpenAI-Target-Path", path)
	headers.Set("X-OpenAI-Target-Route", path)
	if t.service != nil {
		auth, err := t.service.buildOpenAIAuthenticationHeaders(ctx, account, token)
		if err != nil {
			return nil, err
		}
		for key, values := range auth {
			for _, value := range values {
				headers.Add(key, value)
			}
		}
		if err := resolveAndSetOpenAIChatGPTAccountHeaders(ctx, t.service.accountRepo, headers, account); err != nil {
			return nil, err
		}
	} else {
		if strings.TrimSpace(token) == "" {
			return nil, errors.New("access token is required")
		}
		headers.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	return headers, nil
}

func (t *OpenAIWebTransport) request(ctx context.Context, method, path, token string, account *Account, body []byte, headers http.Header) (*http.Response, error) {
	if t == nil || (t.service == nil && t.upstream == nil) {
		return nil, errors.New("ChatGPT web transport upstream is not configured")
	}
	endpoint, err := t.endpoint(path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if req.Header.Get("Authorization") == "" && strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	if parsed, parseErr := url.Parse(endpoint); parseErr == nil {
		req.Host = parsed.Host
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	return t.doUpstream(req, account)
}

// doUpstream keeps the web adapter on the same extension point as the normal
// OpenAI path. In particular, configured OAuth plugins must see bootstrap,
// sentinel, and conversation requests too; tests and small embedders without
// a service continue to use the injected HTTPUpstream directly.
func (t *OpenAIWebTransport) doUpstream(req *http.Request, account *Account) (*http.Response, error) {
	if t == nil || req == nil {
		return nil, errors.New("ChatGPT web transport request is nil")
	}
	if t.service == nil {
		if t.upstream == nil {
			return nil, errors.New("ChatGPT web transport upstream is not configured")
		}
		t.addCookies(req)
		resp, err := t.upstream.Do(req, t.proxyFor(account), accountIDForWebTransport(account), t.concurrencyFor(account))
		t.captureCookies(req, resp, err)
		return resp, err
	}
	if account == nil {
		return nil, errors.New("ChatGPT web transport account is required")
	}
	proxyURL := t.proxyFor(account)
	if t.service.pluginManager != nil {
		originalCookies := append([]string(nil), req.Header.Values("Cookie")...)
		t.addCookies(req)
		resp, handled, err := t.service.pluginManager.RoundTripOpenAIOAuth(req.Context(), req, proxyURL, account)
		if handled {
			t.captureCookies(req, resp, err)
			return resp, err
		}
		req.Header.Del("Cookie")
		for _, value := range originalCookies {
			req.Header.Add("Cookie", value)
		}
	}
	client, err := t.browserClient(proxyURL)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}

func (t *OpenAIWebTransport) browserClient(rawProxyURL string) (*req.Client, error) {
	if t == nil {
		return nil, errors.New("ChatGPT web transport upstream is not configured")
	}
	proxyURL, _, err := proxyurl.Parse(rawProxyURL)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if client := t.clients[proxyURL]; client != nil {
		return client, nil
	}
	client := req.C().
		SetTimeout(5 * time.Minute).
		ImpersonateChrome().
		SetCookieJar(t.jar)
	if proxyURL != "" {
		client.SetProxyURL(proxyURL)
	}
	if t.clients == nil {
		t.clients = make(map[string]*req.Client)
	}
	t.clients[proxyURL] = client
	return client, nil
}

func (t *OpenAIWebTransport) addCookies(request *http.Request) {
	if t == nil || t.jar == nil || request == nil || request.URL == nil {
		return
	}
	for _, cookie := range t.jar.Cookies(request.URL) {
		request.AddCookie(cookie)
	}
}

func (t *OpenAIWebTransport) captureCookies(request *http.Request, response *http.Response, requestErr error) {
	if t == nil || t.jar == nil || requestErr != nil || request == nil || request.URL == nil || response == nil {
		return
	}
	t.jar.SetCookies(request.URL, response.Cookies())
}

func accountIDForWebTransport(account *Account) int64 {
	if account == nil {
		return 0
	}
	return account.ID
}

// openAIWebBootstrapCacheKey keeps the bootstrap shell isolated by both the
// account identity and access token. The token is hashed so the in-memory key
// never retains a credential in plain text or in a diagnostic representation.
func openAIWebBootstrapCacheKey(account *Account, token string) string {
	accountKey := "anonymous"
	if account != nil {
		accountKey = fmt.Sprintf("account:%d", account.ID)
	}
	tokenDigest := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return accountKey + ":token:" + hex.EncodeToString(tokenDigest[:])
}

func cloneOpenAIWebBootstrap(value OpenAIWebBootstrap) OpenAIWebBootstrap {
	value.ScriptSources = append([]string(nil), value.ScriptSources...)
	return value
}

// Bootstrap performs the lightweight GET / request used to discover the
// current proof-token script and data-build marker.
func (t *OpenAIWebTransport) Bootstrap(ctx context.Context, account *Account, token string) (OpenAIWebBootstrap, error) {
	if t == nil {
		return OpenAIWebBootstrap{}, errors.New("ChatGPT web transport is nil")
	}
	cacheKey := openAIWebBootstrapCacheKey(account, token)
	t.mu.Lock()
	if t.bootstrap != nil {
		if result, ok := t.bootstrap[cacheKey]; ok {
			result = cloneOpenAIWebBootstrap(result)
			t.mu.Unlock()
			return result, nil
		}
	}
	t.mu.Unlock()
	path := "/"
	headers, err := t.commonHeaders(ctx, account, token, path)
	if err != nil {
		return OpenAIWebBootstrap{}, err
	}
	headers.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	headers.Set("Sec-Fetch-Dest", "document")
	headers.Set("Sec-Fetch-Mode", "navigate")
	headers.Set("Sec-Fetch-Site", "none")
	headers.Set("Sec-Fetch-User", "?1")
	headers.Set("Upgrade-Insecure-Requests", "1")
	resp, err := t.request(ctx, http.MethodGet, path, token, account, nil, headers)
	if err != nil {
		return OpenAIWebBootstrap{}, err
	}
	if resp == nil {
		return OpenAIWebBootstrap{}, errors.New("ChatGPT web bootstrap returned no response")
	}
	body, readErr := readAndCloseWebBody(resp, openAIWebMaxBootstrapBytes)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return OpenAIWebBootstrap{}, webHTTPError(path, resp.StatusCode, body, token)
	}
	if readErr != nil {
		return OpenAIWebBootstrap{}, readErr
	}
	result := parseOpenAIWebBootstrap(string(body))
	t.mu.Lock()
	if t.bootstrap == nil {
		t.bootstrap = make(map[string]OpenAIWebBootstrap)
	}
	t.bootstrap[cacheKey] = cloneOpenAIWebBootstrap(result)
	t.mu.Unlock()
	return cloneOpenAIWebBootstrap(result), nil
}

var openAIWebScriptSrcRE = regexp.MustCompile(`(?is)<script\b[^>]*\bsrc\s*=\s*["']([^"']+)["']`)
var openAIWebDataBuildRE = regexp.MustCompile(`(?is)<html\b[^>]*\bdata-build\s*=\s*["']([^"']+)["']`)

var openAIWebNavigatorKeys = []string{
	"registerProtocolHandler−function registerProtocolHandler() { [native code] }",
	"storage−[object StorageManager]",
	"locks−[object LockManager]",
	"appCodeName−Mozilla",
	"permissions−[object Permissions]",
	"share−function share() { [native code] }",
	"webdriver−false",
	"managed−[object NavigatorManagedData]",
	"canShare−function canShare() { [native code] }",
	"vendor−Google Inc.",
	"mediaDevices−[object MediaDevices]",
	"vibrate−function vibrate() { [native code] }",
	"storageBuckets−[object StorageBucketManager]",
	"mediaCapabilities−[object MediaCapabilities]",
	"cookieEnabled−true",
	"virtualKeyboard−[object VirtualKeyboard]",
	"product−Gecko",
	"presentation−[object Presentation]",
	"onLine−true",
	"mimeTypes−[object MimeTypeArray]",
	"credentials−[object CredentialsContainer]",
	"serviceWorker−[object ServiceWorkerContainer]",
	"keyboard−[object Keyboard]",
	"gpu−[object GPU]",
	"doNotTrack",
	"serial−[object Serial]",
	"pdfViewerEnabled−true",
	"language−zh-CN",
	"geolocation−[object Geolocation]",
	"userAgentData−[object NavigatorUAData]",
	"getUserMedia−function getUserMedia() { [native code] }",
	"sendBeacon−function sendBeacon() { [native code] }",
	"hardwareConcurrency−32",
	"windowControlsOverlay−[object WindowControlsOverlay]",
}

var openAIWebWindowKeys = []string{
	"0", "window", "self", "document", "name", "location", "customElements", "history",
	"navigation", "innerWidth", "innerHeight", "scrollX", "scrollY", "visualViewport", "screenX",
	"screenY", "outerWidth", "outerHeight", "devicePixelRatio", "screen", "chrome", "navigator",
	"onresize", "performance", "crypto", "indexedDB", "sessionStorage", "localStorage", "scheduler",
	"alert", "atob", "btoa", "fetch", "matchMedia", "postMessage", "queueMicrotask",
	"requestAnimationFrame", "setInterval", "setTimeout", "caches", "__NEXT_DATA__", "__BUILD_MANIFEST",
	"__NEXT_PRELOADREADY",
}

var openAIWebDocumentKeys = []string{"__reactContainer$fzelfjyxej8", "_reactListening5dehydibo78", "location"}
var openAIWebScreenResolutions = [][2]int{{1920, 1080}, {1440, 900}, {2560, 1440}, {3840, 2160}}
var openAIWebCoreCounts = []int{8, 16, 24, 32}

func openAIWebRandomIndex(size int) int {
	if size <= 1 {
		return 0
	}
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err == nil {
		var value uint64
		for _, b := range raw {
			value = (value << 8) | uint64(b)
		}
		return int(value % uint64(size))
	}
	return mathrand.Intn(size)
}

func openAIWebRandomFloat() float64 {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err == nil {
		var value uint64
		for _, b := range raw {
			value = (value << 8) | uint64(b)
		}
		return float64(value>>11) / float64(uint64(1)<<53)
	}
	return mathrand.Float64()
}

func buildOpenAIWebPowConfig(userAgent string, scriptSources []string, dataBuild string) []any {
	if strings.TrimSpace(userAgent) == "" {
		userAgent = openAIWebDefaultUserAgent
	}
	scriptSource := "https://chatgpt.com/backend-api/sentinel/sdk.js"
	if len(scriptSources) > 0 {
		candidates := make([]string, 0, len(scriptSources))
		for _, source := range scriptSources {
			if source = strings.TrimSpace(source); source != "" {
				candidates = append(candidates, source)
			}
		}
		if len(candidates) > 0 {
			scriptSource = candidates[openAIWebRandomIndex(len(candidates))]
		}
	}
	resolution := openAIWebScreenResolutions[openAIWebRandomIndex(len(openAIWebScreenResolutions))]
	// The legacy browser payload uses an Eastern-time JavaScript date string,
	// independent of the gateway host's local timezone.
	eastern := time.FixedZone("EST", -5*60*60)
	legacyTime := time.Now().In(eastern).Format("Mon Jan 02 2006 15:04:05 GMT-0500 (Eastern Standard Time)")
	nowMillis := float64(time.Now().UnixMilli())
	perfMillis := float64(time.Now().UnixNano()%900000000) / 1e6
	return []any{
		resolution[0] + resolution[1],
		legacyTime,
		4294705152,
		// Keep the browser-type flag aligned with the reference sentinel client.
		// The legacy payload's fourth item is currently expected to be 1.
		1,
		userAgent,
		scriptSource,
		strings.TrimSpace(dataBuild),
		"en-US",
		"en-US,es-US,en,es",
		openAIWebRandomFloat(),
		openAIWebNavigatorKeys[openAIWebRandomIndex(len(openAIWebNavigatorKeys))],
		openAIWebDocumentKeys[openAIWebRandomIndex(len(openAIWebDocumentKeys))],
		openAIWebWindowKeys[openAIWebRandomIndex(len(openAIWebWindowKeys))],
		perfMillis,
		uuid.NewString(),
		"",
		openAIWebCoreCounts[openAIWebRandomIndex(len(openAIWebCoreCounts))],
		nowMillis - perfMillis,
		0, 0, 0, 0, 0, 0, 0,
	}
}

func parseOpenAIWebBootstrap(html string) OpenAIWebBootstrap {
	result := OpenAIWebBootstrap{}
	seen := make(map[string]struct{})
	for _, match := range openAIWebScriptSrcRE.FindAllStringSubmatch(html, -1) {
		if len(match) < 2 {
			continue
		}
		src := strings.TrimSpace(match[1])
		if src == "" {
			continue
		}
		if _, ok := seen[src]; ok {
			continue
		}
		seen[src] = struct{}{}
		result.ScriptSources = append(result.ScriptSources, src)
	}
	if match := openAIWebDataBuildRE.FindStringSubmatch(html); len(match) > 1 {
		result.DataBuild = strings.TrimSpace(match[1])
	}
	if len(result.ScriptSources) == 0 {
		result.ScriptSources = []string{"https://chatgpt.com/backend-api/sentinel/sdk.js"}
	}
	return result
}

// BuildLegacyRequirementsToken creates the browser proof envelope accepted by
// the authenticated sentinel prepare endpoint.  It contains no credential
// material and is safe to regenerate for every request.
func BuildOpenAIWebLegacyRequirementsToken(userAgent string, scriptSources []string, dataBuild string) string {
	config := buildOpenAIWebPowConfig(userAgent, scriptSources, dataBuild)
	payload, err := json.Marshal(config)
	if err != nil {
		// All values above are JSON primitives; this fallback is defensive only.
		payload = []byte("[]")
	}
	return "gAAAAAC" + base64.StdEncoding.EncodeToString(payload)
}

// BuildOpenAIWebProofToken solves the small SHA3 proof-of-work challenge used
// by the sentinel endpoint.  A bounded attempt count prevents a hostile
// difficulty from monopolizing a gateway worker.
func BuildOpenAIWebProofToken(seed, difficulty, userAgent string, scriptSources []string, dataBuild string, maxAttempts int) (string, error) {
	seed = strings.TrimSpace(seed)
	difficulty = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(difficulty, "0x"), "0X"))
	if seed == "" || difficulty == "" {
		return "", errors.New("proof-of-work seed or difficulty is missing")
	}
	target, err := hex.DecodeString(difficulty)
	if err != nil || len(target) == 0 {
		return "", errors.New("proof-of-work difficulty is invalid")
	}
	if maxAttempts <= 0 {
		maxAttempts = openAIWebDefaultPowAttempts
	}
	// Decode the legacy envelope so the nonce fields can be replaced in the
	// same positions used by the browser implementation.
	legacy := BuildOpenAIWebLegacyRequirementsToken(userAgent, scriptSources, dataBuild)
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(legacy, "gAAAAAC"))
	if err != nil {
		return "", errors.New("failed to build proof-of-work configuration")
	}
	var config []any
	if err := json.Unmarshal(raw, &config); err != nil || len(config) < 10 {
		return "", errors.New("failed to parse proof-of-work configuration")
	}
	for nonce := 0; nonce < maxAttempts; nonce++ {
		candidate := append([]any(nil), config...)
		candidate[3] = nonce
		candidate[9] = nonce >> 1
		encodedJSON, marshalErr := json.Marshal(candidate)
		if marshalErr != nil {
			continue
		}
		encoded := base64.StdEncoding.EncodeToString(encodedJSON)
		hash := sha3.New512()
		_, _ = hash.Write([]byte(seed))
		_, _ = hash.Write([]byte(encoded))
		digest := hash.Sum(nil)
		compareLen := len(target)
		if compareLen > len(digest) {
			compareLen = len(digest)
		}
		if bytes.Compare(digest[:compareLen], target[:compareLen]) <= 0 {
			return "gAAAAAB" + encoded, nil
		}
	}
	return "", fmt.Errorf("proof-of-work search exhausted after %d attempts", maxAttempts)
}

func webHTTPError(endpoint string, status int, body []byte, secrets ...string) error {
	message := extractOpenAIWebErrorMessage(body)
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "<redacted-token>")
		}
	}
	return &OpenAIWebHTTPError{Endpoint: endpoint, StatusCode: status, Message: message}
}

func extractOpenAIWebErrorMessage(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "request failed"
	}
	var payload any
	if json.Unmarshal(body, &payload) == nil {
		if msg := findOpenAIWebMessage(payload); msg != "" {
			return redactOpenAIWebSecret(msg)
		}
	}
	return redactOpenAIWebSecret(truncateString(trimmed, 512))
}

func findOpenAIWebMessage(value any) string {
	switch current := value.(type) {
	case map[string]any:
		for _, key := range []string{"message", "error", "detail", "error_message"} {
			if candidate, ok := current[key]; ok {
				if msg := findOpenAIWebMessage(candidate); msg != "" {
					return msg
				}
			}
		}
	case string:
		return strings.TrimSpace(current)
	case []any:
		for _, item := range current {
			if msg := findOpenAIWebMessage(item); msg != "" {
				return msg
			}
		}
	}
	return ""
}

func redactOpenAIWebSecret(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if idx := strings.Index(strings.ToLower(value), "bearer "); idx >= 0 {
		value = strings.TrimSpace(value[:idx]) + "Bearer <redacted>"
	}
	parts := strings.Split(value, ".")
	if len(parts) == 3 && len(parts[0]) >= 12 && len(parts[1]) >= 8 && len(parts[2]) >= 8 {
		return "<redacted-jwt>"
	}
	return value
}

func readAndCloseWebBody(resp *http.Response, limit int64) ([]byte, error) {
	if resp == nil {
		return nil, errors.New("nil web response")
	}
	if resp.Body == nil {
		return nil, errors.New("nil web response body")
	}
	defer func() { _ = resp.Body.Close() }()
	if limit <= 0 {
		limit = openAIWebMaxResponseErrorBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return body[:limit], errors.New("ChatGPT web response body exceeds limit")
	}
	return body, nil
}

// GetRequirements executes the authenticated sentinel prepare/finalize flow.
// Arkose still requires an interactive solver; the data-driven Turnstile
// program returned by the Web client is evaluated locally.
func (t *OpenAIWebTransport) GetRequirements(ctx context.Context, account *Account, token string) (OpenAIWebRequirements, error) {
	if t == nil {
		return OpenAIWebRequirements{}, errors.New("ChatGPT web transport is nil")
	}
	if strings.TrimSpace(token) == "" {
		return OpenAIWebRequirements{}, errors.New("access token is required")
	}
	bootstrap := OpenAIWebBootstrap{ScriptSources: []string{"https://chatgpt.com/backend-api/sentinel/sdk.js"}}
	if t != nil && !t.options.SkipBootstrap {
		var err error
		bootstrap, err = t.Bootstrap(ctx, account, token)
		if err != nil {
			return OpenAIWebRequirements{}, err
		}
	}
	pToken := BuildOpenAIWebLegacyRequirementsToken(t.options.UserAgent, bootstrap.ScriptSources, bootstrap.DataBuild)
	preparePath := OpenAIWebRequirementsPath + "/prepare"
	headers, err := t.commonHeaders(ctx, account, token, preparePath)
	if err != nil {
		return OpenAIWebRequirements{}, err
	}
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "application/json")
	prepareBody, _ := json.Marshal(map[string]string{"p": pToken})
	resp, err := t.request(ctx, http.MethodPost, preparePath, token, account, prepareBody, headers)
	if err != nil {
		return OpenAIWebRequirements{}, err
	}
	if resp == nil {
		return OpenAIWebRequirements{}, errors.New("ChatGPT web prepare returned no response")
	}
	prepareRaw, readErr := readAndCloseWebBody(resp, openAIWebMaxResponseErrorBytes)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return OpenAIWebRequirements{}, webHTTPError(preparePath, resp.StatusCode, prepareRaw, token)
	}
	if readErr != nil {
		return OpenAIWebRequirements{}, fmt.Errorf("ChatGPT web prepare response: %w", readErr)
	}
	var prepare map[string]any
	if err := json.Unmarshal(prepareRaw, &prepare); err != nil {
		return OpenAIWebRequirements{}, errors.New("ChatGPT web prepare response is invalid")
	}
	if requiredBool(prepare, "arkose", "required") {
		return OpenAIWebRequirements{}, &OpenAIWebChallengeError{Kind: "arkose", Endpoint: preparePath}
	}
	proofToken := ""
	if requiredBool(prepare, "proofofwork", "required") {
		proof, _ := prepare["proofofwork"].(map[string]any)
		seed, _ := proof["seed"].(string)
		difficulty, _ := proof["difficulty"].(string)
		proofToken, err = BuildOpenAIWebProofToken(seed, difficulty, t.options.UserAgent, bootstrap.ScriptSources, bootstrap.DataBuild, t.options.PowMaxAttempts)
		if err != nil {
			return OpenAIWebRequirements{}, err
		}
	}
	turnstileToken := ""
	if requiredBool(prepare, "turnstile", "required") {
		turnstile, _ := prepare["turnstile"].(map[string]any)
		dx, _ := turnstile["dx"].(string)
		var turnstileErr error
		turnstileToken, turnstileErr = SolveOpenAIWebTurnstileToken(dx, pToken)
		if turnstileErr != nil && !errors.Is(turnstileErr, errOpenAIWebTurnstileNoToken) {
			return OpenAIWebRequirements{}, fmt.Errorf("ChatGPT web turnstile challenge: %w", turnstileErr)
		}
	}
	prepareToken, _ := prepare["prepare_token"].(string)
	if strings.TrimSpace(prepareToken) == "" {
		prepareToken, _ = prepare["token"].(string)
	}
	finalizePath := OpenAIWebRequirementsPath + "/finalize"
	finalizeHeaders, err := t.commonHeaders(ctx, account, token, finalizePath)
	if err != nil {
		return OpenAIWebRequirements{}, err
	}
	finalizeHeaders.Set("Content-Type", "application/json")
	finalizeHeaders.Set("Accept", "application/json")
	finalizeBody, _ := json.Marshal(map[string]string{
		"prepare_token": prepareToken,
		"proofofwork":   proofToken,
		"turnstile":     turnstileToken,
	})
	finalizeResp, err := t.request(ctx, http.MethodPost, finalizePath, token, account, finalizeBody, finalizeHeaders)
	if err != nil {
		return OpenAIWebRequirements{}, err
	}
	if finalizeResp == nil {
		return OpenAIWebRequirements{}, errors.New("ChatGPT web finalize returned no response")
	}
	finalizeRaw, readErr := readAndCloseWebBody(finalizeResp, openAIWebMaxResponseErrorBytes)
	if finalizeResp.StatusCode < http.StatusOK || finalizeResp.StatusCode >= http.StatusMultipleChoices {
		return OpenAIWebRequirements{}, webHTTPError(finalizePath, finalizeResp.StatusCode, finalizeRaw, token)
	}
	if readErr != nil {
		return OpenAIWebRequirements{}, fmt.Errorf("ChatGPT web finalize response: %w", readErr)
	}
	var finalized map[string]any
	if err := json.Unmarshal(finalizeRaw, &finalized); err != nil {
		return OpenAIWebRequirements{}, errors.New("ChatGPT web finalize response is invalid")
	}
	tokenValue, _ := finalized["token"].(string)
	if strings.TrimSpace(tokenValue) == "" {
		return OpenAIWebRequirements{}, errors.New("ChatGPT web finalize response omitted requirements token")
	}
	soToken, _ := finalized["so_token"].(string)
	return OpenAIWebRequirements{
		Token:          strings.TrimSpace(tokenValue),
		PrepareToken:   strings.TrimSpace(prepareToken),
		ProofToken:     strings.TrimSpace(proofToken),
		TurnstileToken: strings.TrimSpace(turnstileToken),
		SOToken:        strings.TrimSpace(soToken),
	}, nil
}

func requiredBool(root map[string]any, key, nested string) bool {
	child, _ := root[key].(map[string]any)
	value, _ := child[nested].(bool)
	return value
}

// OpenAIWebConversationOptions controls conversion of a Chat Completions
// request into the classic web conversation envelope.
type OpenAIWebConversationOptions struct {
	Request *apicompat.ChatCompletionsRequest
	// PromptTools is populated only when the administrator-enabled Web Prompt
	// Tool bridge is active. It carries request-scoped nonce/schema state.
	PromptTools     *OpenAIWebPromptTools
	ConversationID  string
	ParentMessageID string
	// TurnTraceID is shared by the browser's conversation/prepare and
	// conversation requests. Leave it empty to generate one per transaction.
	TurnTraceID       string
	Timezone          string
	TimezoneOffsetMin int
}

// BuildConversationPayload converts a Chat Completions request without doing
// any network work.  It is the preferred unit-test and integration seam.
func (t *OpenAIWebTransport) BuildConversationPayload(req *apicompat.ChatCompletionsRequest) ([]byte, error) {
	return t.BuildConversationPayloadWithOptions(OpenAIWebConversationOptions{Request: req})
}

func (t *OpenAIWebTransport) BuildConversationPayloadWithOptions(options OpenAIWebConversationOptions) ([]byte, error) {
	return t.buildConversationPayload(context.Background(), nil, "", options)
}

func (t *OpenAIWebTransport) buildConversationPayload(ctx context.Context, account *Account, token string, options OpenAIWebConversationOptions) ([]byte, error) {
	if t == nil {
		return nil, errors.New("ChatGPT web transport is nil")
	}
	if options.Request == nil {
		return nil, errors.New("Chat Completions request is nil")
	}
	// Work on a request-local copy. Prompt Tools uses the public tool
	// declaration to build its instruction and response parser, but the classic
	// Web endpoint rejects native tool parameters outright. Keeping this copy
	// boundary makes it impossible for future callers to leak tools back into
	// the private request after the bridge has been selected.
	requestValue := *options.Request
	request := &requestValue
	if err := ValidateOpenAIWebChatCompletionsRequestWithPromptTools(request, options.PromptTools != nil); err != nil {
		return nil, err
	}
	if options.PromptTools != nil {
		request.Tools = nil
		request.Functions = nil
		request.ToolChoice = nil
		request.FunctionCall = nil
		request.ParallelToolCalls = nil
	}
	model, ok := NormalizeOpenAIWebModel(request.Model)
	if !ok {
		return nil, openAIWebInvalidParam("model", fmt.Sprintf("model %q is not supported by ChatGPT web transport", strings.TrimSpace(request.Model)))
	}
	messages, err := openAIWebMessagesFromChatRequestWithPromptTools(ctx, account, token, t, request, options.PromptTools)
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return nil, errors.New("Chat Completions request has no messages")
	}
	if options.PromptTools != nil {
		prompt := openAIWebMessage("system", options.PromptTools.Instruction(), nil)
		messages = append([]map[string]any{prompt}, messages...)
	}
	timezone := strings.TrimSpace(options.Timezone)
	if timezone == "" {
		timezone = t.options.Timezone
	}
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	offset := options.TimezoneOffsetMin
	if offset == 0 {
		offset = t.options.TimezoneOffsetMin
	}
	if offset == 0 {
		offset = -480
	}
	parentID := strings.TrimSpace(options.ParentMessageID)
	if parentID == "" {
		parentID = uuid.NewString()
	}
	workMode := (&Account{}).IsOpenAIWebWorkModeModel(model)
	if account != nil {
		workMode = account.IsOpenAIWebWorkModeModel(model)
	}
	payload := map[string]any{
		"action":                               "next",
		"messages":                             messages,
		"model":                                model,
		"parent_message_id":                    parentID,
		"conversation_mode":                    map[string]any{"kind": "primary_assistant"},
		"supported_encodings":                  []string{"v1"},
		"system_hints":                         []any{},
		"client_prepare_state":                 "success",
		"enable_message_followups":             true,
		"supports_buffering":                   true,
		"local_function_names":                 []string{"local.continue_in_work"},
		"paragen_cot_summary_display_override": "allow",
		"force_parallel_switch":                "auto",
		"timezone":                             timezone,
		"timezone_offset_min":                  offset,
		"client_contextual_info": map[string]any{
			"is_dark_mode":                     false,
			"time_since_loaded":                120,
			"page_height":                      900,
			"page_width":                       1400,
			"pixel_ratio":                      2,
			"screen_height":                    1440,
			"screen_width":                     2560,
			"app_name":                         "chatgpt.com",
			"has_web_push_capabilities":        true,
			"web_push_notification_permission": "default",
		},
	}
	if conversationID := strings.TrimSpace(options.ConversationID); conversationID != "" {
		payload["conversation_id"] = conversationID
	}
	if workMode {
		payload["conversation_origin"] = "tpp"
		payload["model_response_contracts"] = openAIWebModelResponseContracts()
		effort := normalizeOpenAIWebThinkingEffort(request.ReasoningEffort)
		if effort == "" {
			effort = "min"
		}
		payload["thinking_effort"] = effort
	}
	return json.Marshal(payload)
}

func normalizeOpenAIWebThinkingEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "minimal", "low", "medium", "high":
		if strings.EqualFold(strings.TrimSpace(value), "minimal") {
			return "min"
		}
		return strings.ToLower(strings.TrimSpace(value))
	case "xhigh", "extended":
		return "extended"
	default:
		return ""
	}
}

func openAIWebMessagesFromChatRequest(request *apicompat.ChatCompletionsRequest) ([]map[string]any, error) {
	return openAIWebMessagesFromChatRequestWithTransport(context.Background(), nil, "", nil, request)
}

type openAIWebAttachment struct {
	ID          string
	Name        string
	MIMEType    string
	MIMETypeKey string
	// PointerScheme preserves the asset namespace expected by the Web client.
	// Browser-uploaded composer files use sediment; existing file-service IDs
	// remain valid and are kept as-is.
	PointerScheme string
	LibraryFileID string
	// Source distinguishes a newly uploaded local composer file from a file
	// selected from the user's library. The Web API uses this value in the
	// attachment metadata even when a processing event also returns a library
	// object id.
	Source    string
	SizeBytes int
	Width     int
	Height    int
	Data      []byte
}

type openAIWebMessageContent struct {
	Text        string
	Attachments []openAIWebAttachment
}

func openAIWebMessagesFromChatRequestWithTransport(ctx context.Context, account *Account, token string, transport *OpenAIWebTransport, request *apicompat.ChatCompletionsRequest) ([]map[string]any, error) {
	return openAIWebMessagesFromChatRequestWithPromptTools(ctx, account, token, transport, request, nil)
}

func openAIWebMessagesFromChatRequestWithPromptTools(ctx context.Context, account *Account, token string, transport *OpenAIWebTransport, request *apicompat.ChatCompletionsRequest, promptTools *OpenAIWebPromptTools) ([]map[string]any, error) {
	if request == nil {
		return nil, errors.New("Chat Completions request is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	messages := make([]map[string]any, 0, len(request.Messages)+1)
	if strings.TrimSpace(request.Instructions) != "" {
		messages = append(messages, openAIWebMessage("system", request.Instructions, nil))
	}
	for _, message := range request.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role == "" {
			role = "user"
		}
		if role == "developer" {
			role = "system"
		}
		if role == "tool" {
			// The classic endpoint has no public tool role. Preserve the result as
			// a user-authored text turn and retain the correlation metadata.
			role = "user"
		}
		content, err := openAIWebMessageContentFromRaw(message.Content)
		if err != nil {
			return nil, err
		}
		if promptTools != nil {
			if len(message.ToolCalls) > 0 {
				content.Text = promptTools.EncodeAssistantToolCalls(message.ToolCalls)
			} else if message.FunctionCall != nil {
				content.Text = promptTools.EncodeAssistantToolCalls([]apicompat.ChatToolCall{{
					Type:     "function",
					Function: *message.FunctionCall,
				}})
			}
			if strings.EqualFold(strings.TrimSpace(message.Role), "tool") {
				content.Text = promptTools.EncodeToolResult(message.ToolCallID, content.Text)
			}
		}
		metadata := map[string]any{}
		if message.Name != "" {
			metadata["name"] = message.Name
		}
		// The classic Web conversation schema has no native tool metadata.
		// Prompt Tool history is encoded in the message text above; forwarding
		// these public OpenAI fields as metadata can make the upstream reject the
		// conversation body even when top-level native fields were removed.
		if promptTools == nil {
			if message.ToolCallID != "" {
				metadata["tool_call_id"] = message.ToolCallID
			}
			if len(message.ToolCalls) > 0 {
				metadata["tool_calls"] = message.ToolCalls
			}
		}
		if len(content.Attachments) == 0 {
			messages = append(messages, openAIWebMessage(role, content.Text, metadata))
			continue
		}
		if len(content.Attachments) > openAIWebMaxAttachmentCount {
			return nil, fmt.Errorf("ChatGPT web transport supports at most %d attachments per message", openAIWebMaxAttachmentCount)
		}
		resolved := make([]openAIWebAttachment, 0, len(content.Attachments))
		for index, attachment := range content.Attachments {
			if attachment.ID == "" {
				if transport == nil || strings.TrimSpace(token) == "" {
					return nil, errors.New("ChatGPT web attachment upload requires an access token")
				}
				uploaded, uploadErr := transport.uploadWebAttachment(ctx, account, token, attachment, index+1)
				if uploadErr != nil {
					return nil, uploadErr
				}
				attachment = uploaded
			}
			resolved = append(resolved, attachment)
		}
		metadata["attachments"] = openAIWebAttachmentMetadata(resolved)
		messages = append(messages, openAIWebMultimodalMessage(role, content.Text, resolved, metadata))
	}
	return messages, nil
}

func openAIWebMessage(role, text string, metadata map[string]any) map[string]any {
	metadata = openAIWebMessageMetadata(metadata)
	message := map[string]any{
		"id":          uuid.NewString(),
		"author":      map[string]any{"role": role},
		"create_time": float64(time.Now().UnixNano()) / 1e9,
		"content":     map[string]any{"content_type": "text", "parts": []string{text}},
		"metadata":    metadata,
	}
	return message
}

func openAIWebMultimodalMessage(role, text string, attachments []openAIWebAttachment, metadata map[string]any) map[string]any {
	parts := make([]any, 0, len(attachments)+1)
	for _, attachment := range attachments {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(attachment.MIMEType)), "image/") {
			continue
		}
		parts = append(parts, map[string]any{
			"content_type":  "image_asset_pointer",
			"asset_pointer": openAIWebAttachmentPointer(attachment),
			"width":         attachment.Width,
			"height":        attachment.Height,
			"size_bytes":    attachment.SizeBytes,
		})
	}
	if text != "" || len(parts) == 0 {
		parts = append(parts, text)
	}
	content := map[string]any{"content_type": "text", "parts": []string{text}}
	if len(parts) > 0 {
		if _, hasImagePointer := parts[0].(map[string]any); hasImagePointer {
			content = map[string]any{"content_type": "multimodal_text", "parts": parts}
		}
	}
	message := map[string]any{
		"id":          uuid.NewString(),
		"author":      map[string]any{"role": role},
		"create_time": float64(time.Now().UnixNano()) / 1e9,
		"content":     content,
		"metadata":    openAIWebMessageMetadata(metadata),
	}
	return message
}

func openAIWebMessageMetadata(metadata map[string]any) map[string]any {
	result := make(map[string]any, len(metadata)+2)
	for key, value := range metadata {
		result[key] = value
	}
	if _, exists := result["selected_sources"]; !exists {
		result["selected_sources"] = []any{}
	}
	if _, exists := result["serialization_metadata"]; !exists {
		result["serialization_metadata"] = map[string]any{"custom_symbol_offsets": []any{}}
	}
	return result
}

// openAIWebModelResponseContracts mirrors the contract advertised by the
// Plus composer. Keep the value request-scoped so callers cannot mutate a
// shared map between concurrent conversations.
func openAIWebModelResponseContracts() []map[string]any {
	return []map[string]any{{
		"id":               "photo_upload_action.v1",
		"protocol_version": 1,
		"presets":          []string{"cap:image", "cap:file", "placement:end"},
	}}
}

func openAIWebAttachmentPointer(attachment openAIWebAttachment) string {
	scheme := strings.ToLower(strings.TrimSpace(attachment.PointerScheme))
	if scheme != "sediment" && scheme != "file-service" {
		scheme = "file-service"
	}
	return scheme + "://" + attachment.ID
}

func openAIWebAttachmentMetadata(attachments []openAIWebAttachment) []map[string]any {
	metadata := make([]map[string]any, 0, len(attachments))
	for _, attachment := range attachments {
		entry := map[string]any{
			"id":           attachment.ID,
			"name":         attachment.Name,
			"size":         attachment.SizeBytes,
			"is_big_paste": false,
		}
		if attachment.Width > 0 && attachment.Height > 0 {
			entry["width"] = attachment.Width
			entry["height"] = attachment.Height
		}
		mimeKey := strings.TrimSpace(attachment.MIMETypeKey)
		if mimeKey != "mime_type" && mimeKey != "mimeType" {
			mimeKey = "mimeType"
		}
		entry[mimeKey] = attachment.MIMEType
		metadata = append(metadata, entry)
		if source := strings.TrimSpace(attachment.Source); source != "" {
			metadata[len(metadata)-1]["source"] = source
		} else if attachment.LibraryFileID != "" {
			// Preserve the legacy meaning for caller-provided library files.
			metadata[len(metadata)-1]["source"] = "library"
		}
		if attachment.LibraryFileID != "" {
			metadata[len(metadata)-1]["library_file_id"] = attachment.LibraryFileID
		}
	}
	return metadata
}

func openAIWebMessageText(content json.RawMessage) (string, error) {
	parsed, err := openAIWebMessageContentFromRaw(content)
	if err != nil {
		return "", err
	}
	if len(parsed.Attachments) > 0 {
		return "", errors.New("ChatGPT web message contains an attachment; use an authenticated web transport")
	}
	return parsed.Text, nil
}

func openAIWebMessageContentFromRaw(content json.RawMessage) (openAIWebMessageContent, error) {
	if len(bytes.TrimSpace(content)) == 0 || bytes.Equal(bytes.TrimSpace(content), []byte("null")) {
		return openAIWebMessageContent{}, nil
	}
	var text string
	if json.Unmarshal(content, &text) == nil {
		return openAIWebMessageContent{Text: text}, nil
	}
	var parts []any
	if err := json.Unmarshal(content, &parts); err != nil {
		return openAIWebMessageContent{}, errors.New("web conversation message content must be a string or array")
	}
	result := openAIWebMessageContent{}
	var builder strings.Builder
	for index, item := range parts {
		part, ok := item.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := part["type"].(string)
		switch strings.ToLower(strings.TrimSpace(typ)) {
		case "text", "input_text":
			if value, ok := part["text"].(string); ok {
				builder.WriteString(value)
			}
		case "image_url", "input_image", "image":
			attachment, err := openAIWebImageAttachmentFromPart(part, index+1)
			if err != nil {
				return openAIWebMessageContent{}, err
			}
			result.Attachments = append(result.Attachments, attachment)
		case "file", "input_file":
			attachment, err := openAIWebFileAttachmentFromPart(part, index+1)
			if err != nil {
				return openAIWebMessageContent{}, err
			}
			result.Attachments = append(result.Attachments, attachment)
		default:
			if value, ok := part["text"].(string); ok {
				builder.WriteString(value)
			}
		}
	}
	result.Text = builder.String()
	return result, nil
}

func openAIWebImageAttachmentFromPart(part map[string]any, index int) (openAIWebAttachment, error) {
	raw := openAIWebPartString(part["image_url"], "url", "image_url")
	if raw == "" {
		raw = openAIWebPartString(part["url"], "url")
	}
	if raw == "" {
		if value, ok := part["data"].(string); ok {
			raw = value
		}
	}
	if raw == "" {
		return openAIWebAttachment{}, errors.New("ChatGPT web image attachment is missing image_url")
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "data:") {
		return openAIWebAttachment{}, errors.New("ChatGPT web transport does not support remote attachment URLs")
	}
	mimeType, data, err := decodeOpenAIWebDataURI(raw)
	if err != nil {
		return openAIWebAttachment{}, err
	}
	if !strings.HasPrefix(mimeType, "image/") {
		return openAIWebAttachment{}, fmt.Errorf("ChatGPT web image attachment has unsupported MIME type %q", mimeType)
	}
	name := openAIWebPartString(part["filename"], "name")
	if name == "" {
		name = fmt.Sprintf("image_%d.%s", index, openAIWebMIMEExtension(mimeType))
	}
	width, height := openAIWebImageDimensions(data)
	return openAIWebAttachment{
		Name: name, MIMEType: mimeType, MIMETypeKey: "mimeType", PointerScheme: "file-service",
		SizeBytes: len(data), Width: width, Height: height, Data: data,
	}, nil
}

func openAIWebFileAttachmentFromPart(part map[string]any, index int) (openAIWebAttachment, error) {
	fileValue := part["file"]
	fileObject, _ := fileValue.(map[string]any)
	if fileObject == nil {
		fileObject = part
	}
	rawFileID := openAIWebPartString(fileObject["file_id"], "id")
	pointerScheme := "file-service"
	if strings.HasPrefix(strings.ToLower(rawFileID), "sediment://") {
		pointerScheme = "sediment"
	}
	fileID, err := normalizeOpenAIWebFileID(rawFileID)
	if err != nil {
		return openAIWebAttachment{}, err
	}
	name := openAIWebPartString(fileObject["filename"], "name", "file_name")
	mimeType := strings.ToLower(strings.TrimSpace(openAIWebPartString(fileObject["mime_type"], "mimeType", "type")))
	if mimeType == "file" || mimeType == "input_file" {
		mimeType = ""
	}
	// A caller-provided file ID already points at an uploaded ChatGPT asset.
	// Prefer it over an optional inline file_data value so retries or mixed
	// client payloads never upload the same file a second time.
	if fileID != "" {
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		if name == "" {
			name = "attachment"
		}
		mimeKey := "mime_type"
		if pointerScheme == "file-service" && strings.HasPrefix(mimeType, "image/") {
			mimeKey = "mimeType"
		}
		return openAIWebAttachment{ID: fileID, Name: name, MIMEType: mimeType, MIMETypeKey: mimeKey, PointerScheme: pointerScheme}, nil
	}
	dataURI := openAIWebPartString(fileObject["file_data"], "data")
	if dataURI != "" {
		parsedMIME, data, decodeErr := decodeOpenAIWebDataURI(dataURI)
		if decodeErr != nil {
			return openAIWebAttachment{}, decodeErr
		}
		// The data-URI media type is the authoritative type for bytes that are
		// about to be uploaded; ignore a stale client-side hint if it differs.
		mimeType = parsedMIME
		if name == "" {
			name = fmt.Sprintf("attachment_%d.%s", index, openAIWebMIMEExtension(mimeType))
		}
		width, height := openAIWebImageDimensions(data)
		if !strings.HasPrefix(mimeType, "image/") {
			width, height = 0, 0
		}
		return openAIWebAttachment{
			Name: name, MIMEType: mimeType, MIMETypeKey: "mime_type", PointerScheme: "sediment",
			SizeBytes: len(data), Width: width, Height: height, Data: data,
		}, nil
	}
	return openAIWebAttachment{}, errors.New("ChatGPT web file attachment requires file_data or file_id")
}

func openAIWebPartString(value any, nestedKeys ...string) string {
	switch current := value.(type) {
	case string:
		return strings.TrimSpace(current)
	case map[string]any:
		for _, key := range nestedKeys {
			if text, ok := current[key].(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

var openAIWebFileIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func normalizeOpenAIWebFileID(value string) (string, error) {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	for _, prefix := range []string{"file-service://", "sediment://"} {
		if strings.HasPrefix(lower, prefix) {
			value = value[len(prefix):]
			break
		}
	}
	if value == "" {
		return "", nil
	}
	if !openAIWebFileIDRE.MatchString(value) {
		return "", errors.New("ChatGPT web file_id contains unsupported characters")
	}
	return value, nil
}

func decodeOpenAIWebDataURI(value string) (string, []byte, error) {
	value = strings.TrimSpace(value)
	header, encoded, ok := strings.Cut(value, ",")
	if !ok || !strings.HasPrefix(strings.ToLower(header), "data:") {
		return "", nil, errors.New("ChatGPT web attachment must be a base64 data URI")
	}
	parameters := strings.Split(strings.TrimSpace(header[len("data:"):]), ";")
	if len(parameters) == 0 || strings.TrimSpace(parameters[0]) == "" {
		return "", nil, errors.New("ChatGPT web attachment data URI is missing a MIME type")
	}
	// The bare `base64` marker is a data-URI flag rather than a MIME
	// parameter, so only parse the media type itself through mime.ParseMediaType.
	mimeType, _, err := mime.ParseMediaType(strings.TrimSpace(parameters[0]))
	if err != nil || strings.TrimSpace(mimeType) == "" {
		return "", nil, errors.New("ChatGPT web attachment data URI has an invalid MIME type")
	}
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	base64Marker := false
	for _, parameter := range parameters[1:] {
		if strings.EqualFold(strings.TrimSpace(parameter), "base64") {
			base64Marker = true
			break
		}
	}
	if !base64Marker {
		return "", nil, errors.New("ChatGPT web attachment data URI must use base64 encoding")
	}
	encoded = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		default:
			return r
		}
	}, encoded)
	if len(encoded) > ((openAIWebMaxAttachmentBytes+2)/3)*4+4 {
		return "", nil, fmt.Errorf("ChatGPT web attachment exceeds %d byte limit", openAIWebMaxAttachmentBytes)
	}
	data, decodeErr := base64.StdEncoding.DecodeString(encoded)
	if decodeErr != nil {
		data, decodeErr = base64.RawStdEncoding.DecodeString(encoded)
	}
	if decodeErr != nil {
		return "", nil, errors.New("ChatGPT web attachment has invalid base64 data")
	}
	if len(data) > openAIWebMaxAttachmentBytes {
		return "", nil, fmt.Errorf("ChatGPT web attachment exceeds %d byte limit", openAIWebMaxAttachmentBytes)
	}
	return mimeType, data, nil
}

func openAIWebMIMEExtension(mimeType string) string {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	if mimeType == "image/jpeg" {
		return "jpg"
	}
	if slash := strings.IndexByte(mimeType, '/'); slash >= 0 && slash+1 < len(mimeType) {
		value := mimeType[slash+1:]
		if plus := strings.IndexByte(value, '+'); plus >= 0 {
			value = value[:plus]
		}
		if value != "" {
			return value
		}
	}
	return "bin"
}

func openAIWebImageDimensions(data []byte) (int, int) {
	if len(data) == 0 {
		return 0, 0
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return 0, 0
	}
	return config.Width, config.Height
}

// uploadWebAttachment follows the browser's three-step file upload flow.
// The signed blob PUT intentionally does not carry the account bearer token;
// only the metadata and completion calls are authenticated against ChatGPT.
func (t *OpenAIWebTransport) uploadWebAttachment(ctx context.Context, account *Account, token string, attachment openAIWebAttachment, index int) (openAIWebAttachment, error) {
	if t == nil {
		return openAIWebAttachment{}, errors.New("ChatGPT web transport is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if len(attachment.Data) == 0 {
		return openAIWebAttachment{}, errors.New("ChatGPT web attachment is empty")
	}
	if len(attachment.Data) > openAIWebMaxAttachmentBytes {
		return openAIWebAttachment{}, fmt.Errorf("ChatGPT web attachment exceeds %d byte limit", openAIWebMaxAttachmentBytes)
	}
	name := strings.TrimSpace(attachment.Name)
	if name == "" {
		name = fmt.Sprintf("attachment_%d.%s", index, openAIWebMIMEExtension(attachment.MIMEType))
	}
	// Keep paths and control characters out of the browser-facing file name.
	if separator := strings.LastIndexAny(name, `/\\`); separator >= 0 {
		name = name[separator+1:]
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	if name == "" {
		name = fmt.Sprintf("attachment_%d.%s", index, openAIWebMIMEExtension(attachment.MIMEType))
	}
	mimeType := strings.ToLower(strings.TrimSpace(attachment.MIMEType))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	if parsedMIME, _, parseErr := mime.ParseMediaType(mimeType); parseErr == nil && strings.TrimSpace(parsedMIME) != "" {
		mimeType = strings.ToLower(strings.TrimSpace(parsedMIME))
	}

	metadataPath := "/backend-api/files"
	// The current composer uses the ACE upload contract for every file type.
	// In particular, ordinary documents must not use the legacy multimodal
	// completion endpoint: that endpoint leaves them as unprocessed assets.
	metadataPayload := map[string]any{
		"file_name":                       name,
		"file_size":                       len(attachment.Data),
		"use_case":                        "ace_upload",
		"timezone_offset_min":             t.options.TimezoneOffsetMin,
		"reset_rate_limits":               false,
		"supports_direct_azure_multipart": true,
		"entry_surface":                   "chat_composer",
		"selection_method":                "file_picker",
		// Let the Web service resolve MIME details consistently with the
		// browser when a caller supplies an arbitrary file extension.
		"mime_resolution_source":   "none",
		"store_in_library":         true,
		"library_persistence_mode": "opportunistic",
	}
	if strings.HasPrefix(mimeType, "image/") && attachment.Width > 0 && attachment.Height > 0 {
		metadataPayload["width"] = attachment.Width
		metadataPayload["height"] = attachment.Height
	}
	metadataBody, err := json.Marshal(metadataPayload)
	if err != nil {
		return openAIWebAttachment{}, fmt.Errorf("ChatGPT web attachment metadata: %w", err)
	}
	metadataHeaders, err := t.commonHeaders(ctx, account, token, metadataPath)
	if err != nil {
		return openAIWebAttachment{}, err
	}
	metadataHeaders.Set("Accept", "*/*")
	metadataHeaders.Set("Content-Type", "application/json")
	metadataResp, err := t.request(ctx, http.MethodPost, metadataPath, token, account, metadataBody, metadataHeaders)
	if err != nil {
		return openAIWebAttachment{}, err
	}
	if metadataResp == nil {
		return openAIWebAttachment{}, errors.New("ChatGPT web attachment metadata returned no response")
	}
	metadataRaw, readErr := readAndCloseWebBody(metadataResp, openAIWebMaxResponseErrorBytes)
	if metadataResp.StatusCode < http.StatusOK || metadataResp.StatusCode >= http.StatusMultipleChoices {
		return openAIWebAttachment{}, webHTTPError(metadataPath, metadataResp.StatusCode, metadataRaw, token)
	}
	if readErr != nil {
		return openAIWebAttachment{}, fmt.Errorf("ChatGPT web attachment metadata response: %w", readErr)
	}
	var uploadMeta map[string]any
	if err := json.Unmarshal(metadataRaw, &uploadMeta); err != nil {
		return openAIWebAttachment{}, errors.New("ChatGPT web attachment metadata response is invalid")
	}
	rawFileID := openAIWebPartString(uploadMeta["file_id"], "id")
	fileID, err := normalizeOpenAIWebFileID(rawFileID)
	if err != nil {
		return openAIWebAttachment{}, err
	}
	if fileID == "" {
		return openAIWebAttachment{}, errors.New("ChatGPT web attachment metadata omitted file_id")
	}
	libraryFileID, _ := uploadMeta["library_file_id"].(string)
	uploadURL := openAIWebPartString(uploadMeta["upload_url"], "url")
	if uploadURL == "" {
		return openAIWebAttachment{}, errors.New("ChatGPT web attachment metadata omitted upload_url")
	}
	parsedUploadURL, err := url.Parse(uploadURL)
	if err != nil || parsedUploadURL.Host == "" || (parsedUploadURL.Scheme != "https" && parsedUploadURL.Scheme != "http") {
		return openAIWebAttachment{}, errors.New("ChatGPT web attachment upload_url is invalid")
	}

	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, parsedUploadURL.String(), bytes.NewReader(attachment.Data))
	if err != nil {
		return openAIWebAttachment{}, fmt.Errorf("ChatGPT web attachment upload request: %w", err)
	}
	putReq.Header.Set("Accept", "application/json, text/plain, */*")
	putReq.Header.Set("Accept-Language", "en-US,en;q=0.8")
	// A browser File may have an empty MIME type (for example .dockerignore),
	// while known document/image types normally carry their detected type.
	// Do not send a synthetic octet-stream value because it changes the
	// browser's signed-blob request shape.
	if mimeType != "application/octet-stream" {
		putReq.Header.Set("Content-Type", mimeType)
	}
	putReq.Header.Set("Origin", t.baseURL())
	putReq.Header.Set("Referer", t.baseURL()+"/")
	putReq.Header.Set("User-Agent", t.options.UserAgent)
	putReq.Header.Set("x-ms-blob-type", "BlockBlob")
	putReq.Header.Set("x-ms-version", "2020-04-08")
	putReq = putReq.WithContext(WithHTTPUpstreamProfile(putReq.Context(), HTTPUpstreamProfileOpenAI))
	putReq.Host = parsedUploadURL.Host
	putResp, err := t.doUpstream(putReq, account)
	if err != nil {
		return openAIWebAttachment{}, err
	}
	if putResp == nil {
		return openAIWebAttachment{}, errors.New("ChatGPT web attachment upload returned no response")
	}
	putRaw, putReadErr := readAndCloseWebBody(putResp, openAIWebMaxResponseErrorBytes)
	if putResp.StatusCode < http.StatusOK || putResp.StatusCode >= http.StatusMultipleChoices {
		return openAIWebAttachment{}, webHTTPError("ChatGPT web attachment upload", putResp.StatusCode, putRaw, token)
	}
	if putReadErr != nil {
		return openAIWebAttachment{}, fmt.Errorf("ChatGPT web attachment upload response: %w", putReadErr)
	}

	completePath := "/backend-api/files/process_upload_stream"
	completePayload := map[string]any{
		"file_id":                  fileID,
		"use_case":                 "ace_upload",
		"index_for_retrieval":      false,
		"file_name":                name,
		"library_persistence_mode": "opportunistic",
		"entry_surface":            "chat_composer",
		"metadata": map[string]any{
			"store_in_library":           true,
			"is_temporary_chat":          false,
			"library_eligibility_reason": "eligible",
			"is_project_thread":          false,
		},
	}
	completeBody, err := json.Marshal(completePayload)
	if err != nil {
		return openAIWebAttachment{}, fmt.Errorf("ChatGPT web attachment processing metadata: %w", err)
	}
	completeHeaders, err := t.commonHeaders(ctx, account, token, completePath)
	if err != nil {
		return openAIWebAttachment{}, err
	}
	completeHeaders.Set("Accept", "*/*")
	completeHeaders.Set("Content-Type", "application/json")
	completeResp, err := t.request(ctx, http.MethodPost, completePath, token, account, completeBody, completeHeaders)
	if err != nil {
		return openAIWebAttachment{}, err
	}
	if completeResp == nil {
		return openAIWebAttachment{}, errors.New("ChatGPT web attachment completion returned no response")
	}
	completeRaw, completeReadErr := readAndCloseWebBody(completeResp, openAIWebMaxResponseErrorBytes)
	if completeResp.StatusCode < http.StatusOK || completeResp.StatusCode >= http.StatusMultipleChoices {
		return openAIWebAttachment{}, webHTTPError(completePath, completeResp.StatusCode, completeRaw, token)
	}
	if completeReadErr != nil {
		return openAIWebAttachment{}, fmt.Errorf("ChatGPT web attachment completion response: %w", completeReadErr)
	}
	processedLibraryFileID, processErr := parseOpenAIWebUploadProcessStream(completeRaw, fileID)
	if processErr != nil {
		return openAIWebAttachment{}, processErr
	}
	if strings.TrimSpace(processedLibraryFileID) != "" {
		libraryFileID = processedLibraryFileID
	}
	return openAIWebAttachment{
		ID:            fileID,
		Name:          name,
		MIMEType:      mimeType,
		MIMETypeKey:   attachment.MIMETypeKey,
		PointerScheme: attachment.PointerScheme,
		LibraryFileID: strings.TrimSpace(libraryFileID),
		Source:        "local",
		SizeBytes:     len(attachment.Data),
		Width:         attachment.Width,
		Height:        attachment.Height,
	}, nil
}

// parseOpenAIWebUploadProcessStream consumes the line-oriented response from
// /backend-api/files/process_upload_stream. The endpoint currently emits raw
// JSON lines, while a few deployments prefix the same lines with `data:`;
// accept both forms and extract the library object id when one is supplied.
func parseOpenAIWebUploadProcessStream(body []byte, expectedFileID string) (string, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		// Some older deployments return an empty 200 response after scheduling
		// processing. The file id is still usable and the caller can continue.
		return "", nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, int(openAIWebMaxResponseErrorBytes))
	sawEvent := false
	ready := false
	completed := false
	libraryFileID := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line == "[DONE]" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "data:") {
			line = strings.TrimSpace(line[len("data:"):])
		}
		if line == "" || line == "[DONE]" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			// Ignore non-JSON analytics/heartbeat lines. A malformed JSON event
			// is not evidence that the upload itself failed.
			continue
		}
		if eventFileID, _ := event["file_id"].(string); strings.TrimSpace(eventFileID) != "" &&
			strings.TrimSpace(expectedFileID) != "" && strings.TrimSpace(eventFileID) != strings.TrimSpace(expectedFileID) {
			continue
		}
		eventName, _ := event["event"].(string)
		if strings.TrimSpace(eventName) == "" {
			// A successful deployment may prepend a plain status object. Only
			// recognized processing events determine stream completeness.
			continue
		}
		sawEvent = true
		switch strings.ToLower(strings.TrimSpace(eventName)) {
		case "file.processing.file_ready":
			ready = true
		case "file.processing.completed":
			completed = true
			if extra, ok := event["extra"].(map[string]any); ok {
				for _, key := range []string{"metadata_object_id", "library_file_id", "id"} {
					if value, ok := extra[key].(string); ok && strings.TrimSpace(value) != "" {
						libraryFileID = strings.TrimSpace(value)
						break
					}
				}
			}
		case "file.processing.failed", "file.processing.error", "error":
			message, _ := event["message"].(string)
			if strings.TrimSpace(message) == "" {
				message = "ChatGPT web file processing failed"
			}
			return "", errors.New(truncateString(message, 512))
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("ChatGPT web attachment processing response: %w", err)
	}
	if sawEvent && !completed {
		// A ready event without the terminal event means the stream was cut
		// short; continuing would race the conversation against indexing.
		if ready {
			return "", errors.New("ChatGPT web attachment processing ended before completion")
		}
		return "", errors.New("ChatGPT web attachment processing response omitted completion event")
	}
	return libraryFileID, nil
}

func openAIWebConversationPreparePayload(conversationBody []byte) ([]byte, error) {
	return openAIWebConversationPreparePayloadForAccount(conversationBody, nil)
}

func openAIWebConversationPreparePayloadForAccount(conversationBody []byte, account *Account) ([]byte, error) {
	var conversation map[string]any
	if err := json.Unmarshal(conversationBody, &conversation); err != nil {
		return nil, errors.New("ChatGPT web conversation payload is invalid")
	}
	attachmentMIMEs := openAIWebConversationAttachmentMIMEs(conversation)
	prepareState := "none"
	prepareDispatch := "debounced"
	prepareSource := "composer_editor_state"
	if len(attachmentMIMEs) > 0 {
		prepareState = "success"
		prepareDispatch = "immediate"
		prepareSource = "file_picker"
	}
	prepare := map[string]any{
		"action":                  conversation["action"],
		"parent_message_id":       conversation["parent_message_id"],
		"model":                   conversation["model"],
		"client_prepare_state":    prepareState,
		"client_prepare_dispatch": prepareDispatch,
		"client_prepare_source":   prepareSource,
		"timezone_offset_min":     conversation["timezone_offset_min"],
		"timezone":                conversation["timezone"],
		"conversation_mode":       conversation["conversation_mode"],
		"system_hints":            conversation["system_hints"],
		"supports_buffering":      true,
		"supported_encodings":     []string{"v1"},
		"client_contextual_info": map[string]any{
			"app_name":                         "chatgpt.com",
			"has_web_push_capabilities":        true,
			"web_push_notification_permission": "default",
		},
		"local_function_names": []string{"local.continue_in_work"},
	}
	if len(attachmentMIMEs) > 0 {
		prepare["attachment_mime_types"] = attachmentMIMEs
	}
	conversationID, hasConversationID := conversation["conversation_id"]
	if hasConversationID {
		prepare["conversation_id"] = conversationID
	}
	model, _ := conversation["model"].(string)
	workMode := (&Account{}).IsOpenAIWebWorkModeModel(model)
	if account != nil {
		workMode = account.IsOpenAIWebWorkModeModel(model)
	}
	// The browser sends partial_query only for the first ordinary-model
	// prepare. Continuation prepares rely on conversation_id/parent_message_id,
	// and Plus work-mode prepares omit partial_query even on the first turn.
	if !hasConversationID && !workMode && len(attachmentMIMEs) == 0 {
		if messages, ok := conversation["messages"].([]any); ok && len(messages) > 0 {
			prepare["partial_query"] = messages[len(messages)-1]
		}
	}
	if workMode {
		prepare["conversation_origin"] = "tpp"
		prepare["model_response_contracts"] = openAIWebModelResponseContracts()
		if effort, ok := conversation["thinking_effort"]; ok {
			prepare["thinking_effort"] = effort
		}
	}
	return json.Marshal(prepare)
}

func openAIWebConversationAttachmentMIMEs(conversation map[string]any) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	messages, _ := conversation["messages"].([]any)
	for _, rawMessage := range messages {
		message, _ := rawMessage.(map[string]any)
		metadata, _ := message["metadata"].(map[string]any)
		attachments, _ := metadata["attachments"].([]any)
		for _, rawAttachment := range attachments {
			attachment, _ := rawAttachment.(map[string]any)
			mimeType, _ := attachment["mimeType"].(string)
			if strings.TrimSpace(mimeType) == "" {
				mimeType, _ = attachment["mime_type"].(string)
			}
			mimeType = strings.TrimSpace(strings.ToLower(mimeType))
			if mimeType == "" {
				continue
			}
			if _, exists := seen[mimeType]; exists {
				continue
			}
			seen[mimeType] = struct{}{}
			result = append(result, mimeType)
		}
	}
	return result
}

func openAIWebTurnTraceID(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return uuid.NewString()
}

func (t *OpenAIWebTransport) prepareConversation(ctx context.Context, account *Account, token string, conversationBody []byte, turnTraceID string) (string, error) {
	body, err := openAIWebConversationPreparePayloadForAccount(conversationBody, account)
	if err != nil {
		return "", err
	}
	headers, err := t.commonHeaders(ctx, account, token, OpenAIWebConversationPreparePath)
	if err != nil {
		return "", err
	}
	headers.Set("Accept", "*/*")
	headers.Set("Content-Type", "application/json")
	headers.Set("X-Conduit-Token", "no-token")
	headers.Set("X-Oai-Turn-Trace-Id", openAIWebTurnTraceID(turnTraceID))
	resp, err := t.request(ctx, http.MethodPost, OpenAIWebConversationPreparePath, token, account, body, headers)
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", errors.New("ChatGPT web conversation prepare returned no response")
	}
	raw, readErr := readAndCloseWebBody(resp, openAIWebMaxResponseErrorBytes)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", webHTTPError(OpenAIWebConversationPreparePath, resp.StatusCode, raw, token)
	}
	if readErr != nil {
		return "", fmt.Errorf("ChatGPT web conversation prepare response: %w", readErr)
	}
	var prepared struct {
		ConduitToken string `json:"conduit_token"`
	}
	if err := json.Unmarshal(raw, &prepared); err != nil {
		return "", errors.New("ChatGPT web conversation prepare response is invalid")
	}
	conduitToken := strings.TrimSpace(prepared.ConduitToken)
	if conduitToken == "" {
		return "", errors.New("ChatGPT web conversation prepare response omitted conduit token")
	}
	return conduitToken, nil
}

// openAIWebSentinelPingOptions describes the small amount of state the Web
// client places in OpenAI-Sentinel-Extra-Data.  The value is intentionally
// independent of credentials; only token-presence flags are emitted.
type openAIWebSentinelPingOptions struct {
	SequenceNumber int
	Source         string
	ConversationID string
	LastMessageID  string
}

func openAIWebSentinelExtraData(requirements OpenAIWebRequirements, options openAIWebSentinelPingOptions) (string, error) {
	source := strings.TrimSpace(options.Source)
	if source == "" {
		source = "conversation_heartbeat"
	}
	sequence := options.SequenceNumber
	if sequence < 0 {
		sequence = 0
	}
	payload := map[string]any{
		"v":               1,
		"sequence_number": sequence,
		"signals": map[string]any{
			"ping_source":                     source,
			"so_token_present":                boolToOpenAIWebSignal(requirements.SOToken),
			"turnstile_token_present":         boolToOpenAIWebSignal(requirements.TurnstileToken),
			"proof_token_present":             boolToOpenAIWebSignal(requirements.ProofToken),
			"prepare_token_present":           boolToOpenAIWebSignal(requirements.PrepareToken),
			"chat_requirements_token_present": boolToOpenAIWebSignal(requirements.Token),
		},
		"conversation_id": strings.TrimSpace(options.ConversationID),
		"last_message_id": strings.TrimSpace(options.LastMessageID),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func boolToOpenAIWebSignal(value string) string {
	if strings.TrimSpace(value) != "" {
		return "1"
	}
	return "0"
}

func openAIWebConversationState(body []byte) (string, string) {
	var payload struct {
		ConversationID string           `json:"conversation_id"`
		Messages       []map[string]any `json:"messages"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return "", ""
	}
	lastMessageID := ""
	if len(payload.Messages) > 0 {
		lastMessageID, _ = payload.Messages[len(payload.Messages)-1]["id"].(string)
	}
	return strings.TrimSpace(payload.ConversationID), strings.TrimSpace(lastMessageID)
}

// pingSentinel sends the browser's phase-specific Sentinel observation. It is
// best effort: the ping is telemetry and should not turn a valid conversation
// into an upstream failure when the observation endpoint is unavailable.
func (t *OpenAIWebTransport) pingSentinel(ctx context.Context, account *Account, token string, requirements OpenAIWebRequirements, options openAIWebSentinelPingOptions) {
	if t == nil {
		return
	}
	extraData, err := openAIWebSentinelExtraData(requirements, options)
	if err != nil {
		return
	}
	headers, err := t.commonHeaders(ctx, account, token, OpenAIWebSentinelPingPath)
	if err != nil {
		return
	}
	headers.Set("Accept", "*/*")
	if value := strings.TrimSpace(requirements.PrepareToken); value != "" {
		headers.Set("OpenAI-Sentinel-Chat-Requirements-Prepare-Token", value)
	}
	if value := strings.TrimSpace(requirements.Token); value != "" {
		headers.Set("OpenAI-Sentinel-Chat-Requirements-Token", value)
	}
	if value := strings.TrimSpace(requirements.ProofToken); value != "" {
		headers.Set("OpenAI-Sentinel-Proof-Token", value)
	}
	if value := strings.TrimSpace(requirements.TurnstileToken); value != "" {
		headers.Set("OpenAI-Sentinel-Turnstile-Token", value)
	}
	if value := strings.TrimSpace(requirements.SOToken); value != "" {
		headers.Set("OpenAI-Sentinel-SO-Token", value)
	}
	headers.Set("OpenAI-Sentinel-Extra-Data", extraData)
	resp, requestErr := t.request(ctx, http.MethodPost, OpenAIWebSentinelPingPath, token, account, nil, headers)
	if requestErr != nil || resp == nil {
		return
	}
	_, _ = readAndCloseWebBody(resp, openAIWebMaxResponseErrorBytes)
}

// BuildConversationRequest creates the classic conversation request after a
// caller has completed the sentinel handshake.
func (t *OpenAIWebTransport) BuildConversationRequest(ctx context.Context, account *Account, token string, requirements OpenAIWebRequirements, options OpenAIWebConversationOptions) (*http.Request, error) {
	body, err := t.buildConversationPayload(ctx, account, token, options)
	if err != nil {
		return nil, err
	}
	return t.buildConversationRequestWithBody(ctx, account, token, requirements, body, options.TurnTraceID)
}

func (t *OpenAIWebTransport) buildConversationRequestWithBody(ctx context.Context, account *Account, token string, requirements OpenAIWebRequirements, body []byte, turnTraceID string) (*http.Request, error) {
	path := OpenAIWebConversationPath
	headers, err := t.commonHeaders(ctx, account, token, path)
	if err != nil {
		return nil, err
	}
	headers.Set("Accept", "text/event-stream")
	headers.Set("Content-Type", "application/json")
	headers.Set("X-Oai-Turn-Trace-Id", openAIWebTurnTraceID(turnTraceID))
	if strings.TrimSpace(requirements.Token) == "" {
		return nil, errors.New("ChatGPT web requirements token is missing")
	}
	headers.Set("OpenAI-Sentinel-Chat-Requirements-Token", strings.TrimSpace(requirements.Token))
	if strings.TrimSpace(requirements.ProofToken) != "" {
		headers.Set("OpenAI-Sentinel-Proof-Token", strings.TrimSpace(requirements.ProofToken))
	}
	if strings.TrimSpace(requirements.TurnstileToken) != "" {
		headers.Set("OpenAI-Sentinel-Turnstile-Token", strings.TrimSpace(requirements.TurnstileToken))
	}
	if strings.TrimSpace(requirements.SOToken) != "" {
		headers.Set("OpenAI-Sentinel-SO-Token", strings.TrimSpace(requirements.SOToken))
	}
	if strings.TrimSpace(requirements.ConduitToken) != "" {
		headers.Set("X-Conduit-Token", strings.TrimSpace(requirements.ConduitToken))
	}
	endpoint, err := t.endpoint(path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	if parsed, parseErr := url.Parse(endpoint); parseErr == nil {
		req.Host = parsed.Host
	}
	return req, nil
}

type openAIWebConversationHandoff struct {
	ConversationID string
	TurnExchangeID string
	TopicID        string
}

// parseOpenAIWebConversationHandoff extracts the topic handoff emitted by the
// newer Web models. The initial HTTP stream is intentionally short and ends
// with [DONE]; the answer is delivered on the shared user WebSocket instead.
func parseOpenAIWebConversationHandoff(frames []openAIWebSSEFrame) (openAIWebConversationHandoff, bool, error) {
	var handoff openAIWebConversationHandoff
	resumeConversationID := ""
	resumeTopicID := ""
	websocketTopicID := ""
	for _, frame := range frames {
		data := strings.TrimSpace(frame.data)
		if data == "" || data == "[DONE]" {
			continue
		}
		var payload map[string]any
		if json.Unmarshal([]byte(data), &payload) != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(openAIWebStringValue(payload["type"]))) {
		case "resume_conversation_token":
			resumeConversationID = strings.TrimSpace(openAIWebStringValue(payload["conversation_id"]))
		case "stream_handoff":
			handoff.ConversationID = strings.TrimSpace(openAIWebStringValue(payload["conversation_id"]))
			handoff.TurnExchangeID = strings.TrimSpace(openAIWebStringValue(payload["turn_exchange_id"]))
			options, _ := payload["options"].([]any)
			for _, rawOption := range options {
				option, _ := rawOption.(map[string]any)
				topicID := strings.TrimSpace(openAIWebStringValue(option["topic_id"]))
				switch strings.ToLower(strings.TrimSpace(openAIWebStringValue(option["type"]))) {
				case "resume_sse_endpoint":
					resumeTopicID = topicID
				case "subscribe_ws_topic":
					websocketTopicID = topicID
				}
			}
		}
	}
	if handoff.ConversationID == "" && handoff.TurnExchangeID == "" && websocketTopicID == "" && resumeTopicID == "" {
		return openAIWebConversationHandoff{}, false, nil
	}
	if handoff.ConversationID == "" || handoff.TurnExchangeID == "" || websocketTopicID == "" {
		return openAIWebConversationHandoff{}, false, errors.New("ChatGPT web stream handoff is incomplete")
	}
	if resumeConversationID != "" && resumeConversationID != handoff.ConversationID {
		return openAIWebConversationHandoff{}, false, errors.New("ChatGPT web handoff conversation mismatch")
	}
	if resumeTopicID != "" && resumeTopicID != websocketTopicID {
		return openAIWebConversationHandoff{}, false, errors.New("ChatGPT web handoff topic mismatch")
	}
	handoff.TopicID = websocketTopicID
	return handoff, true, nil
}

func openAIWebStringValue(value any) string {
	text, _ := value.(string)
	return text
}

func encodeOpenAIWebSSEFrame(frame openAIWebSSEFrame) []byte {
	var builder strings.Builder
	if strings.TrimSpace(frame.event) != "" {
		builder.WriteString("event: ")
		builder.WriteString(strings.TrimSpace(frame.event))
		builder.WriteString("\n")
	}
	data := strings.Split(frame.data, "\n")
	for _, line := range data {
		builder.WriteString("data: ")
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	builder.WriteString("\n")
	return []byte(builder.String())
}

// openAIWebPrefetchedBody preserves frames consumed while deciding whether
// the response is a direct SSE stream or a WebSocket handoff.
type openAIWebPrefetchedBody struct {
	reader io.Reader
	closer io.Closer
}

func (b *openAIWebPrefetchedBody) Read(p []byte) (int, error) {
	if b == nil || b.reader == nil {
		return 0, io.EOF
	}
	return b.reader.Read(p)
}

func (b *openAIWebPrefetchedBody) Close() error {
	if b == nil || b.closer == nil {
		return nil
	}
	return b.closer.Close()
}

// prepareConversationResponseBody consumes only the short protocol prelude.
// Direct/legacy streams are replayed byte-for-byte through a buffered reader;
// handoff streams are switched to a topic reader before [DONE] can terminate
// the public Responses stream.
func (t *OpenAIWebTransport) prepareConversationResponseBody(ctx context.Context, account *Account, token string, source io.ReadCloser) (io.ReadCloser, error) {
	if source == nil {
		return nil, errors.New("ChatGPT web conversation returned no response body")
	}
	buffered := bufio.NewReaderSize(source, 64*1024)
	frames := make([]openAIWebSSEFrame, 0, 8)
	for len(frames) < 128 {
		frame, err := readOpenAIWebSSEFrame(buffered)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			_ = source.Close()
			return nil, err
		}
		frames = append(frames, frame)
		handoff, found, handoffErr := parseOpenAIWebConversationHandoff(frames)
		if handoffErr != nil {
			_ = source.Close()
			return nil, handoffErr
		}
		if found {
			for len(frames) < 128 {
				next, nextErr := readOpenAIWebSSEFrame(buffered)
				if nextErr != nil {
					if errors.Is(nextErr, io.EOF) {
						break
					}
					_ = source.Close()
					return nil, nextErr
				}
				frames = append(frames, next)
				if strings.TrimSpace(next.data) == "[DONE]" {
					break
				}
			}
			prefix := bytes.Buffer{}
			for _, frame := range frames {
				if strings.TrimSpace(frame.data) == "[DONE]" {
					continue
				}
				_, _ = prefix.Write(encodeOpenAIWebSSEFrame(frame))
			}
			body, wsErr := t.newOpenAIWebTopicBody(ctx, account, token, handoff, &prefix)
			if wsErr != nil {
				_ = source.Close()
				return nil, wsErr
			}
			_ = source.Close()
			return body, nil
		}
		if openAIWebDirectStreamFrame(frame) || strings.TrimSpace(frame.data) == "[DONE]" {
			break
		}
	}
	prefix := bytes.Buffer{}
	for _, frame := range frames {
		_, _ = prefix.Write(encodeOpenAIWebSSEFrame(frame))
	}
	return &openAIWebPrefetchedBody{
		reader: io.MultiReader(bytes.NewReader(prefix.Bytes()), buffered),
		closer: source,
	}, nil
}

func openAIWebDirectStreamFrame(frame openAIWebSSEFrame) bool {
	data := strings.TrimSpace(frame.data)
	if data == "" || data == "[DONE]" || data == `"v1"` || strings.EqualFold(strings.TrimSpace(frame.event), "delta_encoding") {
		return false
	}
	var payload map[string]any
	if json.Unmarshal([]byte(data), &payload) != nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(openAIWebStringValue(payload["type"]))) {
	case "resume_conversation_token", "stream_handoff":
		return false
	default:
		return true
	}
}

func (t *OpenAIWebTransport) userWebsocketURL(ctx context.Context, account *Account, token string) (string, error) {
	headers, err := t.commonHeaders(ctx, account, token, OpenAIWebUserWebsocketPath)
	if err != nil {
		return "", err
	}
	headers.Set("Accept", "application/json")
	resp, err := t.request(ctx, http.MethodGet, OpenAIWebUserWebsocketPath, token, account, nil, headers)
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", errors.New("ChatGPT web websocket endpoint returned no response")
	}
	raw, readErr := readAndCloseWebBody(resp, openAIWebMaxResponseErrorBytes)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", webHTTPError(OpenAIWebUserWebsocketPath, resp.StatusCode, raw, token)
	}
	if readErr != nil {
		return "", fmt.Errorf("ChatGPT web websocket endpoint response: %w", readErr)
	}
	var payload struct {
		WebsocketURL string `json:"websocket_url"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || strings.TrimSpace(payload.WebsocketURL) == "" {
		return "", errors.New("ChatGPT web websocket endpoint omitted websocket_url")
	}
	parsed, err := url.Parse(strings.TrimSpace(payload.WebsocketURL))
	if err != nil || (parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.Host == "" {
		return "", errors.New("ChatGPT web websocket_url is invalid")
	}
	return parsed.String(), nil
}

type openAIWebTopicBody struct {
	ctx             context.Context
	cancel          context.CancelFunc
	conn            openAIWSClientConn
	topic           string
	readTimeout     time.Duration
	output          bytes.Buffer
	seen            map[string]struct{}
	frameCount      int
	streamItemCount int
	lastFrameType   string
	done            bool
	closed          bool
}

func (t *OpenAIWebTransport) newOpenAIWebTopicBody(ctx context.Context, account *Account, token string, handoff openAIWebConversationHandoff, prefix *bytes.Buffer) (io.ReadCloser, error) {
	wsURL, err := t.userWebsocketURL(ctx, account, token)
	if err != nil {
		return nil, err
	}
	headers, err := t.commonHeaders(ctx, account, token, OpenAIWebUserWebsocketPath)
	if err != nil {
		return nil, err
	}
	headers.Set("Origin", t.baseURL())
	headers.Set("Accept", "*/*")
	if parsed, parseErr := url.Parse(wsURL); parseErr == nil && t.jar != nil {
		cookieURL := *parsed
		if cookieURL.Scheme == "ws" {
			cookieURL.Scheme = "http"
		} else if cookieURL.Scheme == "wss" {
			cookieURL.Scheme = "https"
		}
		for _, cookie := range t.jar.Cookies(&cookieURL) {
			headers.Add("Cookie", cookie.String())
		}
	}
	dialer := t.wsDialer
	if dialer == nil {
		dialer = newDefaultOpenAIWSClientDialer()
	}
	conn, _, _, err := dialer.Dial(ctx, wsURL, headers, t.proxyFor(account))
	if err != nil {
		return nil, fmt.Errorf("ChatGPT web websocket connect failed: %w", err)
	}
	// The Celsius user socket requires the browser's connection lifecycle:
	// connect/presence must be accepted before a topic subscription is valid.
	// Sending both commands in one frame matches the current ChatGPT Web client
	// and avoids a race where an immediate subscribe is rejected as not_connected.
	command := []map[string]any{
		{
			"id": 1,
			"command": map[string]any{
				"type": "connect",
				"presence": map[string]any{
					"type":  "presence",
					"state": "background",
				},
			},
		},
		{
			"id": 2,
			"command": map[string]any{
				"type":     "subscribe",
				"topic_id": handoff.TopicID,
			},
		},
	}
	if err := conn.WriteJSON(ctx, command); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ChatGPT web websocket subscribe failed: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	body := &openAIWebTopicBody{
		ctx:         streamCtx,
		cancel:      cancel,
		conn:        conn,
		topic:       handoff.TopicID,
		readTimeout: t.options.TopicReadTimeout,
		seen:        make(map[string]struct{}),
	}
	if prefix != nil && prefix.Len() > 0 {
		body.output.Write(prefix.Bytes())
	}
	return body, nil
}

func (b *openAIWebTopicBody) Read(p []byte) (int, error) {
	if b == nil || b.closed {
		return 0, io.EOF
	}
	if b.output.Len() > 0 {
		return b.output.Read(p)
	}
	if b.done {
		return 0, io.EOF
	}
	for b.output.Len() == 0 && !b.done {
		readCtx := b.ctx
		cancelRead := func() {}
		if b.readTimeout > 0 {
			readCtx, cancelRead = context.WithTimeout(b.ctx, b.readTimeout)
		}
		frame, err := b.conn.ReadMessage(readCtx)
		cancelRead()
		if err != nil {
			b.done = true
			if errors.Is(err, context.DeadlineExceeded) {
				return 0, fmt.Errorf(
					"ChatGPT web websocket read timeout after %s (frames=%d, stream_items=%d, last_type=%s)",
					b.readTimeout,
					b.frameCount,
					b.streamItemCount,
					b.lastFrameType,
				)
			}
			return 0, err
		}
		b.frameCount++
		terminal, parseErr := b.consumeFrame(frame)
		if parseErr != nil {
			b.done = true
			return 0, parseErr
		}
		if terminal {
			b.output.WriteString("data: [DONE]\n\n")
			b.done = true
		}
	}
	if b.output.Len() > 0 {
		return b.output.Read(p)
	}
	return 0, io.EOF
}

func (b *openAIWebTopicBody) consumeFrame(frame []byte) (bool, error) {
	var root any
	if err := json.Unmarshal(frame, &root); err != nil {
		return false, errors.New("ChatGPT web websocket frame is invalid")
	}
	terminal := false
	var consume func(any) error
	consume = func(value any) error {
		if list, ok := value.([]any); ok {
			for _, item := range list {
				if err := consume(item); err != nil {
					return err
				}
			}
			return nil
		}
		item, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		typ := strings.ToLower(strings.TrimSpace(openAIWebStringValue(item["type"])))
		normalizedType := strings.ReplaceAll(typ, "_", "-")
		if typ != "" {
			b.lastFrameType = typ
		}
		topicID := openAIWebStringValue(item["topic_id"])
		if topicID == "" {
			topicID = openAIWebStringValue(item["topicId"])
		}
		if strings.TrimSpace(topicID) != "" && strings.TrimSpace(topicID) != b.topic {
			return nil
		}
		if normalizedType == "error" || normalizedType == "stream-error" || normalizedType == "conversation-turn-error" {
			message := openAIWebStringValue(item["message"])
			if message == "" {
				if errorObject, ok := item["error"].(map[string]any); ok {
					message = openAIWebStringValue(errorObject["message"])
				}
			}
			if message == "" {
				message = openAIWebStringValue(item["error"])
			}
			if strings.TrimSpace(message) == "" {
				message = "ChatGPT web websocket stream failed"
			}
			return fmt.Errorf("ChatGPT web websocket stream error: %s", truncateString(message, 512))
		}
		streamItemID := openAIWebStringValue(item["stream_item_id"])
		if streamItemID == "" {
			streamItemID = openAIWebStringValue(item["streamItemId"])
		}
		encodedItem := openAIWebStringValue(item["encoded_item"])
		if encodedItem == "" {
			encodedItem = openAIWebStringValue(item["encodedItem"])
		}
		if encodedItem != "" {
			if strings.TrimSpace(streamItemID) == "" {
				digest := sha256.Sum256([]byte(encodedItem))
				streamItemID = "encoded:" + hex.EncodeToString(digest[:])
			}
			if b.seen == nil {
				b.seen = make(map[string]struct{})
			}
			if _, exists := b.seen[streamItemID]; !exists {
				b.seen[streamItemID] = struct{}{}
				b.streamItemCount++
				b.output.WriteString(encodedItem)
				if !strings.HasSuffix(encodedItem, "\n\n") {
					b.output.WriteString("\n\n")
				}
			}
		}
		if normalizedType == "done" || normalizedType == "stream-done" || normalizedType == "stream-end" || normalizedType == "conversation-turn-done" || normalizedType == "conversation-turn-completed" || normalizedType == "completed" || normalizedType == "response.completed" {
			terminal = true
		}
		status := strings.ToLower(strings.TrimSpace(openAIWebStringValue(item["status"])))
		if status == "done" || status == "finished" || status == "completed" {
			terminal = true
		}
		for _, key := range []string{"reply", "catchups", "payload", "data", "body", "event"} {
			if nested, exists := item[key]; exists {
				if err := consume(nested); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := consume(root); err != nil {
		return false, err
	}
	return terminal, nil
}

func (b *openAIWebTopicBody) Close() error {
	if b == nil || b.closed {
		return nil
	}
	b.closed = true
	if b.cancel != nil {
		b.cancel()
	}
	if b.conn != nil {
		return b.conn.Close()
	}
	return nil
}

// Do performs the complete web handshake and returns a Responses-shaped SSE
// response suitable for the existing gateway response handlers.
func (t *OpenAIWebTransport) Do(ctx context.Context, account *Account, token string, options OpenAIWebConversationOptions) (*http.Response, error) {
	if t == nil {
		return nil, errors.New("ChatGPT web transport is nil")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("access token is required")
	}
	if !t.options.SkipBootstrap {
		if _, err := t.Bootstrap(ctx, account, token); err != nil {
			return nil, err
		}
	}
	body, err := t.buildConversationPayload(ctx, account, token, options)
	if err != nil {
		return nil, err
	}
	turnTraceID := openAIWebTurnTraceID(options.TurnTraceID)
	conduitToken, err := t.prepareConversation(ctx, account, token, body, turnTraceID)
	if err != nil {
		return nil, err
	}
	requirements, err := t.GetRequirements(ctx, account, token)
	if err != nil {
		return nil, err
	}
	requirements.ConduitToken = conduitToken
	conversationID, lastMessageID := openAIWebConversationState(body)
	t.pingSentinel(ctx, account, token, requirements, openAIWebSentinelPingOptions{
		SequenceNumber: 0,
		Source:         "conversation_heartbeat",
		ConversationID: conversationID,
		LastMessageID:  lastMessageID,
	})
	req, err := t.buildConversationRequestWithBody(ctx, account, token, requirements, body, turnTraceID)
	if err != nil {
		return nil, err
	}
	resp, err := t.doUpstream(req, account)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("ChatGPT web conversation returned no response")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := readAndCloseWebBody(resp, openAIWebMaxResponseErrorBytes)
		return nil, webHTTPError(OpenAIWebConversationPath, resp.StatusCode, body, token)
	}
	if resp.Body == nil {
		return nil, errors.New("ChatGPT web conversation returned no response body")
	}
	conversationBody, bodyErr := t.prepareConversationResponseBody(ctx, account, token, resp.Body)
	if bodyErr != nil {
		return nil, bodyErr
	}
	model := OpenAIWebTestModel
	if options.Request != nil {
		// buildConversationPayload already validated this selector. Reuse the
		// canonical spelling so payload and response metadata cannot diverge.
		if canonical, ok := NormalizeOpenAIWebModel(options.Request.Model); ok {
			model = canonical
		}
	}
	if resp.Header == nil {
		resp.Header = make(http.Header)
	}
	var history []apicompat.ChatMessage
	if options.Request != nil {
		history = options.Request.Messages
	}
	resp.Body = newOpenAIWebResponsesBodyWithPromptTools(conversationBody, model, history, options.PromptTools)
	resp.Header.Set("Content-Type", "text/event-stream")
	return resp, nil
}

// NewOpenAIWebResponsesBody wraps classic conversation SSE as Responses SSE.
func NewOpenAIWebResponsesBody(source io.ReadCloser, model string) io.ReadCloser {
	return NewOpenAIWebResponsesBodyWithHistory(source, model, nil)
}

// NewOpenAIWebResponsesBodyWithHistory also removes assistant messages that
// the web endpoint replays from the submitted conversation history.
func NewOpenAIWebResponsesBodyWithHistory(source io.ReadCloser, model string, messages []apicompat.ChatMessage) io.ReadCloser {
	return newOpenAIWebResponsesBodyWithPromptTools(source, model, messages, nil)
}

func newOpenAIWebResponsesBodyWithPromptTools(source io.ReadCloser, model string, messages []apicompat.ChatMessage, promptTools *OpenAIWebPromptTools) io.ReadCloser {
	if source == nil {
		// Keep the exported constructor safe for callers that only have an
		// optional upstream body. Do will reject a nil response body before it
		// reaches this point, while pure conversion helpers can still drain a
		// deterministic empty stream.
		source = io.NopCloser(strings.NewReader(""))
	}
	historyMessages, historyText := openAIWebAssistantHistory(messages)
	return &openAIWebResponsesReader{
		source:          source,
		reader:          bufio.NewReaderSize(source, 64*1024),
		model:           strings.TrimSpace(model),
		responseID:      "resp_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		itemID:          "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		createdAt:       time.Now().Unix(),
		historyText:     historyText,
		historyMessages: historyMessages,
		promptTools:     promptTools,
	}
}

// ConvertOpenAIWebConversationSSE converts a complete classic SSE body.  It is
// convenient for deterministic tests and offline diagnostics.
func ConvertOpenAIWebConversationSSE(body []byte, model string) ([]byte, error) {
	source := io.NopCloser(bytes.NewReader(body))
	reader := NewOpenAIWebResponsesBody(source, model)
	defer func() { _ = reader.Close() }()
	return io.ReadAll(reader)
}

func openAIWebAssistantHistory(messages []apicompat.ChatMessage) ([]string, string) {
	values := make([]string, 0)
	var combined strings.Builder
	for _, message := range messages {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "assistant") {
			continue
		}
		content, err := openAIWebMessageContentFromRaw(message.Content)
		if err != nil || content.Text == "" {
			continue
		}
		values = append(values, content.Text)
		combined.WriteString(content.Text)
	}
	return values, combined.String()
}

type openAIWebResponsesReader struct {
	source           io.ReadCloser
	reader           *bufio.Reader
	model            string
	responseID       string
	itemID           string
	conversationID   string
	lastMessageID    string
	createdAt        int64
	sequenceNumber   int
	rawText          string
	text             string
	historyText      string
	historyMessages  []string
	historyIndex     int
	promptTools      *OpenAIWebPromptTools
	promptClassified bool
	usage            map[string]any
	output           bytes.Buffer
	started          bool
	failed           bool
	finished         bool
	closed           bool
}

// OpenAIWebConversationStateProvider exposes the private Web cursor captured
// while the response body is consumed. It is intentionally separate from the
// public Responses response ID: Web conversation IDs and message IDs are
// account-bound upstream state and must never be sent to API clients as if
// they were portable OpenAI response IDs.
type OpenAIWebConversationStateProvider interface {
	OpenAIWebConversationState() (conversationID, parentMessageID string)
}

// OpenAIWebConversationTranscriptProvider exposes the assistant text that was
// actually emitted by the Web upstream. It is used only for a compact
// transcript-prefix digest; the text is never stored as a public response
// identifier or forwarded back to ChatGPT Web.
type OpenAIWebConversationTranscriptProvider interface {
	OpenAIWebAssistantText() string
}

func (r *openAIWebResponsesReader) OpenAIWebConversationState() (string, string) {
	if r == nil {
		return "", ""
	}
	return strings.TrimSpace(r.conversationID), strings.TrimSpace(r.lastMessageID)
}

func (r *openAIWebResponsesReader) OpenAIWebAssistantText() string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.text)
}

func (r *openAIWebResponsesReader) Close() error {
	if r == nil || r.closed {
		return nil
	}
	r.closed = true
	if r.source != nil {
		return r.source.Close()
	}
	return nil
}

func (r *openAIWebResponsesReader) Read(p []byte) (int, error) {
	if r == nil {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.closed {
		return 0, io.EOF
	}
	for r.output.Len() == 0 && !r.finished {
		frame, err := readOpenAIWebSSEFrame(r.reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				r.finish(false)
				break
			}
			return 0, err
		}
		r.convertFrame(frame)
	}
	if r.output.Len() > 0 {
		return r.output.Read(p)
	}
	return 0, io.EOF
}

type openAIWebSSEFrame struct {
	event string
	data  string
}

func readOpenAIWebSSEFrame(reader *bufio.Reader) (openAIWebSSEFrame, error) {
	if reader == nil {
		return openAIWebSSEFrame{}, io.EOF
	}
	var event string
	var data []string
	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if len(line) > 16<<20 {
			return openAIWebSSEFrame{}, errors.New("ChatGPT web SSE frame exceeds limit")
		}
		if strings.TrimSpace(line) == "" {
			if event == "" && len(data) == 0 {
				if err != nil {
					return openAIWebSSEFrame{}, err
				}
				continue
			}
			return openAIWebSSEFrame{event: event, data: strings.Join(data, "\n")}, nil
		}
		if strings.HasPrefix(line, ":") {
			if err != nil && len(data) == 0 {
				return openAIWebSSEFrame{}, err
			}
			if err != nil {
				return openAIWebSSEFrame{event: event, data: strings.Join(data, "\n")}, nil
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
		if err != nil {
			if len(data) > 0 || event != "" {
				return openAIWebSSEFrame{event: event, data: strings.Join(data, "\n")}, nil
			}
			return openAIWebSSEFrame{}, err
		}
	}
}

func (r *openAIWebResponsesReader) convertFrame(frame openAIWebSSEFrame) {
	data := strings.TrimSpace(frame.data)
	if data == "" {
		if openAIWebFrameTerminal(nil, frame.event) {
			r.finish(true)
		}
		return
	}
	if data == "[DONE]" {
		r.finish(true)
		return
	}
	var payload any
	if json.Unmarshal([]byte(data), &payload) != nil {
		return
	}
	r.start()
	if conversationID := findStringRecursive(payload, "conversation_id", "conversationId"); conversationID != "" {
		r.conversationID = conversationID
	}
	if messageID := openAIWebAssistantMessageID(payload); messageID != "" {
		r.lastMessageID = messageID
	}
	if usage, ok := openAIWebUsage(payload); ok {
		mergeOpenAIWebUsage(&r.usage, usage)
	}
	if blocked, _ := findBoolRecursive(payload, "blocked", "is_blocked"); blocked {
		r.failed = true
		response := r.responseObject("failed", nil)
		response["error"] = map[string]any{"code": "content_policy", "message": "upstream conversation was blocked"}
		r.emit("response.failed", map[string]any{
			"response": response,
		})
		r.finish(true)
		return
	}
	if message := findStringRecursive(payload, "error", "error_message"); message != "" {
		r.failed = true
		response := r.responseObject("failed", nil)
		response["error"] = map[string]any{"code": "upstream_error", "message": redactOpenAIWebSecret(message)}
		r.emit("response.failed", map[string]any{
			"response": response,
		})
		r.finish(true)
		return
	}
	if historyMessage, ok := openAIWebExplicitAssistantText(payload); ok && r.historyIndex < len(r.historyMessages) && historyMessage == r.historyMessages[r.historyIndex] {
		r.historyIndex++
		r.rawText = ""
		r.text = ""
		if openAIWebFrameTerminal(payload, frame.event) {
			r.finish(true)
		}
		return
	}
	if nextRaw, changed := applyOpenAIWebTextPatch(payload, r.rawText, r.historyText); changed {
		nextText := sanitizeOpenAIWebText(nextRaw)
		delta := nextText
		if strings.HasPrefix(nextText, r.text) {
			delta = strings.TrimPrefix(nextText, r.text)
		} else if nextText == r.text {
			delta = ""
		}
		r.rawText = nextRaw
		r.text = nextText
		if delta == "" {
			if openAIWebFrameTerminal(payload, frame.event) {
				r.finish(true)
			}
			return
		}
		if r.promptTools == nil {
			r.emit("response.output_text.delta", map[string]any{
				"response_id":   r.responseID,
				"item_id":       r.itemID,
				"output_index":  0,
				"content_index": 0,
				"delta":         delta,
			})
		}
	}
	if openAIWebFrameTerminal(payload, frame.event) {
		r.finish(true)
	}
}

func (r *openAIWebResponsesReader) start() {
	if r == nil || r.started {
		return
	}
	r.started = true
	r.emit("response.created", map[string]any{"response": r.responseObject("in_progress", []any{})})
	if r.promptTools != nil {
		return
	}
	r.emit("response.output_item.added", map[string]any{
		"response_id": r.responseID, "output_index": 0, "item": r.messageItem("in_progress", []any{}),
	})
	r.emit("response.content_part.added", map[string]any{
		"response_id": r.responseID, "item_id": r.itemID, "output_index": 0, "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
	})
}

func (r *openAIWebResponsesReader) responseObject(status string, output []any) map[string]any {
	if output == nil {
		output = []any{}
	}
	parallel := false
	if r.promptTools != nil {
		parallel = r.promptTools.Parallel
	}
	return map[string]any{
		"id":                  r.responseID,
		"object":              "response",
		"created_at":          r.createdAt,
		"status":              status,
		"error":               nil,
		"incomplete_details":  nil,
		"model":               r.model,
		"output":              output,
		"parallel_tool_calls": parallel,
	}
}

func (r *openAIWebResponsesReader) messageItem(status string, content []any) map[string]any {
	if content == nil {
		content = []any{}
	}
	return map[string]any{
		"id": r.itemID, "type": "message", "role": "assistant", "status": status, "content": content,
	}
}

func (r *openAIWebResponsesReader) emit(eventType string, payload map[string]any) {
	if r == nil || r.finished || payload == nil {
		return
	}
	payload["type"] = eventType
	payload["sequence_number"] = r.sequenceNumber
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	r.sequenceNumber++
	_, _ = r.output.WriteString("event: " + eventType + "\ndata: " + string(encoded) + "\n\n")
}

func (r *openAIWebResponsesReader) finish(fromDone bool) {
	if r == nil || r.finished {
		return
	}
	if r.promptTools != nil && !r.promptClassified {
		r.promptClassified = true
		calls, recognized, err := r.promptTools.ParseResponse(r.text)
		if err != nil {
			r.failed = true
			response := r.responseObject("failed", nil)
			response["error"] = map[string]any{"code": "tool_protocol_error", "message": redactOpenAIWebSecret(err.Error())}
			r.emit("response.failed", map[string]any{"response": response})
			r.finished = true
			_, _ = r.output.WriteString("data: [DONE]\n\n")
			return
		}
		if recognized {
			if len(calls) == 0 {
				r.failed = true
				response := r.responseObject("failed", nil)
				response["error"] = map[string]any{"code": "tool_protocol_error", "message": "prompt tool envelope contained no calls"}
				r.emit("response.failed", map[string]any{"response": response})
				r.finished = true
				_, _ = r.output.WriteString("data: [DONE]\n\n")
				return
			}
			r.finishPromptToolCalls(calls)
			return
		}
		if !recognized && (r.promptTools.Choice == "required" || r.promptTools.ChoiceName != "") {
			r.failed = true
			response := r.responseObject("failed", nil)
			response["error"] = map[string]any{"code": "tool_protocol_error", "message": "model did not satisfy the required tool_choice"}
			r.emit("response.failed", map[string]any{"response": response})
			r.finished = true
			_, _ = r.output.WriteString("data: [DONE]\n\n")
			return
		}
		// Prompt mode buffers the upstream text until classification. Emit the
		// ordinary message lifecycle only after we know it is not a tool envelope.
		r.emitPromptMessageStart()
		if r.text != "" {
			r.emit("response.output_text.delta", map[string]any{
				"response_id": r.responseID, "item_id": r.itemID, "output_index": 0,
				"content_index": 0, "delta": r.text,
			})
		}
	}
	if r.failed {
		r.finished = true
		_, _ = r.output.WriteString("data: [DONE]\n\n")
		return
	}
	if strings.TrimSpace(r.text) == "" {
		r.failed = true
		response := r.responseObject("failed", nil)
		response["error"] = map[string]any{
			"code":    "upstream_empty_response",
			"message": "ChatGPT web upstream completed without assistant text",
		}
		r.emit("response.failed", map[string]any{"response": response})
		r.finished = true
		_, _ = r.output.WriteString("data: [DONE]\n\n")
		return
	}
	if !r.started {
		r.start()
	}
	itemStatus := "completed"
	status := "completed"
	terminalEvent := "response.completed"
	if !fromDone {
		itemStatus = "incomplete"
		status = "incomplete"
		terminalEvent = "response.incomplete"
	}
	part := map[string]any{"type": "output_text", "text": r.text, "annotations": []any{}}
	r.emit("response.output_text.done", map[string]any{
		"response_id": r.responseID, "item_id": r.itemID, "output_index": 0, "content_index": 0, "text": r.text,
	})
	r.emit("response.content_part.done", map[string]any{
		"response_id": r.responseID, "item_id": r.itemID, "output_index": 0, "content_index": 0, "part": part,
	})
	item := r.messageItem(itemStatus, []any{part})
	r.emit("response.output_item.done", map[string]any{
		"response_id": r.responseID, "output_index": 0, "item": item,
	})
	response := r.responseObject(status, []any{item})
	if r.usage != nil {
		response["usage"] = r.usage
	}
	r.emit(terminalEvent, map[string]any{"response": response})
	r.finished = true
	_, _ = r.output.WriteString("data: [DONE]\n\n")
}

func (r *openAIWebResponsesReader) emitPromptMessageStart() {
	if r == nil {
		return
	}
	r.emit("response.output_item.added", map[string]any{
		"response_id": r.responseID, "output_index": 0, "item": r.messageItem("in_progress", []any{}),
	})
	r.emit("response.content_part.added", map[string]any{
		"response_id": r.responseID, "item_id": r.itemID, "output_index": 0,
		"content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
	})
}

func (r *openAIWebResponsesReader) finishPromptToolCalls(calls []OpenAIWebPromptToolCall) {
	items := make([]any, 0, len(calls))
	for index, call := range calls {
		itemID := "fc_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		callID := "call_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		outputName := strings.TrimSpace(call.TargetName)
		if outputName == "" {
			outputName = call.Name
			if call.Namespace != "" {
				prefix := strings.TrimSpace(call.Namespace) + "__"
				if strings.HasPrefix(outputName, prefix) {
					outputName = strings.TrimPrefix(outputName, prefix)
				}
			}
		}
		isCustom := strings.EqualFold(strings.TrimSpace(call.Type), "custom")
		if isCustom {
			input := call.Input
			item := map[string]any{
				"id": itemID, "type": "custom_tool_call", "status": "completed",
				"call_id": callID, "name": outputName, "input": input,
			}
			if call.Namespace != "" {
				item["namespace"] = call.Namespace
			}
			added := map[string]any{
				"id": itemID, "type": "custom_tool_call", "status": "in_progress",
				"call_id": callID, "name": outputName, "input": "",
			}
			if call.Namespace != "" {
				added["namespace"] = call.Namespace
			}
			r.emit("response.output_item.added", map[string]any{
				"response_id": r.responseID, "output_index": index, "item": added,
			})
			if input != "" {
				r.emit("response.custom_tool_call_input.delta", map[string]any{
					"response_id": r.responseID, "item_id": itemID, "output_index": index,
					"call_id": callID, "name": outputName, "delta": input,
				})
			}
			r.emit("response.custom_tool_call_input.done", map[string]any{
				"response_id": r.responseID, "item_id": itemID, "output_index": index,
				"call_id": callID, "name": outputName, "input": input,
			})
			r.emit("response.output_item.done", map[string]any{
				"response_id": r.responseID, "output_index": index, "item": item,
			})
			items = append(items, item)
			continue
		}
		arguments := string(call.Arguments)
		item := map[string]any{
			"id": itemID, "type": "function_call", "status": "completed",
			"call_id": callID, "name": outputName, "arguments": arguments,
		}
		if call.Namespace != "" {
			item["namespace"] = call.Namespace
		}
		added := map[string]any{
			"id": itemID, "type": "function_call", "status": "in_progress",
			"call_id": callID, "name": outputName, "arguments": "",
		}
		if call.Namespace != "" {
			added["namespace"] = call.Namespace
		}
		r.emit("response.output_item.added", map[string]any{
			"response_id": r.responseID, "output_index": index, "item": added,
		})
		r.emit("response.function_call_arguments.delta", map[string]any{
			"response_id": r.responseID, "item_id": itemID, "output_index": index,
			"call_id": callID, "name": outputName, "delta": arguments,
		})
		r.emit("response.function_call_arguments.done", map[string]any{
			"response_id": r.responseID, "item_id": itemID, "output_index": index,
			"call_id": callID, "name": outputName, "arguments": arguments,
		})
		r.emit("response.output_item.done", map[string]any{
			"response_id": r.responseID, "output_index": index, "item": item,
		})
		items = append(items, item)
	}
	response := r.responseObject("completed", items)
	r.emit("response.completed", map[string]any{"response": response})
	r.finished = true
	_, _ = r.output.WriteString("data: [DONE]\n\n")
}

func applyOpenAIWebTextPatch(value any, currentText, historyText string) (string, bool) {
	switch root := value.(type) {
	case map[string]any:
		if role := openAIWebMessageRole(root); role != "" {
			if role != "assistant" {
				return currentText, false
			}
			if text := openAIWebFullText(root); text != "" {
				return stripOpenAIWebHistory(text, historyText), true
			}
		}
		if message, exists := root["message"]; exists {
			if role := openAIWebMessageRole(message); role != "" && role != "assistant" {
				return currentText, false
			}
			if text := openAIWebFullText(message); text != "" {
				return stripOpenAIWebHistory(text, historyText), true
			}
		}
		operation, _ := root["o"].(string)
		operation = strings.ToLower(strings.TrimSpace(operation))
		path, _ := root["p"].(string)
		if strings.Contains(path, "/content/parts/") {
			if text, ok := root["v"].(string); ok {
				switch operation {
				case "append":
					return currentText + text, true
				case "replace", "add":
					return stripOpenAIWebHistory(text, historyText), true
				}
			}
		}
		if nested, exists := root["v"]; exists {
			if text, ok := nested.(string); ok && operation == "" && strings.TrimSpace(path) == "" {
				return currentText + text, true
			}
			return applyOpenAIWebTextPatch(nested, currentText, historyText)
		}
	case []any:
		text := currentText
		changed := false
		for _, item := range root {
			next, itemChanged := applyOpenAIWebTextPatch(item, text, historyText)
			if itemChanged {
				text = next
				changed = true
			}
		}
		return text, changed
	}
	return currentText, false
}

func openAIWebExplicitAssistantText(value any) (string, bool) {
	switch root := value.(type) {
	case map[string]any:
		if role := openAIWebMessageRole(root); role != "" {
			if role != "assistant" {
				return "", false
			}
			if text := openAIWebFullText(root); text != "" {
				return text, true
			}
		}
		if message, exists := root["message"]; exists {
			if openAIWebMessageRole(message) == "assistant" {
				if text := openAIWebFullText(message); text != "" {
					return text, true
				}
			}
		}
		if nested, exists := root["v"]; exists {
			return openAIWebExplicitAssistantText(nested)
		}
	case []any:
		for _, item := range root {
			if text, ok := openAIWebExplicitAssistantText(item); ok {
				return text, true
			}
		}
	}
	return "", false
}

func stripOpenAIWebHistory(text, historyText string) string {
	for historyText != "" && strings.HasPrefix(text, historyText) {
		text = strings.TrimPrefix(text, historyText)
	}
	return text
}

var (
	openAIWebAnnotationRE            = regexp.MustCompile("\uE200([^\uE201]*)\uE201")
	openAIWebTrailingAnnotationRE    = regexp.MustCompile("\uE200[^\uE201]*$")
	openAIWebWhitespaceBeforePunctRE = regexp.MustCompile(`\s+([.,;:!?])`)
)

func sanitizeOpenAIWebText(text string) string {
	text = openAIWebAnnotationRE.ReplaceAllStringFunc(text, func(annotation string) string {
		match := openAIWebAnnotationRE.FindStringSubmatch(annotation)
		if len(match) < 2 {
			return ""
		}
		parts := strings.Split(match[1], "\uE202")
		if len(parts) == 0 {
			return ""
		}
		kind := strings.ToLower(strings.TrimSpace(parts[0]))
		data := parts[1:]
		if kind == "url" {
			label := ""
			link := ""
			if len(data) > 0 {
				label = strings.TrimSpace(data[0])
			}
			if len(data) > 1 {
				link = strings.TrimSpace(data[1])
			}
			if label != "" && (strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://")) {
				return label + " (" + link + ")"
			}
			if label != "" {
				return label
			}
			return link
		}
		for _, part := range data {
			part = strings.TrimSpace(part)
			lower := strings.ToLower(part)
			if part != "" && !strings.HasPrefix(lower, "turn") && !strings.HasPrefix(lower, "source") {
				return part
			}
		}
		return ""
	})
	text = openAIWebTrailingAnnotationRE.ReplaceAllString(text, "")
	return openAIWebWhitespaceBeforePunctRE.ReplaceAllString(text, "$1")
}

// openAIWebUsage finds the first object-valued usage field in a web frame.
// Depending on the conversation variant it may be attached to the root,
// message, or an envelope under v/response.
func openAIWebUsage(value any) (map[string]any, bool) {
	switch current := value.(type) {
	case map[string]any:
		if candidate, ok := current["usage"].(map[string]any); ok {
			return candidate, true
		}
		for _, child := range current {
			if usage, ok := openAIWebUsage(child); ok {
				return usage, true
			}
		}
	case []any:
		for _, child := range current {
			if usage, ok := openAIWebUsage(child); ok {
				return usage, true
			}
		}
	}
	return nil, false
}

func mergeOpenAIWebUsage(dst *map[string]any, src map[string]any) {
	if dst == nil || src == nil {
		return
	}
	if *dst == nil {
		*dst = cloneOpenAIWebJSONMap(src)
		return
	}
	mergeOpenAIWebJSONMap(*dst, src)
}

func mergeOpenAIWebJSONMap(dst, src map[string]any) {
	for key, value := range src {
		existing, exists := dst[key]
		if sourceMap, ok := value.(map[string]any); ok {
			if existingMap, ok := existing.(map[string]any); ok {
				mergeOpenAIWebJSONMap(existingMap, sourceMap)
				continue
			}
			dst[key] = cloneOpenAIWebJSONValue(sourceMap)
			continue
		}
		// Do not let a terminal all-zero usage object erase non-zero counts
		// observed on an earlier progress frame.
		if !exists || !openAIWebJSONValueHasPositiveNumber(existing) || openAIWebJSONValueHasPositiveNumber(value) {
			dst[key] = cloneOpenAIWebJSONValue(value)
		}
	}
}

func cloneOpenAIWebJSONMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	cloned := make(map[string]any, len(value))
	for key, child := range value {
		cloned[key] = cloneOpenAIWebJSONValue(child)
	}
	return cloned
}

func cloneOpenAIWebJSONValue(value any) any {
	switch current := value.(type) {
	case map[string]any:
		return cloneOpenAIWebJSONMap(current)
	case []any:
		cloned := make([]any, len(current))
		for index, child := range current {
			cloned[index] = cloneOpenAIWebJSONValue(child)
		}
		return cloned
	default:
		return value
	}
}

func openAIWebJSONValueHasPositiveNumber(value any) bool {
	switch current := value.(type) {
	case float64:
		return current > 0
	case float32:
		return current > 0
	case int:
		return current > 0
	case int64:
		return current > 0
	case uint:
		return current > 0
	case uint64:
		return current > 0
	case json.Number:
		number, err := current.Float64()
		return err == nil && number > 0
	case map[string]any:
		for _, child := range current {
			if openAIWebJSONValueHasPositiveNumber(child) {
				return true
			}
		}
	case []any:
		for _, child := range current {
			if openAIWebJSONValueHasPositiveNumber(child) {
				return true
			}
		}
	}
	return false
}

// openAIWebMessageRole returns an explicit author role when a frame carries a
// message object. Patch operations usually omit the message, in which case an
// empty result lets the caller keep the operation (the web stream has already
// established the assistant message in the preceding frame).
func openAIWebMessageRole(value any) string {
	root, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	author, _ := root["author"].(map[string]any)
	role, _ := author["role"].(string)
	return strings.ToLower(strings.TrimSpace(role))
}

func openAIWebFullText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if root, ok := value.(map[string]any); ok {
		if text, ok := root["text"].(string); ok {
			return text
		}
		if message, ok := root["message"]; ok {
			if text := openAIWebFullText(message); text != "" {
				return text
			}
		}
		if content, ok := root["content"]; ok {
			if text := openAIWebFullText(content); text != "" {
				return text
			}
		}
		if parts, ok := root["parts"].([]any); ok {
			var builder strings.Builder
			for _, part := range parts {
				if text, ok := part.(string); ok {
					builder.WriteString(text)
				}
			}
			return builder.String()
		}
	}
	if list, ok := value.([]any); ok {
		var builder strings.Builder
		for _, item := range list {
			builder.WriteString(openAIWebFullText(item))
		}
		return builder.String()
	}
	return ""
}

func openAIWebFrameTerminal(value any, event string) bool {
	if strings.EqualFold(strings.TrimSpace(event), "done") || strings.EqualFold(strings.TrimSpace(event), "conversation.done") {
		return true
	}
	if root, ok := value.(map[string]any); ok {
		path, _ := root["p"].(string)
		operation, _ := root["o"].(string)
		if strings.Contains(strings.ToLower(path), "/end_turn") &&
			(strings.EqualFold(operation, "replace") || strings.EqualFold(operation, "add")) {
			if complete, ok := root["v"].(bool); ok && complete {
				return true
			}
		}
		if strings.Contains(strings.ToLower(path), "/status") {
			if status, ok := root["v"].(string); ok {
				status = strings.ToLower(strings.TrimSpace(status))
				if strings.Contains(status, "finish") || strings.Contains(status, "complete") {
					return true
				}
			}
		}
		for _, key := range []string{"is_complete", "is_finished", "end_turn"} {
			if complete, ok := root[key].(bool); ok && complete {
				return true
			}
		}
		for _, key := range []string{"status", "state"} {
			if status, ok := root[key].(string); ok && strings.Contains(strings.ToLower(status), "finish") {
				return true
			}
		}
		if value, ok := root["v"]; ok {
			return openAIWebFrameTerminal(value, "")
		}
	}
	if list, ok := value.([]any); ok {
		for _, item := range list {
			if openAIWebFrameTerminal(item, "") {
				return true
			}
		}
	}
	return false
}

func findStringRecursive(value any, keys ...string) string {
	wanted := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		wanted[key] = struct{}{}
	}
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if _, ok := wanted[key]; ok {
				if text, ok := child.(string); ok && strings.TrimSpace(text) != "" {
					return strings.TrimSpace(text)
				}
			}
			if text := findStringRecursive(child, keys...); text != "" {
				return text
			}
		}
	case []any:
		for _, child := range current {
			if text := findStringRecursive(child, keys...); text != "" {
				return text
			}
		}
	}
	return ""
}

// openAIWebAssistantMessageID finds the upstream assistant message cursor
// without treating public response/item IDs or arbitrary nested IDs as a Web
// parent message. ChatGPT Web frames normally carry author.role=assistant and
// message.id, but the handoff/topic envelopes vary between direct SSE and WS.
func openAIWebAssistantMessageID(value any) string {
	var visit func(any) string
	visit = func(current any) string {
		switch item := current.(type) {
		case map[string]any:
			if openAIWebMessageRole(item) == "assistant" {
				if id, ok := item["id"].(string); ok && strings.TrimSpace(id) != "" {
					return strings.TrimSpace(id)
				}
				if nested, ok := item["message"]; ok {
					if id := visit(nested); id != "" {
						return id
					}
				}
			}
			for _, key := range []string{"message", "messages", "item", "items", "data", "payload", "body", "event", "v"} {
				if nested, ok := item[key]; ok {
					if id := visit(nested); id != "" {
						return id
					}
				}
			}
		case []any:
			for _, nested := range item {
				if id := visit(nested); id != "" {
					return id
				}
			}
		}
		return ""
	}
	return visit(value)
}

func findBoolRecursive(value any, keys ...string) (bool, bool) {
	wanted := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		wanted[key] = struct{}{}
	}
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if _, ok := wanted[key]; ok {
				if result, ok := child.(bool); ok {
					return result, true
				}
			}
			if result, ok := findBoolRecursive(child, keys...); ok {
				return result, ok
			}
		}
	case []any:
		for _, child := range current {
			if result, ok := findBoolRecursive(child, keys...); ok {
				return result, ok
			}
		}
	}
	return false, false
}
