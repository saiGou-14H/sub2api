package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func prismAccount(extra map[string]any) *Account {
	if extra == nil {
		extra = map[string]any{OpenAIWebTransportExtraKey: OpenAITransportPrism}
	}
	return &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra}
}

func TestOpenAIPrismTransportIsIndependentFromCodexAndWeb(t *testing.T) {
	prism := prismAccount(map[string]any{OpenAIWebTransportExtraKey: " PRISM "})
	require.Equal(t, OpenAITransportPrism, prism.OpenAITransport())
	require.True(t, prism.IsOpenAIPrismTransport())
	require.False(t, prism.IsOpenAIWebTransport())
	require.False(t, prism.UsesOpenAICodexProtocol())
	require.False(t, prism.IsOpenAIResponsesWebSocketV2Enabled())
	require.Equal(t, OpenAIWSIngressModeOff, prism.ResolveOpenAIResponsesWebSocketV2Mode(OpenAIWSIngressModeCtxPool))
	require.False(t, prism.IsOpenAIPassthroughEnabled())
	require.False(t, prism.IsCodexCLIOnlyEnabled())

	codex := prismAccount(map[string]any{OpenAIWebTransportExtraKey: OpenAITransportCodex})
	require.True(t, codex.UsesOpenAICodexProtocol())
	web := prismAccount(map[string]any{OpenAIWebTransportExtraKey: OpenAITransportWeb})
	require.True(t, web.IsOpenAIWebTransport())
	require.False(t, web.UsesOpenAICodexProtocol())
}

func TestOpenAIPrismModelAllowlistDoesNotUseCodexAliases(t *testing.T) {
	account := prismAccount(nil)
	require.True(t, account.IsModelSupported(OpenAIPrismDefaultModel))
	require.False(t, account.IsModelSupported("gpt-5.4-codex"))
	require.Equal(t, []string{OpenAIPrismDefaultModel}, OpenAIPrismAccountModels(account))

	account.Credentials = map[string]any{"model_mapping": map[string]any{
		"my-prism-model": "provider-model",
	}}
	require.False(t, account.IsModelSupported(OpenAIPrismDefaultModel))
	require.True(t, account.IsModelSupported("my-prism-model"))
	require.Equal(t, []string{"my-prism-model"}, OpenAIPrismAccountModels(account))

	wildcard := prismAccount(nil)
	wildcard.Credentials = map[string]any{"model_mapping": map[string]any{"gpt-*": OpenAIPrismDefaultModel}}
	require.Empty(t, OpenAIPrismAccountModels(wildcard))
	require.False(t, wildcard.IsModelSupported(OpenAIPrismDefaultModel), "models absent from the concrete allowlist must not be enabled by wildcard mappings")
}

func TestOpenAIPrismEndpointCapabilitiesAreConservative(t *testing.T) {
	account := prismAccount(nil)
	require.True(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityChatCompletions))
	require.True(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityResponses))
	require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityLive))
	require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityAlphaSearch))
	require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityEmbeddings))
}

func TestOpenAIPrismSessionTokenIsSensitive(t *testing.T) {
	require.True(t, IsSensitiveCredentialKey("prism_session_token"))
	out := MergePreservingSensitiveCreds(
		map[string]any{"prism_session_token": "old"},
		map[string]any{"model_mapping": map[string]any{"x": "y"}},
	)
	require.Equal(t, "old", out["prism_session_token"])
}

func TestValidateOpenAITransportExtraAcceptsPrismOnlyForOAuthLikeAccounts(t *testing.T) {
	require.NoError(t, ValidateOpenAITransportExtra(PlatformOpenAI, AccountTypeOAuth, map[string]any{
		OpenAIWebTransportExtraKey: "PrIsM",
	}))
	require.NoError(t, ValidateOpenAITransportExtra(PlatformOpenAI, AccountTypeSetupToken, map[string]any{
		OpenAIWebTransportExtraKey: OpenAITransportPrism,
	}))
	require.Error(t, ValidateOpenAITransportExtra(PlatformOpenAI, AccountTypeAPIKey, map[string]any{
		OpenAIWebTransportExtraKey: OpenAITransportPrism,
	}))
}
