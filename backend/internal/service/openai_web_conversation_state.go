package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// OpenAIWebConversationState is the minimum server-side cursor needed to
// continue a ChatGPT Web conversation without resending its full history.
// The identifiers are private upstream values and are never exposed as the
// public Responses response ID.
type OpenAIWebConversationState struct {
	ConversationID         string `json:"conversation_id"`
	ParentMessageID        string `json:"parent_message_id"`
	AccountID              int64  `json:"account_id"`
	GroupID                int64  `json:"group_id"`
	Model                  string `json:"model"`
	SessionKeyHash         string `json:"session_key_hash"`
	ProfileFingerprint     string `json:"profile_fingerprint,omitempty"`
	LastUserFingerprint    string `json:"last_user_fingerprint,omitempty"`
	TranscriptFingerprint  string `json:"transcript_fingerprint,omitempty"`
	TranscriptMessageCount int    `json:"transcript_message_count,omitempty"`
	RequiresFullReplay     bool   `json:"requires_full_replay,omitempty"`
}

type openAIWebContinuationContext struct {
	stateKey           string
	responseAliasKey   string
	sessionKeyHash     string
	profileFingerprint string
	previousResponseID string
	state              *OpenAIWebConversationState
	reused             bool
	eligible           bool
	lockRelease        func()
}

func openAIWebStableSessionID(c *gin.Context, body []byte) string {
	if id := strings.TrimSpace(explicitOpenAIRequestSessionID(c, body)); id != "" {
		return id
	}
	if body != nil {
		metadata := strings.TrimSpace(gjson.GetBytes(body, "metadata.user_id").String())
		if parsed := ParseMetadataUserID(metadata); parsed != nil && strings.TrimSpace(parsed.SessionID) != "" {
			return "metadata:" + strings.TrimSpace(parsed.SessionID)
		}
	}
	return ""
}

func openAIWebPreviousResponseID(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	id := strings.TrimSpace(gjson.GetBytes(body, "previous_response_id").String())
	if id == "" || ClassifyOpenAIPreviousResponseIDKind(id) != OpenAIPreviousResponseIDKindResponseID {
		return ""
	}
	return id
}

func openAIWebHash(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte{0})
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func openAIWebSessionKeyHash(c *gin.Context, sessionID string) string {
	return openAIWebHash("web-session-v1", stringInt64(getAPIKeyIDFromContext(c)), sessionID)
}

func openAIWebStorageKey(c *gin.Context, accountID int64, model, sessionKeyHash string) string {
	if strings.TrimSpace(sessionKeyHash) == "" || accountID <= 0 {
		return ""
	}
	return "web-conversation:v1:" + openAIWebHash(
		stringInt64(getAPIKeyIDFromContext(c)),
		stringInt64(accountID),
		strings.TrimSpace(model),
		sessionKeyHash,
	)
}

func openAIWebResponseAliasKey(c *gin.Context, accountID int64, model, responseID string) string {
	if strings.TrimSpace(responseID) == "" || accountID <= 0 {
		return ""
	}
	return "web-response:v1:" + openAIWebHash(
		stringInt64(getAPIKeyIDFromContext(c)),
		stringInt64(accountID),
		strings.TrimSpace(model),
		strings.TrimSpace(responseID),
	)
}

func stringInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}

func openAIWebProfileFingerprint(req *apicompat.ChatCompletionsRequest) string {
	if req == nil {
		return ""
	}
	profile := struct {
		Model        string                   `json:"model"`
		Instructions string                   `json:"instructions,omitempty"`
		Messages     []apicompat.ChatMessage  `json:"messages,omitempty"`
		Tools        []apicompat.ChatTool     `json:"tools,omitempty"`
		Functions    []apicompat.ChatFunction `json:"functions,omitempty"`
	}{
		Model: req.Model, Instructions: req.Instructions,
		Tools: req.Tools, Functions: req.Functions,
	}
	for _, message := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role == "system" || role == "developer" {
			profile.Messages = append(profile.Messages, message)
		}
	}
	if len(profile.Messages) == 0 && strings.TrimSpace(profile.Instructions) == "" && len(profile.Tools) == 0 && len(profile.Functions) == 0 {
		return ""
	}
	raw, err := json.Marshal(profile)
	if err != nil {
		return ""
	}
	return openAIWebHash("web-profile-v1", string(raw))
}

func openAIWebMessageHasRichContent(message apicompat.ChatMessage) bool {
	if len(message.ToolCalls) > 0 || message.FunctionCall != nil {
		return true
	}
	role := strings.ToLower(strings.TrimSpace(message.Role))
	if role == "tool" || role == "function" {
		return true
	}
	if len(message.Content) == 0 {
		return false
	}
	var value any
	if json.Unmarshal(message.Content, &value) != nil {
		return false
	}
	var rich func(any) bool
	rich = func(current any) bool {
		switch item := current.(type) {
		case map[string]any:
			typ := strings.ToLower(strings.TrimSpace(firstString(item, "type")))
			switch typ {
			case "image_url", "input_image", "file", "input_file", "image_asset_pointer", "file_asset_pointer":
				return true
			}
			for _, child := range item {
				if rich(child) {
					return true
				}
			}
		case []any:
			for _, child := range item {
				if rich(child) {
					return true
				}
			}
		}
		return false
	}
	return rich(value)
}

func firstString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := value[key].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func openAIWebRequestRequiresFullReplay(req *apicompat.ChatCompletionsRequest) bool {
	if req == nil || len(req.Tools) > 0 || len(req.Functions) > 0 {
		return true
	}
	for _, message := range req.Messages {
		if openAIWebMessageHasRichContent(message) {
			return true
		}
	}
	return false
}

func openAIWebLastUserFingerprint(req *apicompat.ChatCompletionsRequest) (string, int) {
	if req == nil {
		return "", -1
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(req.Messages[i].Role), "user") {
			raw, err := json.Marshal(req.Messages[i])
			if err != nil {
				return "", i
			}
			return openAIWebHash("web-user-v1", string(raw)), i
		}
	}
	return "", -1
}

func openAIWebCountUserMessagesAfter(req *apicompat.ChatCompletionsRequest, index int) int {
	if req == nil || index < 0 || index >= len(req.Messages) {
		return 0
	}
	count := 0
	for _, message := range req.Messages[index+1:] {
		if strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			count++
		}
	}
	return count
}

func openAIWebMessageSequenceFingerprint(messages []apicompat.ChatMessage) string {
	if len(messages) == 0 {
		return ""
	}
	raw, err := json.Marshal(messages)
	if err != nil {
		return ""
	}
	return openAIWebHash("web-transcript-v1", string(raw))
}

func openAIWebTranscriptFingerprint(messages []apicompat.ChatMessage, assistantText string) (string, int) {
	transcript := append([]apicompat.ChatMessage(nil), messages...)
	if strings.TrimSpace(assistantText) != "" {
		encoded, err := json.Marshal(assistantText)
		if err == nil {
			transcript = append(transcript, apicompat.ChatMessage{
				Role:    "assistant",
				Content: encoded,
			})
		}
	}
	return openAIWebMessageSequenceFingerprint(transcript), len(transcript)
}

func openAIWebHistoryPrefixMatches(req *apicompat.ChatCompletionsRequest, latestUserIndex int, state *OpenAIWebConversationState) bool {
	if req == nil || state == nil || latestUserIndex <= 0 || strings.TrimSpace(state.TranscriptFingerprint) == "" {
		return false
	}
	if state.TranscriptMessageCount > 0 && latestUserIndex != state.TranscriptMessageCount {
		return false
	}
	return openAIWebMessageSequenceFingerprint(req.Messages[:latestUserIndex]) == state.TranscriptFingerprint
}

func (s *OpenAIGatewayService) resetOpenAIWebContinuationState(ctx context.Context, c *gin.Context, continuation *openAIWebContinuationContext) {
	if s == nil || continuation == nil || continuation.state == nil {
		return
	}
	groupID := getOpenAIGroupIDFromContext(c)
	store := s.getOpenAIWSStateStore()
	if store != nil && groupID > 0 && continuation.stateKey != "" {
		_ = store.DeleteWebConversationState(ctx, groupID, continuation.stateKey)
	}
	if store != nil && groupID > 0 && continuation.responseAliasKey != "" && continuation.responseAliasKey != continuation.stateKey {
		_ = store.DeleteWebConversationState(ctx, groupID, continuation.responseAliasKey)
	}
	continuation.state = nil
	continuation.responseAliasKey = ""
	if strings.TrimSpace(continuation.sessionKeyHash) == "" {
		// Without a stable caller session, an old previous_response_id alias
		// must never be rebound to a newly replayed conversation.
		continuation.stateKey = ""
		continuation.eligible = false
	}
}

func (s *OpenAIGatewayService) releaseOpenAIWebContinuation(continuation *openAIWebContinuationContext) {
	if continuation == nil || continuation.lockRelease == nil {
		return
	}
	release := continuation.lockRelease
	continuation.lockRelease = nil
	release()
}

func (s *OpenAIGatewayService) prepareOpenAIWebContinuation(ctx context.Context, c *gin.Context, account *Account, model string, body []byte, req *apicompat.ChatCompletionsRequest) (*apicompat.ChatCompletionsRequest, *openAIWebContinuationContext) {
	transportReq := req
	continuation := &openAIWebContinuationContext{}
	if s == nil || account == nil || req == nil {
		return transportReq, continuation
	}
	stableID := openAIWebStableSessionID(c, body)
	previousResponseID := openAIWebPreviousResponseID(body)
	if stableID == "" && previousResponseID == "" {
		return transportReq, continuation
	}
	groupID := getOpenAIGroupIDFromContext(c)
	// Web conversation state is caller-owned. If the gateway context is not
	// backed by an authenticated API key, do not create a shared "key 0"
	// namespace that could be reused by unrelated internal callers.
	if groupID <= 0 || getAPIKeyIDFromContext(c) <= 0 {
		return transportReq, continuation
	}
	sessionHash := ""
	if stableID != "" {
		sessionHash = openAIWebSessionKeyHash(c, stableID)
	}
	profile := openAIWebProfileFingerprint(req)
	continuation.sessionKeyHash = sessionHash
	continuation.profileFingerprint = profile
	continuation.previousResponseID = previousResponseID
	store := s.getOpenAIWSStateStore()
	if store == nil {
		return transportReq, continuation
	}
	// A stable caller session is sufficient to create the first binding after
	// a complete replay. Do not require an existing cursor before marking this
	// request eligible for commit.
	if sessionHash != "" {
		continuation.stateKey = openAIWebStorageKey(c, account.ID, model, sessionHash)
		continuation.eligible = continuation.stateKey != ""
	}
	lookupStateKey := continuation.stateKey
	if previousResponseID != "" {
		// A stable caller session remains the canonical lock/write key even
		// when the request also supplies a Responses response alias. This
		// prevents alias-based requests from racing a normal session turn or
		// leaving the canonical cursor stale after a successful continuation.
		if sessionHash == "" {
			lookupStateKey = openAIWebResponseAliasKey(c, account.ID, model, previousResponseID)
		}
	}
	if locker, ok := store.(openAIWebConversationLockProvider); ok && lookupStateKey != "" {
		release, acquired := locker.AcquireOpenAIWebConversationLock(ctx, groupID, lookupStateKey)
		if !acquired {
			continuation.eligible = false
			return transportReq, continuation
		}
		continuation.lockRelease = release
	}
	var state *OpenAIWebConversationState
	stateKey := continuation.stateKey
	if previousResponseID != "" {
		alias := openAIWebResponseAliasKey(c, account.ID, model, previousResponseID)
		if alias != "" {
			loaded, found, _ := store.GetWebConversationState(ctx, groupID, alias)
			if found {
				state = loaded
				if sessionHash == "" {
					stateKey = alias
				}
			}
		}
	}
	if state == nil && sessionHash != "" {
		if stateKey != "" {
			loaded, found, _ := store.GetWebConversationState(ctx, groupID, stateKey)
			if found {
				state = loaded
			}
		}
	}
	if state == nil {
		return transportReq, continuation
	}
	continuation.state = state
	continuation.stateKey = stateKey
	continuation.responseAliasKey = openAIWebResponseAliasKey(c, account.ID, model, previousResponseID)
	if state.AccountID != account.ID || state.GroupID != groupID || strings.TrimSpace(state.Model) != strings.TrimSpace(model) || strings.TrimSpace(state.ConversationID) == "" || strings.TrimSpace(state.ParentMessageID) == "" {
		s.resetOpenAIWebContinuationState(ctx, c, continuation)
		return transportReq, continuation
	}
	if profile != "" && state.ProfileFingerprint != "" && profile != state.ProfileFingerprint {
		s.resetOpenAIWebContinuationState(ctx, c, continuation)
		return transportReq, continuation
	}
	lastUserFingerprint, lastUserIndex := openAIWebLastUserFingerprint(req)
	if lastUserFingerprint == "" {
		return transportReq, continuation
	}
	if lastUserIndex > 0 && !openAIWebHistoryPrefixMatches(req, lastUserIndex, state) {
		s.resetOpenAIWebContinuationState(ctx, c, continuation)
		continuation.eligible = continuation.stateKey != ""
		return transportReq, continuation
	}
	if lastUserFingerprint == state.LastUserFingerprint {
		return transportReq, continuation
	}
	if state.RequiresFullReplay {
		continuation.eligible = true
		return transportReq, continuation
	}
	if openAIWebRequestRequiresFullReplay(req) {
		continuation.eligible = true
		return transportReq, continuation
	}
	if lastUserIndex == len(req.Messages)-1 && len(req.Messages) == 1 {
		transportCopy := *req
		transportCopy.Messages = []apicompat.ChatMessage{req.Messages[lastUserIndex]}
		continuation.reused = true
		continuation.eligible = true
		return &transportCopy, continuation
	}
	priorIndex := -1
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(req.Messages[i].Role), "user") {
			raw, _ := json.Marshal(req.Messages[i])
			if openAIWebHash("web-user-v1", string(raw)) == state.LastUserFingerprint {
				priorIndex = i
				break
			}
		}
	}
	if priorIndex < 0 || openAIWebCountUserMessagesAfter(req, priorIndex) != 1 || lastUserIndex <= priorIndex {
		s.resetOpenAIWebContinuationState(ctx, c, continuation)
		continuation.eligible = continuation.stateKey != ""
		return transportReq, continuation
	}
	transportCopy := *req
	transportCopy.Messages = []apicompat.ChatMessage{req.Messages[lastUserIndex]}
	continuation.reused = true
	continuation.eligible = true
	return &transportCopy, continuation
}

func (s *OpenAIGatewayService) invalidateOpenAIWebContinuation(ctx context.Context, c *gin.Context, account *Account, model string, continuation *openAIWebContinuationContext) {
	if continuation == nil {
		return
	}
	defer s.releaseOpenAIWebContinuation(continuation)
	if s == nil || account == nil || continuation.state == nil {
		return
	}
	groupID := getOpenAIGroupIDFromContext(c)
	store := s.getOpenAIWSStateStore()
	if store == nil || groupID <= 0 {
		return
	}
	if continuation.stateKey != "" {
		_ = store.DeleteWebConversationState(ctx, groupID, continuation.stateKey)
	}
	if continuation.responseAliasKey != "" && continuation.responseAliasKey != continuation.stateKey {
		_ = store.DeleteWebConversationState(ctx, groupID, continuation.responseAliasKey)
	}
}

func (s *OpenAIGatewayService) commitOpenAIWebContinuation(ctx context.Context, c *gin.Context, account *Account, model string, req *apicompat.ChatCompletionsRequest, responseID string, body io.Reader, continuation *openAIWebContinuationContext) {
	defer s.releaseOpenAIWebContinuation(continuation)
	if s == nil || account == nil || req == nil || continuation == nil || !continuation.eligible {
		return
	}
	groupID := getOpenAIGroupIDFromContext(c)
	if groupID <= 0 {
		return
	}
	provider, ok := body.(OpenAIWebConversationStateProvider)
	if !ok {
		return
	}
	conversationID, parentMessageID := provider.OpenAIWebConversationState()
	if strings.TrimSpace(conversationID) == "" || strings.TrimSpace(parentMessageID) == "" {
		return
	}
	state := OpenAIWebConversationState{
		ConversationID:     strings.TrimSpace(conversationID),
		ParentMessageID:    strings.TrimSpace(parentMessageID),
		AccountID:          account.ID,
		GroupID:            groupID,
		Model:              strings.TrimSpace(model),
		SessionKeyHash:     continuation.sessionKeyHash,
		ProfileFingerprint: continuation.profileFingerprint,
		RequiresFullReplay: openAIWebRequestRequiresFullReplay(req),
	}
	state.LastUserFingerprint, _ = openAIWebLastUserFingerprint(req)
	if transcriptProvider, ok := body.(OpenAIWebConversationTranscriptProvider); ok {
		state.TranscriptFingerprint, state.TranscriptMessageCount = openAIWebTranscriptFingerprint(req.Messages, transcriptProvider.OpenAIWebAssistantText())
	}
	store := s.getOpenAIWSStateStore()
	if store == nil {
		return
	}
	stateKey := continuation.stateKey
	if stateKey == "" && continuation.sessionKeyHash != "" {
		stateKey = openAIWebStorageKey(c, account.ID, model, continuation.sessionKeyHash)
	}
	if stateKey == "" {
		return
	}
	ttl := s.openAIWSResponseStickyTTL()
	_ = store.BindWebConversationState(ctx, groupID, stateKey, state, ttl)
	if responseAlias := openAIWebResponseAliasKey(c, account.ID, model, responseID); responseAlias != "" {
		_ = store.BindWebConversationState(ctx, groupID, responseAlias, state, ttl)
	}
}
