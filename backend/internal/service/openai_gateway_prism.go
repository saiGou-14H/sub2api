package service

// Prism is an asynchronous, browser-backed OpenAI transport.  This file keeps
// the public gateway protocol adapters separate from the Prism wire protocol:
// OpenAI Responses, Chat Completions, and Anthropic Messages all become one
// Responses request, while the transport turns the completed Prism payload
// back into the response stream consumed by the existing handlers.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Prism state belongs to an explicit response chain and its downstream API key.
// New requests get their own project as well as conversation, since sandbox
// tools can inspect all files in a project.
type openAIPrismSessionKey struct {
	accountID      int64
	apiKeyID       int64
	groupID        int64
	credentialHash [32]byte
	responseID     string
	promptBridge   bool
}

type openAIPrismSession struct {
	state         OpenAIPrismSessionState
	projectHandle string
	transport     *OpenAIPrismTransport
	gate          chan struct{}
	expiresAt     time.Time
}

func prismSessionKey(c *gin.Context, account, credentials *Account, responseID string) openAIPrismSessionKey {
	material, _ := json.Marshal([]string{prismAccessToken(credentials), prismCredential(credentials, "prism_session_token"), prismCredential(credentials, "prism_cookie")})
	key := openAIPrismSessionKey{accountID: account.ID, apiKeyID: getAPIKeyIDFromContext(c), credentialHash: sha256.Sum256(material), responseID: strings.TrimSpace(responseID), promptBridge: account.IsPrismPromptToolBridgeEnabled()}
	if c != nil {
		if value, ok := c.Get("api_key"); ok {
			if apiKey, ok := value.(*APIKey); ok && apiKey != nil {
				key.groupID = derefGroupID(apiKey.GroupID)
			}
		}
	}
	return key
}

func (s *OpenAIGatewayService) savePrismSession(ctx context.Context, key openAIPrismSessionKey, transport *OpenAIPrismTransport, gate chan struct{}, projectHandle string) error {
	state := transport.OpenAIPrismSessionState()
	if state.ResponseID == "" {
		return nil
	}
	key.responseID = state.ResponseID
	if err := s.persistPrismSession(ctx, key, state, projectHandle); err != nil {
		return err
	}
	now := time.Now()
	// Bound retained opaque credentials and expire idle response chains.
	count := 0
	var oldestKey any
	var oldest time.Time
	s.openaiPrismSessions.Range(func(k, value any) bool {
		session, ok := value.(openAIPrismSession)
		if !ok || !session.expiresAt.After(now) {
			s.openaiPrismSessions.Delete(k)
			return true
		}
		count++
		if oldestKey == nil || session.expiresAt.Before(oldest) {
			oldestKey, oldest = k, session.expiresAt
		}
		return true
	})
	if count >= 4096 && oldestKey != nil {
		s.openaiPrismSessions.Delete(oldestKey)
	}
	s.openaiPrismSessions.Store(key, openAIPrismSession{state: state, projectHandle: projectHandle, transport: transport, gate: gate, expiresAt: now.Add(openaiStickySessionTTL)})
	return nil
}

func (s *OpenAIGatewayService) newOpenAIPrismTransport() *OpenAIPrismTransport {
	if s != nil && s.openAIPrismTransportFactory != nil {
		return s.openAIPrismTransportFactory()
	}
	return NewOpenAIPrismTransport(s, OpenAIPrismTransportOptions{})
}

func prismAccessToken(account *Account) string {
	if account == nil {
		return ""
	}
	// The UI stores this as the normal OpenAI access_token.  The explicit
	// Prism key is accepted for imports created by older integrations.
	if token := strings.TrimSpace(account.GetCredential("access_token")); token != "" {
		return token
	}
	return strings.TrimSpace(account.GetCredential("prism_oai_access_token"))
}

func prismErrorStatus(err error) int {
	var workspaceErr *PrismWorkspaceError
	if errors.As(err, &workspaceErr) {
		return workspaceErr.Status
	}
	var prismErr *OpenAIPrismHTTPError
	if errors.As(err, &prismErr) && prismErr != nil && prismErr.StatusCode >= 400 && prismErr.StatusCode <= 599 {
		return prismErr.StatusCode
	}
	return http.StatusBadGateway
}

func prismGatewayErrorMessage(err error) string {
	var prismErr *OpenAIPrismHTTPError
	if errors.As(err, &prismErr) && prismErr != nil && strings.TrimSpace(prismErr.Message) != "" {
		return prismErr.Message
	}
	if err == nil {
		return "Prism upstream request failed"
	}
	return "Prism upstream request failed: " + err.Error()
}

func writePrismResponsesError(c *gin.Context, err error) {
	if writePrismStreamError(c, err, "responses") {
		return
	}
	status := prismErrorStatus(err)
	message := prismGatewayErrorMessage(err)
	MarkResponseCommitted(c)
	writeOpenAIResponsesFallbackError(c, status, "upstream_error", message)
}

func writePrismChatError(c *gin.Context, err error) {
	if writePrismStreamError(c, err, "chat") {
		return
	}
	status := prismErrorStatus(err)
	MarkResponseCommitted(c)
	writeChatCompletionsError(c, status, "upstream_error", prismGatewayErrorMessage(err))
}

// A completed Prism turn has already executed its remote tools. Protocol
// adapters may normally request failover for an invalid local stream, but that
// signal must never escape this boundary and replay the completed turn.
func finishPrismConversionError(c *gin.Context, err error, write func(*gin.Context, error)) error {
	var failover *UpstreamFailoverError
	if !errors.As(err, &failover) {
		return err
	}
	// Do not wrap the original error: errors.As at the gateway handler would
	// still discover its failover signal through an Unwrap chain.
	terminal := &OpenAIPrismStartedError{Err: errors.New("Prism response conversion failed after the upstream turn completed")}
	if write != nil {
		write(c, terminal)
	}
	return terminal
}

func writePrismAnthropicError(c *gin.Context, err error) {
	if writePrismStreamError(c, err, "anthropic") {
		return
	}
	status := prismErrorStatus(err)
	MarkResponseCommitted(c)
	c.JSON(status, gin.H{"type": "error", "error": gin.H{"type": "api_error", "message": prismGatewayErrorMessage(err)}})
}

func prismErrorBody(err *OpenAIPrismHTTPError) []byte {
	if err == nil {
		return nil
	}
	message := SanitizeUpstreamErrorMessage(strings.TrimSpace(err.Message))
	body, marshalErr := json.Marshal(map[string]any{"error": map[string]any{"message": message}})
	if marshalErr != nil {
		return []byte(`{"error":{"message":"Prism upstream request failed"}}`)
	}
	return body
}

// handlePrismForwardError converts a Prism HTTP failure into the gateway's
// normal failover signal when the response is account/provider-retryable. A
// deterministic client error is written in the requested protocol and stays
// local to this account.
func (s *OpenAIGatewayService) handlePrismForwardError(ctx context.Context, c *gin.Context, account *Account, err error, model string, write func()) error {
	var workspaceErr *PrismWorkspaceError
	if errors.As(err, &workspaceErr) {
		if write != nil {
			write()
		}
		return err
	}
	var startedErr *OpenAIPrismStartedError
	if errors.As(err, &startedErr) {
		// Once Prism has accepted a turn, failover would start another remote
		// task and could execute tools twice. Keep account error bookkeeping,
		// but finish this request without the gateway's retry signal.
		status := prismErrorStatus(err)
		message := SanitizeUpstreamErrorMessage(prismGatewayErrorMessage(err))
		setOpsUpstreamError(c, status, message, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			ProxyID: opsUpstreamProxyID(account), ProxyName: opsUpstreamProxyName(account),
			Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
			UpstreamStatusCode: status, Kind: "request_error", Message: message,
		})
		if errors.Is(err, context.Canceled) || (errors.Is(err, context.DeadlineExceeded) && errors.Is(ctx.Err(), context.DeadlineExceeded)) {
			return err
		}
		var httpErr *OpenAIPrismHTTPError
		if errors.As(err, &httpErr) {
			s.handleOpenAIAccountUpstreamError(ctx, account, httpErr.StatusCode, httpErr.Headers, prismErrorBody(httpErr), model)
			if retryAfter := httpErr.Headers.Get("Retry-After"); retryAfter != "" {
				c.Header("Retry-After", retryAfter)
			}
		}
		if write != nil {
			write()
		}
		return err
	}
	var prismErr *OpenAIPrismHTTPError
	if !errors.As(err, &prismErr) || prismErr == nil {
		return s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	body := prismErrorBody(prismErr)
	message := SanitizeUpstreamErrorMessage(strings.TrimSpace(prismErr.Message))
	resp := &http.Response{StatusCode: prismErr.StatusCode, Header: prismErr.Headers}
	if failoverErr := s.failoverOpenAIUpstreamHTTPError(ctx, c, account, resp, body, message, model); failoverErr != nil {
		return failoverErr
	}
	setOpsUpstreamError(c, prismErr.StatusCode, message, "")
	if retryAfter := prismErr.Headers.Get("Retry-After"); retryAfter != "" {
		c.Header("Retry-After", retryAfter)
	}

	if write != nil {
		write()
	}
	return err
}

func (s *OpenAIGatewayService) doOpenAIPrism(ctx context.Context, c *gin.Context, account *Account, req *apicompat.ResponsesRequest) (*http.Response, error) {
	if req == nil || account == nil {
		return nil, errors.New("Prism request and account are required")
	}
	// All Prism operations finish before their distributed lease can expire.
	ctx, cancel := context.WithTimeout(ctx, 14*time.Minute)
	defer cancel()
	credentialAccount := account
	if account.IsCredentialShadow() && s.accountRepo != nil {
		resolved, err := resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil {
			return nil, err
		}
		credentialAccount = resolved
	}
	key := prismSessionKey(c, account, credentialAccount, req.PreviousResponseID)
	var state OpenAIPrismSessionState
	var transport *OpenAIPrismTransport
	var gate chan struct{}
	project := PrismProjectFromContext(ctx)
	if key.responseID != "" {
		session, err := s.loadPrismSession(ctx, key)
		if err != nil {
			return nil, err
		}
		state, transport, gate = session.state, session.transport, session.gate
		if session.projectHandle != "" {
			if project != nil && project.Handle != session.projectHandle {
				return nil, &OpenAIPrismHTTPError{StatusCode: http.StatusBadRequest, Message: "Prism project does not match this response chain"}
			}
			if project == nil {
				var apiKey *APIKey
				if c != nil {
					value, _ := c.Get("api_key")
					apiKey, _ = value.(*APIKey)
				}
				project, err = s.LoadPrismProject(ctx, apiKey, session.projectHandle)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	if project != nil {
		if project.AccountID != account.ID || (state.ProjectID != "" && state.ProjectID != project.State.ProjectID) {
			return nil, &OpenAIPrismHTTPError{StatusCode: http.StatusBadRequest, Message: "Prism project does not match this response chain"}
		}
		release, err := s.LockPrismProject(ctx, project)
		if err != nil {
			return nil, err
		}
		defer release()
		ctx = PrismProjectLeaseContext(ctx, project)
		if state.ProjectID == "" {
			state = project.State
			// A new conversation may use existing project files without inheriting
			// another conversation's response cursor or tool bridge state.
			state.ConversationID, state.ResponseID, state.CodexListenSnapshot = "", "", nil
		} else {
			// A workspace operation may have refreshed the sandbox after this
			// response was saved. Keep its conversation cursor, but use the
			// current project authorization loaded under the project lock.
			state.SandboxURL, state.SandboxToken = project.State.SandboxURL, project.State.SandboxToken
			state.Cookies = prismCloneCookies(project.State.Cookies)
		}
		transport = s.PrismProjectTransport(project)
	} else {
		release, err := s.lockPrismContinuation(ctx, key, state)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	if transport == nil {
		transport = s.newOpenAIPrismTransport()
	}
	if transport == nil {
		return nil, errors.New("Prism transport is not configured")
	}
	if gate == nil {
		gate = make(chan struct{}, 1)
	}
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-gate }()
	transportAccount := *account
	transportAccount.Credentials = credentialAccount.Credentials
	transport.RestoreCookies(&transportAccount, prismAccessToken(credentialAccount), state.Cookies)
	resp, err := transport.Do(ctx, &transportAccount, prismAccessToken(credentialAccount), OpenAIPrismConversationOptions{
		Request: req, ProjectID: state.ProjectID, ConversationID: state.ConversationID,
		PreviousResponseID: req.PreviousResponseID, CodexListenSnapshot: state.CodexListenSnapshot,
		SandboxURL: state.SandboxURL, SandboxToken: state.SandboxToken,
		OnProgress: prismProgressCallback(ctx),
	})
	if err != nil {
		return nil, err
	}
	projectHandle := ""
	if project != nil {
		projectHandle = project.Handle
	}
	if project != nil {
		if err := s.SavePrismProjectState(ctx, project, transport.OpenAIPrismSessionState()); err != nil {
			_ = resp.Body.Close()
			return nil, &OpenAIPrismStartedError{Err: err}
		}
	}
	// Publish the response cursor only after the project's lease-checked state
	// save succeeds, so an expired owner cannot leave a usable stale cursor.
	if err := s.savePrismSession(ctx, key, transport, gate, projectHandle); err != nil {
		_ = resp.Body.Close()
		return nil, &OpenAIPrismStartedError{Err: err}
	}
	publishPrismFileSync(c, transport.OpenAIPrismSessionState(), prismProgressCallback(ctx) != nil)
	return resp, nil
}

func prismResult(ctx context.Context, s *OpenAIGatewayService, c *gin.Context, account *Account, resp *http.Response, result *OpenAIForwardResult, requestedModel, billingModel, upstreamModel string, stream bool, started time.Time, reasoning *string) *OpenAIForwardResult {
	if result == nil {
		result = &OpenAIForwardResult{}
	}
	result.RequestID = resp.Header.Get("x-request-id")
	result.UpstreamHeaders = resp.Header
	result.Model = requestedModel
	result.BillingModel = billingModel
	result.UpstreamModel = upstreamModel
	result.UpstreamEndpoint = OpenAIPrismStartPath
	result.Stream = stream
	result.ReasoningEffort = reasoning
	result.Duration = time.Since(started)
	s.bindHTTPResponseAccount(ctx, c, account, result.ResponseID)
	return result
}

func (s *OpenAIGatewayService) forwardResponsesViaOpenAIPrism(ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
	started := time.Now()
	var req apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writePrismResponsesError(c, fmt.Errorf("parse Prism Responses request: %w", err))
		return nil, err
	}
	originalModel := strings.TrimSpace(req.Model)
	if originalModel == "" {
		originalModel = OpenAIPrismDefaultModel
		req.Model = originalModel
	}
	billingModel, upstreamModel := resolveOpenAIForwardMappedModels(account, originalModel, false)
	if upstreamModel == "" {
		err := &OpenAIPrismHTTPError{StatusCode: http.StatusBadRequest, Message: "Model is not enabled for this Prism account"}
		writePrismResponsesError(c, err)
		return nil, err
	}
	req.Model = upstreamModel
	prepared, prompt, err := prepareOpenAIPrismTools(account, &req)
	if err != nil {
		writePrismResponsesError(c, err)
		return nil, err
	}
	prepared.Stream = true
	ctx = withPrismStreamingProgress(ctx, c, req.Stream)
	SetOpsUpstreamModel(c, upstreamModel)
	reasoning := extractOpenAIReasoningEffortFromBody(body, upstreamModel, billingModel, originalModel)
	resp, err := s.doOpenAIPrism(ctx, c, account, prepared)
	if err != nil {
		return nil, s.handlePrismForwardError(ctx, c, account, err, upstreamModel, func() { writePrismResponsesError(c, err) })
	}
	if resp == nil || resp.Body == nil {
		err = errors.New("Prism transport returned no response body")
		writePrismResponsesError(c, err)
		return nil, err
	}
	if prompt != nil {
		resp.Body = newOpenAIPrismPromptToolBody(resp.Body, prompt)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err = fmt.Errorf("Prism response status %d", resp.StatusCode)
		writePrismResponsesError(c, err)
		return nil, err
	}
	var result *OpenAIForwardResult
	if req.Stream {
		reasoningValue := ""
		if req.Reasoning != nil {
			reasoningValue = strings.TrimSpace(req.Reasoning.Effort)
		}
		streamResult, handleErr := s.handleStreamingResponseWithReasoning(ctx, resp, c, account, started, originalModel, upstreamModel, reasoningValue)
		if handleErr != nil {
			return nil, finishPrismConversionError(c, handleErr, writePrismResponsesError)
		}
		result = &OpenAIForwardResult{Usage: *streamResult.usage, ResponseID: strings.TrimSpace(streamResult.responseID), FirstTokenMs: streamResult.firstTokenMs, ImageCount: streamResult.imageCount, ImageOutputSizes: streamResult.imageOutputSizes, SearchCount: streamResult.searchCount}
	} else {
		nonStreamResult, handleErr := s.handleNonStreamingResponse(ctx, resp, c, account, originalModel, upstreamModel)
		if handleErr != nil {
			return nil, finishPrismConversionError(c, handleErr, writePrismResponsesError)
		}
		result = &OpenAIForwardResult{Usage: *nonStreamResult.usage, ResponseID: strings.TrimSpace(nonStreamResult.responseID), ImageCount: nonStreamResult.imageCount, ImageOutputSizes: nonStreamResult.imageOutputSizes, SearchCount: nonStreamResult.searchCount}
	}
	return prismResult(ctx, s, c, account, resp, result, originalModel, billingModel, upstreamModel, req.Stream, started, reasoning), nil
}

func (s *OpenAIGatewayService) forwardChatCompletionsViaOpenAIPrism(ctx context.Context, c *gin.Context, account *Account, body []byte, defaultMappedModel string) (*OpenAIForwardResult, error) {
	started := time.Now()
	var chat apicompat.ChatCompletionsRequest
	if gjson.GetBytes(body, "messages").Exists() {
		if err := json.Unmarshal(body, &chat); err != nil {
			writePrismChatError(c, err)
			return nil, err
		}
	} else {
		var source apicompat.ResponsesRequest
		if err := json.Unmarshal(body, &source); err != nil {
			writePrismChatError(c, err)
			return nil, err
		}
		converted, err := apicompat.ResponsesToChatCompletionsRequestWithOptions(&source, &apicompat.ResponsesToChatOptions{ReasoningContentByID: s.reasoningContentByID})
		if err != nil {
			writePrismChatError(c, err)
			return nil, err
		}
		chat = *converted
	}
	originalModel := strings.TrimSpace(chat.Model)
	if originalModel == "" {
		originalModel = strings.TrimSpace(defaultMappedModel)
	}
	if originalModel == "" {
		originalModel = OpenAIPrismDefaultModel
	}
	chat.Model = originalModel
	req, err := apicompat.ChatCompletionsToResponses(&chat)
	if err != nil {
		writePrismChatError(c, err)
		return nil, err
	}
	clientStream := chat.Stream
	ctx = withPrismStreamingProgress(ctx, c, clientStream)
	req.Stream = true
	billingModel, upstreamModel := resolveOpenAIForwardMappedModels(account, originalModel, false)
	if upstreamModel == "" {
		err := &OpenAIPrismHTTPError{StatusCode: http.StatusBadRequest, Message: "Model is not enabled for this Prism account"}
		writePrismChatError(c, err)
		return nil, err
	}
	req.Model = upstreamModel
	prepared, prompt, err := prepareOpenAIPrismTools(account, req)
	if err != nil {
		writePrismChatError(c, err)
		return nil, err
	}
	SetOpsUpstreamModel(c, upstreamModel)
	reasoning := extractOpenAIReasoningEffortFromBody(body, upstreamModel, billingModel, originalModel)
	resp, err := s.doOpenAIPrism(ctx, c, account, prepared)
	if err != nil {
		return nil, s.handlePrismForwardError(ctx, c, account, err, upstreamModel, func() { writePrismChatError(c, err) })
	}
	if resp == nil || resp.Body == nil {
		err = errors.New("Prism transport returned no response body")
		writePrismChatError(c, err)
		return nil, err
	}
	if prompt != nil {
		resp.Body = newOpenAIPrismPromptToolBody(resp.Body, prompt)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err = fmt.Errorf("Prism response status %d", resp.StatusCode)
		writePrismChatError(c, err)
		return nil, err
	}
	var result *OpenAIForwardResult
	if clientStream {
		result, err = s.handleChatStreamingResponse(resp, c, account, originalModel, billingModel, upstreamModel, started, len(body))
	} else {
		result, err = s.handleChatBufferedStreamingResponse(resp, c, account, originalModel, billingModel, upstreamModel, started)
	}
	if err != nil {
		return result, finishPrismConversionError(c, err, writePrismChatError)
	}
	if result != nil {
		result.ReasoningEffort = reasoning
	}
	return prismResult(ctx, s, c, account, resp, result, originalModel, billingModel, upstreamModel, clientStream, started, reasoning), nil
}

func (s *OpenAIGatewayService) forwardAnthropicViaOpenAIPrism(ctx context.Context, c *gin.Context, account *Account, body []byte, defaultMappedModel string) (*OpenAIForwardResult, error) {
	started := time.Now()
	var source apicompat.AnthropicRequest
	if err := json.Unmarshal(body, &source); err != nil {
		writePrismAnthropicError(c, err)
		return nil, err
	}
	originalModel := strings.TrimSpace(source.Model)
	if originalModel == "" {
		originalModel = strings.TrimSpace(defaultMappedModel)
	}
	if originalModel == "" {
		originalModel = OpenAIPrismDefaultModel
	}
	clientStream := source.Stream
	ctx = withPrismStreamingProgress(ctx, c, clientStream)
	req, err := apicompat.AnthropicToResponses(&source)
	if err != nil {
		writePrismAnthropicError(c, err)
		return nil, err
	}
	req.Model = originalModel
	req.Stream = true
	billingModel, upstreamModel := resolveOpenAIForwardMappedModels(account, originalModel, false)
	if upstreamModel == "" {
		err := &OpenAIPrismHTTPError{StatusCode: http.StatusBadRequest, Message: "Model is not enabled for this Prism account"}
		writePrismAnthropicError(c, err)
		return nil, err
	}
	req.Model = upstreamModel
	prepared, prompt, err := prepareOpenAIPrismTools(account, req)
	if err != nil {
		writePrismAnthropicError(c, err)
		return nil, err
	}
	SetOpsUpstreamModel(c, upstreamModel)
	reasoning := extractOpenAIReasoningEffortFromBody(body, upstreamModel, billingModel, originalModel)
	resp, err := s.doOpenAIPrism(ctx, c, account, prepared)
	if err != nil {
		return nil, s.handlePrismForwardError(ctx, c, account, err, upstreamModel, func() { writePrismAnthropicError(c, err) })
	}
	if resp == nil || resp.Body == nil {
		err = errors.New("Prism transport returned no response body")
		writePrismAnthropicError(c, err)
		return nil, err
	}
	if prompt != nil {
		resp.Body = newOpenAIPrismPromptToolBody(resp.Body, prompt)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err = fmt.Errorf("Prism response status %d", resp.StatusCode)
		writePrismAnthropicError(c, err)
		return nil, err
	}
	var result *OpenAIForwardResult
	if clientStream {
		result, err = s.handleAnthropicStreamingResponse(resp, c, account, originalModel, billingModel, upstreamModel, started)
	} else {
		result, err = s.handleAnthropicBufferedStreamingResponse(resp, c, account, originalModel, billingModel, upstreamModel, started)
	}
	if err != nil {
		return result, finishPrismConversionError(c, err, writePrismAnthropicError)
	}
	return prismResult(ctx, s, c, account, resp, result, originalModel, billingModel, upstreamModel, clientStream, started, reasoning), nil
}
