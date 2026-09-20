package service

import (
	"context"
	"time"

	coderws "github.com/coder/websocket"
)

// ShouldBridgeWebSocket is a transport decision, not a ticket decision. Route the
// whole opted-in connection through HTTP even if its first model is not enabled:
// a later response.create may change models. Apply checks the final model each turn.
// On a read failure, an observed enabled policy and explicit account opt-in
// still use HTTP so Apply owns the missing-ticket decision. Unknown/disabled
// policy must not move or close default-off native WS connections.
func (r *CodexTicketRuntime) ShouldBridgeWebSocket(ctx context.Context, account *Account) bool {
	if r == nil || !CodexTicketAccountSupported(account) {
		return false
	}
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	knownEnabled := func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return account.CodexTurnStateEnabled() && r.knownSettings != nil && r.knownSettings.Enabled
	}
	if r.settings == nil {
		return knownEnabled()
	}
	cfg, err := r.settings.Load(readCtx)
	if err != nil {
		return knownEnabled()
	}
	r.rememberSettings(cfg)
	if !cfg.Enabled {
		return false
	}
	if r.accounts == nil {
		return account.CodexTurnStateEnabled()
	}
	fresh, err := r.accounts.GetByID(readCtx, account.ID)
	if err != nil {
		return account.CodexTurnStateEnabled()
	}
	return fresh != nil && fresh.CodexTurnStateEnabled()
}

func (s *OpenAIGatewayService) codexTicketRequiresHTTPBridge(ctx context.Context, account *Account) bool {
	return s != nil && s.codexTicketRuntime != nil && s.codexTicketRuntime.ShouldBridgeWebSocket(ctx, account)
}

func (s *OpenAIGatewayService) guardCodexTicketNativeWebSocket(ctx context.Context, account *Account) error {
	if !s.codexTicketRequiresHTTPBridge(ctx, account) {
		return nil
	}
	return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation,
		"CODEX_TICKET_RECONNECT_REQUIRED: reconnect to use the HTTP bridge", ErrCodexTicketReconnectRequired)
}
