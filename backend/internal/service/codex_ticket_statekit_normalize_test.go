//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCodexTicketPlanCommonAccountNormalization(t *testing.T) {
	for _, plan := range []any{"pro", "team", "inherit"} {
		extra, err := normalizeOpenAITransportExtra(PlatformOpenAI, AccountTypeOAuth, map[string]any{CodexTurnStatePlanExtraKey: plan})
		require.NoError(t, err)
		require.Equal(t, plan, extra[CodexTurnStatePlanExtraKey])
	}
	for _, plan := range []any{"Pro", "", " premium ", 292, true, nil} {
		_, err := normalizeOpenAITransportExtra(PlatformOpenAI, AccountTypeOAuth, map[string]any{CodexTurnStatePlanExtraKey: plan})
		require.Error(t, err)
	}
	a := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{CodexTurnStateEnabledExtraKey: true, CodexTurnStatePlanExtraKey: "team"}}
	extra, err := normalizeOpenAILongContextBillingUpdateExtra(a, &UpdateAccountInput{Extra: map[string]any{"other": true}})
	require.NoError(t, err)
	require.Equal(t, "team", extra[CodexTurnStatePlanExtraKey])
	extra, err = normalizeOpenAILongContextBillingUpdateExtra(a, &UpdateAccountInput{Extra: map[string]any{CodexTurnStatePlanExtraKey: "inherit"}})
	require.NoError(t, err)
	require.Equal(t, "inherit", extra[CodexTurnStatePlanExtraKey])
	require.Equal(t, "team", a.Extra[CodexTurnStatePlanExtraKey])
}
