package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

const CodexTurnStateEnabledExtraKey = "codex_turn_state_enabled"

// CodexTicketAccountSupported excludes other OpenAI transports even when they
// expose the same model names. A missing account switch never opts in.
func CodexTicketAccountSupported(a *Account) bool {
	return a != nil && a.IsOpenAIOAuthLike() && a.UsesOpenAICodexProtocol() &&
		!a.IsShadow() && !a.IsOpenAIAgentIdentity()
}

func (a *Account) CodexTurnStateEnabled() bool {
	if !CodexTicketAccountSupported(a) {
		return false
	}
	enabled, ok := a.Extra[CodexTurnStateEnabledExtraKey].(bool)
	return ok && enabled
}

// CodexTicketIdentityScope binds short-lived state to the actual upstream
// identity, not a mutable access token. Account ID is a separate cache key part.
func CodexTicketIdentityScope(a *Account) string {
	if !CodexTicketAccountSupported(a) {
		return ""
	}
	accountID := strings.TrimSpace(a.GetChatGPTAccountID())
	if accountID == "" {
		return ""
	}
	// Read these fields for both OAuth and setup-token; the older public
	// getters intentionally cover OAuth only. Stable JWT identity claims also
	// fence access-token-only reimports within the same ChatGPT workspace.
	subject, tokenUser, tokenOrg := "", "", ""
	if claims, err := openai.DecodeIDToken(a.GetOpenAIAccessToken()); err == nil && claims != nil {
		subject = strings.TrimSpace(claims.Sub)
		if claims.OpenAIAuth != nil {
			tokenUser = strings.TrimSpace(claims.OpenAIAuth.ChatGPTUserID)
			if tokenUser == "" {
				tokenUser = strings.TrimSpace(claims.OpenAIAuth.UserID)
			}
			tokenOrg = strings.TrimSpace(claims.OpenAIAuth.POID)
		}
	}
	payload, _ := json.Marshal([]string{accountID,
		strings.TrimSpace(a.GetCredential("chatgpt_user_id")), strings.TrimSpace(a.GetCredential("organization_id")),
		subject, tokenUser, tokenOrg})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func ValidateCodexTurnStateExtra(extra map[string]any) error {
	if value, exists := extra[CodexTurnStateEnabledExtraKey]; exists {
		if _, ok := value.(bool); !ok {
			return infraerrors.BadRequest("CODEX_TURN_STATE_INVALID", "codex_turn_state_enabled must be a boolean")
		}
	}
	return nil
}
