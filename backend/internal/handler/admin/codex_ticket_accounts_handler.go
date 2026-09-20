package admin

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *CodexTicketHandler) SetAccountsProvider(p func(context.Context, []int64) (service.CodexTicketAccountsStatus, error)) {
	h.accounts = p
}
func parseCodexTicketAccountIDs(raw string) ([]int64, error) {
	invalid := errors.New("invalid account ids")
	if len(raw) == 0 || len(raw) > 2100 {
		return nil, invalid
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 100 {
		return nil, invalid
	}
	seen := map[int64]bool{}
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			return nil, invalid
		}
		for _, ch := range p {
			if ch < '0' || ch > '9' {
				return nil, invalid
			}
		}
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil || id <= 0 {
			return nil, invalid
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}
func (h *CodexTicketHandler) Accounts(c *gin.Context) {
	values := c.QueryArray("account_ids")
	if len(values) != 1 {
		response.ErrorWithDetails(c, 400, "account_ids must contain 1 to 100 positive integers", "CODEX_TICKET_INVALID_ACCOUNT_IDS", nil)
		return
	}
	ids, err := parseCodexTicketAccountIDs(values[0])
	if err != nil {
		response.ErrorWithDetails(c, 400, "account_ids must contain 1 to 100 positive integers", "CODEX_TICKET_INVALID_ACCOUNT_IDS", nil)
		return
	}
	if h.accounts == nil {
		response.ErrorWithDetails(c, 503, "account status unavailable", codexTicketControlUnavailable, nil)
		return
	}
	out, err := h.accounts(c.Request.Context(), ids)
	if err != nil {
		response.ErrorWithDetails(c, 503, "account status unavailable", codexTicketControlUnavailable, nil)
		return
	}
	response.Success(c, out)
}
