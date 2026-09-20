//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCodexTicketAccountOptInAndTransport(t *testing.T) {
	for _, mode := range []string{"codex", "web", "prism"} {
		for _, kind := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
			a := &Account{ID: 1, Platform: PlatformOpenAI, Type: kind, Extra: map[string]any{OpenAIWebTransportExtraKey: mode}}
			require.False(t, a.CodexTurnStateEnabled())
			a.Extra[CodexTurnStateEnabledExtraKey] = true
			require.Equal(t, mode == "codex" && kind != AccountTypeAPIKey, a.CodexTurnStateEnabled(), "%s/%s", mode, kind)
			a.Extra[CodexTurnStateEnabledExtraKey] = "true"
			require.False(t, a.CodexTurnStateEnabled())
		}
	}
	var absent *Account
	require.False(t, absent.CodexTurnStateEnabled())
	parent := int64(1)
	shadow := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent, Extra: map[string]any{CodexTurnStateEnabledExtraKey: true}}
	require.False(t, shadow.CodexTurnStateEnabled())
}

func TestCodexTicketIdentityBinding(t *testing.T) {
	a := &Account{ID: 5, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "identity-a", "access_token": "old"}}
	first := CodexTicketIdentityScope(a)
	require.NotEmpty(t, first)
	a.Credentials["access_token"] = "refreshed"
	require.Equal(t, first, CodexTicketIdentityScope(a))
	a.Credentials["chatgpt_account_id"] = "identity-b"
	require.NotEqual(t, first, CodexTicketIdentityScope(a))
	delete(a.Credentials, "chatgpt_account_id")
	require.Empty(t, CodexTicketIdentityScope(a))
}

func TestCodexTicketAccountEditPreservesOmittedOptIn(t *testing.T) {
	a := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{CodexTurnStateEnabledExtraKey: true}}
	out, err := normalizeOpenAILongContextBillingUpdateExtra(a, &UpdateAccountInput{Extra: map[string]any{"other": true}})
	require.NoError(t, err)
	require.Equal(t, true, out[CodexTurnStateEnabledExtraKey])
	out, err = normalizeOpenAILongContextBillingUpdateExtra(a, &UpdateAccountInput{Extra: map[string]any{CodexTurnStateEnabledExtraKey: false}})
	require.NoError(t, err)
	require.Equal(t, false, out[CodexTurnStateEnabledExtraKey])
	require.Equal(t, true, a.Extra[CodexTurnStateEnabledExtraKey])
}

func TestCodexTicketAccountRejectsNonBoolean(t *testing.T) {
	s := &adminServiceImpl{}
	for _, v := range []any{nil, "true", 1, map[string]any{}} {
		extra := map[string]any{CodexTurnStateEnabledExtraKey: v}
		require.Error(t, ValidateOpenAITransportExtra(PlatformOpenAI, AccountTypeOAuth, extra))
		require.Error(t, s.UpdateAccountExtra(context.Background(), 1, extra))
		_, err := s.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{Extra: extra})
		require.Error(t, err)
	}
	require.NoError(t, ValidateCodexTurnStateExtra(map[string]any{CodexTurnStateEnabledExtraKey: false}))
}
