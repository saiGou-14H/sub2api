package handler

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func prismWorkspaceHandlerContext(method, target, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, w
}

func prismWorkspaceHandlerIdentity(c *gin.Context) *service.APIKey {
	groupID := int64(17)
	key := &service.APIKey{ID: 23, UserID: 31, User: &service.User{ID: 31}, GroupID: &groupID, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI}}
	c.Set(string(middleware2.ContextKeyAPIKey), key)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: key.UserID, Concurrency: 2})
	return key
}

func TestPrismWorkspaceRequiresCompleteIdentityAndServices(t *testing.T) {
	for _, handlerCall := range []struct {
		name string
		call func(*OpenAIGatewayHandler, *gin.Context)
	}{
		{"create", (*OpenAIGatewayHandler).CreatePrismProject},
		{"operation", func(h *OpenAIGatewayHandler, c *gin.Context) { h.PrismProject(c, "access") }},
	} {
		for _, test := range []struct {
			name   string
			setup  func(*gin.Context)
			status int
		}{
			{"no key", func(*gin.Context) {}, http.StatusUnauthorized},
			{"no subject", func(c *gin.Context) {
				prismWorkspaceHandlerIdentity(c)
				delete(c.Keys, string(middleware2.ContextKeyUser))
			}, http.StatusUnauthorized},
			{"mismatched subject", func(c *gin.Context) {
				prismWorkspaceHandlerIdentity(c)
				c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 99})
			}, http.StatusUnauthorized},
			{"missing group", func(c *gin.Context) { prismWorkspaceHandlerIdentity(c).Group = nil }, http.StatusForbidden},
			{"other platform", func(c *gin.Context) { prismWorkspaceHandlerIdentity(c).Group.Platform = service.PlatformComposite }, http.StatusForbidden},
			{"missing services", func(c *gin.Context) { prismWorkspaceHandlerIdentity(c) }, http.StatusServiceUnavailable},
		} {
			t.Run(handlerCall.name+"/"+test.name, func(t *testing.T) {
				c, w := prismWorkspaceHandlerContext(http.MethodPost, "/v1/prism/projects", `{}`)
				test.setup(c)
				handlerCall.call(&OpenAIGatewayHandler{}, c)
				require.Equal(t, test.status, w.Code)
			})
		}
	}
}

type prismWorkspaceConcurrencyCache struct {
	service.ConcurrencyCache
	acquired bool
	err      error
	calls    int
	released int
}

func (s *prismWorkspaceConcurrencyCache) AcquireUserSlot(context.Context, int64, int, string) (bool, error) {
	s.calls++
	return s.acquired, s.err
}

func (s *prismWorkspaceConcurrencyCache) ReleaseUserSlot(context.Context, int64, string) error {
	s.released++
	return nil
}

type prismWorkspaceBillingCache struct {
	service.BillingCache
	calls int
}

func (s *prismWorkspaceBillingCache) GetUserBalance(context.Context, int64) (float64, error) {
	s.calls++
	return 0, nil
}

func TestPrismWorkspaceBillingDenialPreventsConcurrencyAndUpstream(t *testing.T) {
	balance := &prismWorkspaceBillingCache{}
	billing := service.NewBillingCacheService(balance, nil, nil, nil, nil, nil, &config.Config{RunMode: config.RunModeStandard}, nil)
	t.Cleanup(billing.Stop)
	concurrency := &prismWorkspaceConcurrencyCache{acquired: true}
	h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}, billingCacheService: billing, concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(concurrency), SSEPingFormatNone, 0)}
	c, w := prismWorkspaceHandlerContext(http.MethodPost, "/v1/prism/projects", `{}`)
	prismWorkspaceHandlerIdentity(c)
	h.CreatePrismProject(c)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Equal(t, 1, balance.calls)
	require.Zero(t, concurrency.calls)
}

func TestPrismWorkspaceConcurrencyAndReleaseOnInvalidRequest(t *testing.T) {
	for _, test := range []struct {
		name     string
		acquired bool
		err      error
		status   int
		released int
	}{
		{"cache unavailable", false, errors.New("offline"), http.StatusServiceUnavailable, 0},
		{"at limit", false, nil, http.StatusTooManyRequests, 0},
		{"invalid JSON releases slot", true, nil, http.StatusBadRequest, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &config.Config{RunMode: config.RunModeSimple}, nil)
			t.Cleanup(billing.Stop)
			concurrency := &prismWorkspaceConcurrencyCache{acquired: test.acquired, err: test.err}
			h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}, billingCacheService: billing, concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(concurrency), SSEPingFormatNone, 0)}
			c, w := prismWorkspaceHandlerContext(http.MethodPost, "/v1/prism/projects", `null`)
			prismWorkspaceHandlerIdentity(c)
			h.CreatePrismProject(c)
			require.Equal(t, test.status, w.Code)
			require.Equal(t, 1, concurrency.calls)
			require.Equal(t, test.released, concurrency.released)
		})
	}
}

func TestDecodePrismProjectCreateRequest(t *testing.T) {
	c, _ := prismWorkspaceHandlerContext(http.MethodPost, "/", `{"title":"Paper","model":"prism-model"}`)
	var request service.PrismProjectCreateRequest
	require.NoError(t, decodePrismProjectJSON(c, &request, false))
	require.Equal(t, "Paper", request.Title)
	require.Equal(t, "prism-model", request.Model)
	for _, body := range []string{"", "null", "[]", `{"title":3}`, `{} {}`, `{"title":"incomplete"`} {
		c, _ := prismWorkspaceHandlerContext(http.MethodPost, "/", body)
		require.Error(t, decodePrismProjectJSON(c, &request, false), body)
	}
	c, _ = prismWorkspaceHandlerContext(http.MethodPost, "/", `{}`)
	request = service.PrismProjectCreateRequest{}
	require.NoError(t, decodePrismProjectJSON(c, &request, false))
	require.Empty(t, request.Model, "service supplies the default model")
}

func TestPrismProjectOperationParsing(t *testing.T) {
	c, _ := prismWorkspaceHandlerContext(http.MethodPost, "/", `{"Name":"upload","main_document":"main.tex","client_state_vector":"vector","client_delete_set_update":"delete-set"}`)
	request, err := parsePrismProjectOperation(c, "render")
	require.NoError(t, err)
	require.Equal(t, "render", request.Name, "client JSON cannot change the route operation")
	require.Equal(t, "main.tex", request.MainDocument)
	require.Equal(t, "vector", request.ClientStateVector)
	require.Equal(t, "delete-set", request.ClientDeleteSetUpdate)
	c, _ = prismWorkspaceHandlerContext(http.MethodGet, "/?main_document=paper.tex&include_bibliography=true&page=2&page_size=50&wait_ms=30000", "")
	request, err = parsePrismProjectOperation(c, "version-history")
	require.NoError(t, err)
	require.Equal(t, 2, request.Page)
	require.Equal(t, 50, request.PageSize)
	require.Equal(t, 30000, request.WaitMS)
	require.True(t, request.IncludeBibliography)
	require.Equal(t, "paper.tex", request.MainDocument)
	for _, query := range []string{"page=no", "page=-1", "page_size=101", "include_bibliography=maybe", "wait_ms=30001"} {
		c, _ := prismWorkspaceHandlerContext(http.MethodGet, "/?"+query, "")
		_, err := parsePrismProjectOperation(c, "version-history")
		require.Error(t, err, query)
	}
	for _, name := range []string{"heartbeat", "wait-for-sync"} {
		c, _ := prismWorkspaceHandlerContext(http.MethodPost, "/", "")
		_, err := parsePrismProjectOperation(c, name)
		require.NoError(t, err)
	}
	c, _ = prismWorkspaceHandlerContext(http.MethodGet, "/", "")
	request, err = parsePrismProjectOperation(c, "sync-status")
	require.NoError(t, err)
	require.Equal(t, "sync-status", request.Name)
	c, _ = prismWorkspaceHandlerContext(http.MethodPatch, "/", `{"file_id":"prism_file_synthetic"}`)
	request, err = parsePrismProjectOperation(c, "thumbnail")
	require.NoError(t, err)
	require.Equal(t, "prism_file_synthetic", request.FileID)
	c, _ = prismWorkspaceHandlerContext(http.MethodPatch, "/", `{}`)
	_, err = parsePrismProjectOperation(c, "thumbnail")
	require.Error(t, err)
}

func prismWorkspaceUploadContext(t *testing.T, field string, size int) *gin.Context {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile(field, "paper.tex")
	require.NoError(t, err)
	_, err = io.CopyN(part, strings.NewReader(strings.Repeat("x", size)), int64(size))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	return c
}

func TestPrismProjectUploadLimitsAndTemporaryFileCleanup(t *testing.T) {
	for _, size := range []int{1, (1 << 20) + 1, prismProjectUploadLimit} {
		c := prismWorkspaceUploadContext(t, "file", size)
		request, err := parsePrismProjectUpload(c)
		require.NoError(t, err)
		require.Len(t, request.Data, size)
		require.Equal(t, byte('x'), request.Data[0])
		require.Equal(t, "paper.tex", request.FileName)
		require.Equal(t, "upload", request.Name)
		if size > 1<<20 {
			_, err := c.Request.MultipartForm.File["file"][0].Open()
			require.True(t, os.IsNotExist(err), "disk-backed upload must be removed after parsing")
		}
	}
	for _, size := range []int{prismProjectUploadLimit + 1, prismProjectUploadLimit + (1 << 20) + 1} {
		c := prismWorkspaceUploadContext(t, "file", size)
		c.Request.ContentLength = -1
		_, err := parsePrismProjectUpload(c)
		var requestErr *service.PrismWorkspaceError
		require.ErrorAs(t, err, &requestErr)
		require.Equal(t, http.StatusRequestEntityTooLarge, requestErr.Status)
	}
	for _, test := range []struct {
		field string
		size  int
	}{{"other", 1}, {"file", 0}} {
		_, err := parsePrismProjectUpload(prismWorkspaceUploadContext(t, test.field, test.size))
		require.Error(t, err)
	}
}

func TestPrismWorkspaceErrorsHideUpstreamSecrets(t *testing.T) {
	for _, err := range []error{
		&service.OpenAIPrismHTTPError{StatusCode: http.StatusTooManyRequests, Message: "synthetic-private-token", Path: "/private-provider-path"},
		errors.New("synthetic-private-token"),
	} {
		c, w := prismWorkspaceHandlerContext(http.MethodGet, "/", "")
		(&OpenAIGatewayHandler{}).writePrismWorkspaceError(c, err)
		require.GreaterOrEqual(t, w.Code, 400)
		require.NotContains(t, w.Body.String(), "synthetic-private-token")
		require.NotContains(t, w.Body.String(), "/private-provider-path")
	}
}
