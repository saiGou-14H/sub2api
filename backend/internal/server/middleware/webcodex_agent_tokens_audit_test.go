package middleware

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type agentAuditBody struct {
	io.Reader
	reads int
}

func (b *agentAuditBody) Read(p []byte) (int, error) { b.reads++; return b.Reader.Read(p) }
func (*agentAuditBody) Close() error                 { return nil }

func TestWebCodexAgentTokenAuditOmitsWholeBodyAndResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &auditCaptureRepository{}
	audit := service.NewAuditLogService(repo, nil)
	audit.Start()
	defer audit.Stop()
	const fakeHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const fakePlaintext = "wc_agent_fabricated_response_secret_never_recorded"
	const fakeName = "credential_hidden_in_name"
	const fakeJWT = "fabricated-header-token-never-recorded"
	for _, action := range []string{"create", "register_hash", "list", "revoke"} {
		for _, status := range []int{200, 400, 500} {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(ContextKeyUser), AuthSubject{UserID: 42})
				c.Set(string(ContextKeyUserRole), service.RoleUser)
				c.Next()
			}, gin.HandlerFunc(NewAuditLogMiddleware(audit)))
			body := &agentAuditBody{Reader: strings.NewReader(`{"token_hash":"` + fakeHash + `","name":"` + fakeName + `","token":"` + fakePlaintext + `"}`)}
			path := "/api/agent-tokens/" + action
			router.POST(path, func(c *gin.Context) {
				require.Zero(t, body.reads, "audit must not consume credential bodies before bounded parser")
				content, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				require.True(t, strings.Contains(string(content), fakeHash))
				c.JSON(status, gin.H{"token": fakePlaintext})
			})
			req := httptest.NewRequest(http.MethodPost, path, body)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+fakeJWT)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			require.Equal(t, status, rec.Code)
		}
	}
	audit.Stop()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.logs, 12)
	for _, entry := range repo.logs {
		require.True(t, entry.RequestBody == "<credential-bearing body omitted>")
		require.Equal(t, int64(42), *entry.ActorUserID)
		require.Equal(t, service.RoleUser, entry.ActorRole)
		require.Equal(t, service.AuditAuthMethodJWT, entry.AuthMethod)
		require.NotEmpty(t, entry.Action)
		encoded, err := json.Marshal(entry)
		require.NoError(t, err)
		for _, secret := range []string{fakeHash, fakePlaintext, fakeName, fakeJWT} {
			require.False(t, strings.Contains(string(encoded), secret), "audit entry contains a fabricated credential marker")
		}
	}
}
