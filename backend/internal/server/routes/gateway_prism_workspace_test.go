package routes

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

var prismWorkspaceRouteCases = []struct {
	method    string
	suffix    string
	operation string
}{
	{http.MethodGet, "", "access"},
	{http.MethodGet, "/conversation-history", "history"},
	{http.MethodGet, "/delta-files", "delta-files"},
	{http.MethodGet, "/sync-status", "sync-status"},
	{http.MethodPost, "/files", "upload"},
	{http.MethodPatch, "/thumbnail", "thumbnail"},
	{http.MethodPost, "/render", "render"},
	{http.MethodGet, "/render-status", "render-status"},
	{http.MethodGet, "/pdf", "pdf"},
	{http.MethodGet, "/logs", "logs"},
	{http.MethodGet, "/synctex", "synctex"},
	{http.MethodGet, "/word-count", "word-count"},
	{http.MethodGet, "/latest-render", "latest-render"},
	{http.MethodGet, "/version-history", "version-history"},
	{http.MethodPost, "/heartbeat", "heartbeat"},
	{http.MethodPost, "/wait-for-sync", "wait-for-sync"},
}

func TestPrismWorkspaceRouteDispatchKeepsOperationAndProjectHandle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/v1")
	group.Use(func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer synthetic-key" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	})
	registerPrismProjectRoutes(group, func(c *gin.Context) { c.String(http.StatusCreated, "created") }, func(c *gin.Context, operation string) {
		c.String(http.StatusOK, operation+":"+c.Param("project_id"))
	})
	for _, route := range prismWorkspaceRouteCases {
		request := httptest.NewRequest(route.method, "/v1/prism/projects/prism_proj_synthetic"+route.suffix, nil)
		request.Header.Set("Authorization", "Bearer synthetic-key")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, request)
		require.Equal(t, http.StatusOK, w.Code, route.suffix)
		require.Equal(t, route.operation+":prism_proj_synthetic", w.Body.String(), route.suffix)
		unauthorized := httptest.NewRecorder()
		router.ServeHTTP(unauthorized, httptest.NewRequest(route.method, request.URL.String(), nil))
		require.Equal(t, http.StatusUnauthorized, unauthorized.Code, route.suffix)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/prism/projects", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer synthetic-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, request)
	require.Equal(t, http.StatusCreated, w.Code)
}

func prismWorkspaceFullRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterGatewayRoutes(router, &handler.Handlers{Gateway: &handler.GatewayHandler{}, OpenAIGateway: &handler.OpenAIGatewayHandler{}}, servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer synthetic-key" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		groupID := int64(7)
		c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{ID: 11, UserID: 13, User: &service.User{ID: 13}, GroupID: &groupID, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"allowed-prism-model"}}}})
		c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 13, Concurrency: 1})
		c.Next()
	}), nil, nil, nil, nil, nil, &config.Config{Gateway: config.GatewayConfig{MaxBodySize: 1024 * 1024, TextMaxBodySize: 1024 * 1024}})
	return router
}

func TestPrismWorkspaceRoutesUseGatewayAuthentication(t *testing.T) {
	router := prismWorkspaceFullRouter()
	for _, route := range prismWorkspaceRouteCases {
		for _, authorized := range []bool{false, true} {
			request := httptest.NewRequest(route.method, "/v1/prism/projects/prism_proj_synthetic"+route.suffix, strings.NewReader(`{}`))
			request.Header.Set("Content-Type", "application/json")
			if authorized {
				request.Header.Set("Authorization", "Bearer synthetic-key")
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, request)
			if authorized {
				require.Equal(t, http.StatusServiceUnavailable, w.Code, route.suffix)
				require.Contains(t, w.Body.String(), "Prism project service is unavailable")
			} else {
				require.Equal(t, http.StatusUnauthorized, w.Code, route.suffix)
			}
		}
	}
}

func TestPrismWorkspaceUploadDoesNotUseJSONModelMiddleware(t *testing.T) {
	router := prismWorkspaceFullRouter()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "not-a-chat-request-model"))
	file, err := writer.CreateFormFile("file", "paper.tex")
	require.NoError(t, err)
	_, err = io.WriteString(file, "binary\x00payload")
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	request := httptest.NewRequest(http.MethodPost, "/v1/prism/projects/prism_proj_synthetic/files", &body)
	request.Header.Set("Authorization", "Bearer synthetic-key")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, request)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, "request must reach the authenticated workspace handler")
	require.Contains(t, w.Body.String(), "Prism project service is unavailable")
	require.NotContains(t, w.Body.String(), "not available for this group")
}
