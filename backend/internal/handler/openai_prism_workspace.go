package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const prismProjectUploadLimit = 32 << 20

// Workspace routes share API-key auth and body limits with the gateway, while
// project ownership, upstream account selection and project locks stay in service.
func (h *OpenAIGatewayHandler) CreatePrismProject(c *gin.Context) {
	key, release, ok := h.authorizePrismWorkspace(c)
	if !ok {
		return
	}
	defer release()
	var request service.PrismProjectCreateRequest
	if err := decodePrismProjectJSON(c, &request, false); err != nil {
		h.writePrismWorkspaceError(c, err)
		return
	}
	project, err := h.gatewayService.CreatePrismProject(c.Request.Context(), key, request)
	if err != nil {
		h.writePrismWorkspaceError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, project.Public())
}

func (h *OpenAIGatewayHandler) PrismProject(c *gin.Context, operation string) {
	key, release, ok := h.authorizePrismWorkspace(c)
	if !ok {
		return
	}
	defer release()
	request, err := parsePrismProjectOperation(c, operation)
	if err != nil {
		h.writePrismWorkspaceError(c, err)
		return
	}
	result, err := h.gatewayService.OperatePrismProject(c.Request.Context(), key, c.Param("project_id"), request)
	if err != nil {
		h.writePrismWorkspaceError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	if result.ContentType == "application/pdf" {
		c.Header("Content-Disposition", `inline; filename="document.pdf"`)
	}
	c.Data(http.StatusOK, result.ContentType, result.Body)
}

func (h *OpenAIGatewayHandler) authorizePrismWorkspace(c *gin.Context) (*service.APIKey, func(), bool) {
	key, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || key == nil || key.ID <= 0 || key.User == nil {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return nil, nil, false
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 || subject.UserID != key.UserID || key.User.ID != key.UserID {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid authenticated user")
		return nil, nil, false
	}
	if key.GroupID == nil || key.Group == nil || key.Group.ID != *key.GroupID || key.Group.Platform != service.PlatformOpenAI {
		h.errorResponse(c, http.StatusForbidden, "permission_error", "Prism projects require an OpenAI group")
		return nil, nil, false
	}
	if h.gatewayService == nil || h.billingCacheService == nil || h.concurrencyHelper == nil || h.concurrencyHelper.concurrencyService == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Prism project service is unavailable")
		return nil, nil, false
	}
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), key.User, key, key.Group, subscription, service.QuotaPlatform(c.Request.Context(), key)); err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return nil, nil, false
	}
	release, acquired, err := h.concurrencyHelper.TryAcquireUserSlot(c.Request.Context(), subject.UserID, subject.Concurrency)
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Prism concurrency is unavailable")
		return nil, nil, false
	}
	if !acquired {
		h.errorResponse(c, http.StatusTooManyRequests, "rate_limit_error", "Prism concurrency limit reached")
		return nil, nil, false
	}
	if release == nil {
		release = func() {}
	}
	return key, wrapReleaseOnDone(c.Request.Context(), release), true
}

func decodePrismProjectJSON(c *gin.Context, target any, allowEmpty bool) error {
	decoder := json.NewDecoder(c.Request.Body)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		if allowEmpty && errors.Is(err, io.EOF) {
			return nil
		}
		return prismProjectParseError(err)
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return prismProjectParseError(nil)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return prismProjectParseError(err)
	}
	if err := decoder.Decode(&raw); !errors.Is(err, io.EOF) {
		return prismProjectParseError(err)
	}
	return nil
}

func prismProjectParseError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return &service.PrismWorkspaceError{Status: http.StatusRequestEntityTooLarge, Message: "Prism request body is too large"}
	}
	return &service.PrismWorkspaceError{Status: http.StatusBadRequest, Message: "Prism request must contain one valid JSON object"}
}

func parsePrismProjectOperation(c *gin.Context, operation string) (service.PrismProjectOperation, error) {
	request := service.PrismProjectOperation{}
	switch operation {
	case "upload":
		return parsePrismProjectUpload(c)
	case "render", "thumbnail", "heartbeat", "wait-for-sync":
		if err := decodePrismProjectJSON(c, &request, operation == "heartbeat" || operation == "wait-for-sync"); err != nil {
			return request, err
		}
	case "access", "history", "delta-files", "sync-status", "render-status", "pdf", "logs", "synctex", "word-count", "latest-render", "version-history":
		request.MainDocument = c.Query("main_document")
		if value, present := c.GetQuery("include_bibliography"); present {
			var err error
			request.IncludeBibliography, err = strconv.ParseBool(value)
			if err != nil {
				return request, &service.PrismWorkspaceError{Status: http.StatusBadRequest, Message: "Invalid include_bibliography"}
			}
		}
		for _, field := range []struct {
			name  string
			value *int
		}{{"page", &request.Page}, {"page_size", &request.PageSize}, {"wait_ms", &request.WaitMS}} {
			if value, present := c.GetQuery(field.name); present {
				parsed, err := strconv.Atoi(value)
				if err != nil || parsed < 0 {
					return request, &service.PrismWorkspaceError{Status: http.StatusBadRequest, Message: "Invalid Prism pagination or wait parameter"}
				}
				*field.value = parsed
			}
		}
	default:
		return request, &service.PrismWorkspaceError{Status: http.StatusNotFound, Message: "Unknown Prism project operation"}
	}
	// The route chooses the operation; JSON must never override it via Name.
	request.Name = operation
	if request.Page < 0 || request.PageSize < 0 || request.PageSize > 100 || request.WaitMS < 0 || request.WaitMS > 30000 {
		return request, &service.PrismWorkspaceError{Status: http.StatusBadRequest, Message: "Invalid Prism pagination or wait parameter"}
	}
	if operation == "thumbnail" && strings.TrimSpace(request.FileID) == "" {
		return request, &service.PrismWorkspaceError{Status: http.StatusBadRequest, Message: "file_id is required"}
	}
	return request, nil
}

func parsePrismProjectUpload(c *gin.Context) (service.PrismProjectOperation, error) {
	request := service.PrismProjectOperation{Name: "upload"}
	invalid := &service.PrismWorkspaceError{Status: http.StatusBadRequest, Message: "Provide one non-empty multipart file named file"}
	tooLarge := &service.PrismWorkspaceError{Status: http.StatusRequestEntityTooLarge, Message: "Prism file must not exceed 32 MiB"}
	// Bound disk spooling too, including multipart overhead and unrelated parts.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, prismProjectUploadLimit+(1<<20))
	err := c.Request.ParseMultipartForm(1 << 20)
	if c.Request.MultipartForm != nil {
		defer func() { _ = c.Request.MultipartForm.RemoveAll() }()
	}
	if err != nil {
		var limitErr *http.MaxBytesError
		if errors.As(err, &limitErr) {
			return request, tooLarge
		}
		return request, invalid
	}
	form := c.Request.MultipartForm
	if form == nil || len(form.File) != 1 || len(form.File["file"]) != 1 {
		return request, invalid
	}
	file := form.File["file"][0]
	if file.Size > prismProjectUploadLimit {
		return request, tooLarge
	}
	name := path.Base(strings.ReplaceAll(file.Filename, "\\", "/"))
	if file.Size <= 0 || name == "." || name == "/" || len(name) > 255 || strings.ContainsAny(name, "\r\n\x00") {
		return request, invalid
	}
	source, err := file.Open()
	if err != nil {
		return request, invalid
	}
	defer func() { _ = source.Close() }()
	data, err := io.ReadAll(io.LimitReader(source, prismProjectUploadLimit+1))
	if len(data) > prismProjectUploadLimit {
		return request, tooLarge
	}
	if err != nil || len(data) == 0 {
		return request, invalid
	}
	contentType, _, err := mime.ParseMediaType(file.Header.Get("Content-Type"))
	if err != nil || contentType == "" {
		contentType = "application/octet-stream"
	}
	request.FileName, request.ContentType, request.Data = name, contentType, data
	return request, nil
}

func (h *OpenAIGatewayHandler) writePrismWorkspaceError(c *gin.Context, err error) {
	status, message := http.StatusBadGateway, "Prism project request failed"
	var workspaceErr *service.PrismWorkspaceError
	var upstreamErr *service.OpenAIPrismHTTPError
	if errors.As(err, &workspaceErr) {
		status, message = workspaceErr.Status, workspaceErr.Message
	} else if errors.As(err, &upstreamErr) {
		status, message = upstreamErr.StatusCode, "Prism upstream rejected the request"
	} else if errors.Is(err, service.ErrNoAvailableAccounts) {
		status, message = http.StatusServiceUnavailable, "No Prism account is available"
	}
	if status < 400 || status > 599 {
		status = http.StatusBadGateway
	}
	code := "api_error"
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		code = "invalid_request_error"
	case http.StatusUnauthorized:
		code = "authentication_error"
	case http.StatusForbidden:
		code = "permission_error"
	case http.StatusNotFound:
		code = "not_found_error"
	case http.StatusTooManyRequests:
		code = "rate_limit_error"
	}
	h.errorResponse(c, status, code, message)
}
