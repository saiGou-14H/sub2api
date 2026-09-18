package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type prismSharedSessionCache struct {
	GatewayCache
	data map[string][]byte
	err  error
}

func (c *prismSharedSessionCache) SetPrismSession(_ context.Context, key string, data []byte, _ time.Duration) error {
	if c.err != nil {
		return c.err
	}
	if c.data == nil {
		c.data = make(map[string][]byte)
	}
	c.data[key] = append([]byte(nil), data...)
	return nil
}
func (c *prismSharedSessionCache) GetPrismSession(_ context.Context, key string) ([]byte, error) {
	return append([]byte(nil), c.data[key]...), c.err
}

func TestOpenAIPrismSessionSurvivesGatewayRestartAndRemainsIsolated(t *testing.T) {
	ctx := context.Background()
	cache := &prismSharedSessionCache{}
	first := &OpenAIGatewayService{cache: cache}
	second := &OpenAIGatewayService{cache: cache}
	key := openAIPrismSessionKey{accountID: 5, apiKeyID: 7, groupID: 9, credentialHash: [32]byte{3}, responseID: "resp_test"}
	state := OpenAIPrismSessionState{ProjectID: "project-test", ResponseID: key.responseID, SandboxToken: "private-test-token", CodexListenSnapshot: json.RawMessage(`{"cursor":9007199254740993,"unknown":null}`), DeltaFiles: json.RawMessage(`[]`), Cookies: []*http.Cookie{{Name: "prism_session_token", Value: "refreshed-test-token"}}}
	require.NoError(t, first.persistPrismSession(ctx, key, state, "prism-project-test"))
	loaded, err := second.loadPrismSession(ctx, key)
	require.NoError(t, err)
	require.Equal(t, state, loaded.state)
	require.Equal(t, "prism-project-test", loaded.projectHandle)
	for _, mutate := range []func(*openAIPrismSessionKey){
		func(k *openAIPrismSessionKey) { k.apiKeyID++ },
		func(k *openAIPrismSessionKey) { k.accountID++ },
		func(k *openAIPrismSessionKey) { k.groupID++ },
		func(k *openAIPrismSessionKey) { k.credentialHash[0]++ },
		func(k *openAIPrismSessionKey) { k.promptBridge = true },
		func(k *openAIPrismSessionKey) { k.responseID = "other-response" },
	} {
		other := key
		mutate(&other)
		_, err := second.loadPrismSession(ctx, other)
		require.Error(t, err)
	}
	cache.err = errors.New("cache offline")
	second.openaiPrismSessions.Store(key, openAIPrismSession{state: state, expiresAt: time.Now().Add(time.Hour)})
	_, err = second.loadPrismSession(ctx, key)
	var upstream *OpenAIPrismHTTPError
	require.ErrorAs(t, err, &upstream)
	require.Equal(t, http.StatusServiceUnavailable, upstream.StatusCode, "a local copy must not hide a shared store outage")
}

func TestOpenAIPrismSessionLocalExpiry(t *testing.T) {
	s := &OpenAIGatewayService{}
	key := openAIPrismSessionKey{responseID: "resp_test"}
	s.openaiPrismSessions.Store(key, openAIPrismSession{expiresAt: time.Now().Add(-time.Second)})
	_, err := s.loadPrismSession(context.Background(), key)
	require.Error(t, err)
	_, found := s.openaiPrismSessions.Load(key)
	require.False(t, found)
}
