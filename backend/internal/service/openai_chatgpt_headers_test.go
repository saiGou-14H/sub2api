package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// stubChatGPTHeadersRepo 是最小化 AccountRepository stub，仅实现 GetByID，
// 供 TestResolveAndSetOpenAIChatGPTAccountHeaders 使用。
type stubChatGPTHeadersRepo struct {
	AccountRepository
	byID map[int64]*Account
}

func (r *stubChatGPTHeadersRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	return r.byID[id], nil
}

func TestResolveAndSetOpenAIChatGPTAccountHeaders(t *testing.T) {
	ctx := context.Background()
	pid := int64(100)

	parentCreds := map[string]any{"chatgpt_account_id": "org-parent"}
	parent := &Account{
		ID:          100,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Credentials: parentCreds,
	}
	repo := &stubChatGPTHeadersRepo{byID: map[int64]*Account{100: parent}}

	t.Run("shadow_resolves_to_parent_org", func(t *testing.T) {
		shadow := &Account{
			ID:              200,
			ParentAccountID: &pid,
			Platform:        PlatformOpenAI,
			Type:            AccountTypeOAuth,
		}
		headers := make(http.Header)
		err := resolveAndSetOpenAIChatGPTAccountHeaders(ctx, repo, headers, shadow)
		require.NoError(t, err)
		require.Equal(t, "org-parent", headers.Get("chatgpt-account-id"),
			"影子账号应透传母账号的 chatgpt-account-id")
	})

	t.Run("normal_account_passthrough", func(t *testing.T) {
		ownCreds := map[string]any{"chatgpt_account_id": "org-own"}
		normal := &Account{
			ID:          300,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeOAuth,
			Credentials: ownCreds,
		}
		headers := make(http.Header)
		err := resolveAndSetOpenAIChatGPTAccountHeaders(ctx, repo, headers, normal)
		require.NoError(t, err)
		require.Equal(t, "org-own", headers.Get("chatgpt-account-id"),
			"普通账号应透传自身的 chatgpt-account-id")
	})
}

func TestOpenAIChatGPTAccountIDFallsBackToAccessTokenClaims(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-from-access-token",
		},
	})
	require.NoError(t, err)
	accessToken := "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeSetupToken,
		Credentials: map[string]any{
			"access_token": accessToken,
		},
	}

	require.Equal(t, "acct-from-access-token", account.GetChatGPTAccountID())
	headers := make(http.Header)
	setOpenAIChatGPTAccountHeaders(headers, account)
	require.Equal(t, "acct-from-access-token", headers.Get("chatgpt-account-id"))
}

func TestOpenAIChatGPTAccountIDIgnoresOpaqueAccessToken(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeSetupToken,
		Credentials: map[string]any{
			"access_token": "opaque-access-token",
		},
	}

	require.Empty(t, account.GetChatGPTAccountID())
	headers := make(http.Header)
	setOpenAIChatGPTAccountHeaders(headers, account)
	require.Empty(t, headers.Get("chatgpt-account-id"))
}
