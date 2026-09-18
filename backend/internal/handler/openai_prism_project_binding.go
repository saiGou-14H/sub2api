package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Projects are explicit and scoped to the authenticated API key. Binding
// before selection prevents a shared group from routing project files to a
// different upstream account during normal selection or failover.
func (h *OpenAIGatewayHandler) bindPrismProject(c *gin.Context, key *service.APIKey, anthropic bool) bool {
	handle := strings.TrimSpace(c.GetHeader("X-Prism-Project-ID"))
	if handle == "" {
		return true
	}
	writeError := h.errorResponse
	if anthropic {
		writeError = h.anthropicErrorResponse
	}
	if h.gatewayService == nil {
		writeError(c, http.StatusServiceUnavailable, "api_error", "Prism project service is unavailable")
		return false
	}
	project, err := h.gatewayService.LoadPrismProject(c.Request.Context(), key, handle)
	if err != nil {
		status := http.StatusNotFound
		var upstreamErr *service.OpenAIPrismHTTPError
		var workspaceErr *service.PrismWorkspaceError
		if errors.As(err, &upstreamErr) {
			status = upstreamErr.StatusCode
		} else if errors.As(err, &workspaceErr) {
			status = workspaceErr.Status
		}
		writeError(c, status, "invalid_request_error", "Prism project is unavailable for this API key")
		return false
	}
	c.Request = c.Request.WithContext(service.WithPrismProject(c.Request.Context(), project))
	return true
}
