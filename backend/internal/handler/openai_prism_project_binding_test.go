package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const prismBindingHandle = "prism_proj_00000000-0000-4000-8000-000000000001"

type prismBindingStore struct {
	service.GatewayCache
	service.PrismProjectStore
	payload []byte
	err     error
	reads   int
}

func (s *prismBindingStore) GetPrismProject(_ context.Context, handle string) ([]byte, error) {
	s.reads++
	if s.err != nil {
		return nil, s.err
	}
	if handle != prismBindingHandle {
		return nil, nil
	}
	return append([]byte(nil), s.payload...), nil
}

type prismBindingAccountRepo struct {
	service.AccountRepository
	account    *service.Account
	loads      int
	selections int
}

func (r *prismBindingAccountRepo) GetByID(_ context.Context, id int64) (*service.Account, error) {
	r.loads++
	if r.account == nil || r.account.ID != id {
		return nil, errors.New("account not found")
	}
	clone := *r.account
	return &clone, nil
}

func (r *prismBindingAccountRepo) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]service.Account, error) {
	r.selections++
	return nil, errors.New("unexpected account scheduling")
}

type prismBindingUpstream struct{ calls int }

func (u *prismBindingUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	u.calls++
	return nil, errors.New("unexpected upstream request")
}

func (u *prismBindingUpstream) DoWithTLS(r *http.Request, p string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(r, p, id, n)
}

func prismBindingHandlerFixture(t *testing.T) (*OpenAIGatewayHandler, *service.APIKey, *prismBindingStore, *prismBindingAccountRepo, *prismBindingUpstream) {
	t.Helper()
	groupID := int64(17)
	key := &service.APIKey{ID: 23, UserID: 31, GroupID: &groupID, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI}, User: &service.User{ID: 31}}
	account := &service.Account{ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeSetupToken, GroupIDs: []int64{groupID}, Credentials: map[string]any{"access_token": "synthetic-access"}, Extra: map[string]any{service.OpenAIWebTransportExtraKey: service.OpenAITransportPrism}}
	material, err := json.Marshal([]string{"synthetic-access", "", ""})
	require.NoError(t, err)
	digest := sha256.Sum256(material)
	payload, err := json.Marshal(map[string]any{
		"Handle": prismBindingHandle, "Title": "Test project", "Model": service.OpenAIPrismDefaultModel,
		"APIKeyID": key.ID, "UserID": key.UserID, "GroupID": groupID, "AccountID": account.ID,
		"CredentialHash": hex.EncodeToString(digest[:]),
		"State":          service.OpenAIPrismSessionState{ProjectID: "synthetic-private-project", SandboxToken: "synthetic-private-sandbox"},
		"CreatedAt":      time.Now(), "ExpiresAt": time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	store := &prismBindingStore{payload: payload}
	repo := &prismBindingAccountRepo{account: account}
	upstream := &prismBindingUpstream{}
	gateway := service.NewOpenAIGatewayService(
		repo, nil, nil, nil, nil, nil, store, &config.Config{}, nil, nil, nil,
		nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	t.Cleanup(gateway.CloseOpenAIWSPool)
	return &OpenAIGatewayHandler{gatewayService: gateway}, key, store, repo, upstream
}

func TestBindPrismProjectStoresOnlyAuthenticatedProjectInRequestContext(t *testing.T) {
	for _, anthropic := range []bool{false, true} {
		h, key, store, repo, upstream := prismBindingHandlerFixture(t)
		c, w := prismWorkspaceHandlerContext(http.MethodPost, "/v1/responses", `{}`)
		c.Request.Header.Set("X-Prism-Project-ID", "  "+prismBindingHandle+"  ")
		require.True(t, h.bindPrismProject(c, key, anthropic))
		project := service.PrismProjectFromContext(c.Request.Context())
		require.NotNil(t, project)
		require.Equal(t, prismBindingHandle, project.Handle)
		require.Equal(t, key.ID, project.APIKeyID)
		require.Equal(t, repo.account.ID, project.AccountID)
		require.Equal(t, 1, store.reads)
		require.Equal(t, 1, repo.loads)
		require.Zero(t, repo.selections)
		require.Zero(t, upstream.calls)
		require.Empty(t, w.Body.String(), "binding must not expose private project state")
	}
}

func TestPrismProjectHeaderBindsBeforeThreeProtocolHandlersContinue(t *testing.T) {
	for _, protocol := range []struct {
		path string
		call func(*OpenAIGatewayHandler, *gin.Context)
	}{
		{"/v1/responses", (*OpenAIGatewayHandler).Responses},
		{"/v1/chat/completions", (*OpenAIGatewayHandler).ChatCompletions},
		{"/v1/messages", (*OpenAIGatewayHandler).Messages},
	} {
		t.Run(protocol.path, func(t *testing.T) {
			h, key, store, repo, upstream := prismBindingHandlerFixture(t)
			c, w := prismWorkspaceHandlerContext(http.MethodPost, protocol.path, `invalid-json-before-scheduling`)
			c.Set(string(middleware2.ContextKeyAPIKey), key)
			c.Request.Header.Set("X-Prism-Project-ID", prismBindingHandle)
			// Deliberately omit the auth subject: the public handler must bind
			// the project before reaching its next admission stage or reading JSON.
			protocol.call(h, c)
			require.Equal(t, http.StatusInternalServerError, w.Code)
			require.Contains(t, w.Body.String(), "User context not found")
			project := service.PrismProjectFromContext(c.Request.Context())
			require.NotNil(t, project)
			require.Equal(t, prismBindingHandle, project.Handle)
			require.Equal(t, 1, store.reads)
			require.Equal(t, 1, repo.loads)
			require.Zero(t, repo.selections)
			require.Zero(t, upstream.calls)
		})
	}
}

func TestPrismProjectHeaderRejectsMissingOrOtherOwnerBeforeScheduling(t *testing.T) {
	for _, protocol := range []struct {
		name string
		call func(*OpenAIGatewayHandler, *gin.Context)
	}{
		{"responses", (*OpenAIGatewayHandler).Responses},
		{"chat", (*OpenAIGatewayHandler).ChatCompletions},
		{"anthropic", (*OpenAIGatewayHandler).Messages},
	} {
		for _, test := range []struct {
			name   string
			status int
		}{
			{"missing", http.StatusNotFound}, {"other user", http.StatusNotFound},
			{"other key", http.StatusNotFound}, {"cache unavailable", http.StatusServiceUnavailable},
		} {
			t.Run(protocol.name+"/"+test.name, func(t *testing.T) {
				h, key, store, repo, upstream := prismBindingHandlerFixture(t)
				handle := prismBindingHandle
				switch test.name {
				case "missing":
					handle = "prism_proj_00000000-0000-4000-8000-000000000002"
				case "other user":
					key.UserID++
				case "other key":
					key.ID++
				case "cache unavailable":
					store.err = errors.New("synthetic-private-cache-details")
				}
				c, w := prismWorkspaceHandlerContext(http.MethodPost, "/v1/"+protocol.name, `{}`)
				c.Set(string(middleware2.ContextKeyAPIKey), key)
				c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: key.UserID, Concurrency: 1})
				c.Request.Header.Set("X-Prism-Project-ID", handle)
				protocol.call(h, c)
				require.Equal(t, test.status, w.Code)
				require.Contains(t, w.Body.String(), "Prism project is unavailable for this API key")
				require.NotContains(t, w.Body.String(), "synthetic-private")
				require.Nil(t, service.PrismProjectFromContext(c.Request.Context()))
				require.Zero(t, repo.loads, "owner rejection must precede even account lookup")
				require.Zero(t, repo.selections)
				require.Zero(t, upstream.calls)
			})
		}
	}
}

func TestBindPrismProjectAbsentHeaderDoesNotTouchProjectServices(t *testing.T) {
	c, w := prismWorkspaceHandlerContext(http.MethodPost, "/v1/responses", `{}`)
	require.True(t, (&OpenAIGatewayHandler{}).bindPrismProject(c, nil, false))
	require.Nil(t, service.PrismProjectFromContext(c.Request.Context()))
	require.Empty(t, w.Body.String())
}
