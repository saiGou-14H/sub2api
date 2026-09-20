package service

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// SetCodexTicketRuntime is called once during dependency wiring, before serving.
func (s *OpenAIGatewayService) SetCodexTicketRuntime(runtime *CodexTicketRuntime) {
	s.codexTicketRuntime = runtime
}

var ErrCodexTicketReconnectRequired = errors.New("codex ticket reconnect required")

func IsCodexTicketPolicyError(err error) bool {
	return errors.Is(err, ErrCodexTicketMissing) || errors.Is(err, ErrCodexTicketControlUnavailable) || errors.Is(err, ErrCodexTicketReconnectRequired)
}

// Run after account echo isolation and all outbound identity/header rewriting.
// The body already contains the final upstream model. Legacy compact has a
// different protocol; the dedicated counter endpoint does not use this builder.
func (s *OpenAIGatewayService) applyCodexTicket(ctx context.Context, c *gin.Context, account *Account, body []byte, headers http.Header) error {
	if s.codexTicketRuntime == nil || !CodexTicketAccountSupported(account) || isOpenAIResponsesCompactPath(c) {
		return nil
	}
	model := gjson.GetBytes(body, "model").String()
	err := s.codexTicketRuntime.Apply(ctx, account, model, headers)
	if err != nil && c != nil && c.Writer != nil {
		code := "CODEX_TICKET_CONTROL_UNAVAILABLE"
		if errors.Is(err, ErrCodexTicketMissing) {
			code = "CODEX_TICKET_MISSING"
		}
		writeOpenAIResponsesFallbackError(c, http.StatusServiceUnavailable, code, "Codex turn-state is not ready; retry later or change the configured missing-ticket policy")
	}
	return err
}
