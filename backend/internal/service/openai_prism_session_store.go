package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Private cursors live in their own namespace. Deployments with a cache must
// use it authoritatively so two gateway instances observe the same state.
type openAIPrismSessionCache interface {
	SetPrismSession(context.Context, string, []byte, time.Duration) error
	GetPrismSession(context.Context, string) ([]byte, error)
}

func prismSessionCacheKey(key openAIPrismSessionKey) string {
	data := fmt.Sprintf("%d:%d:%d:%x:%t:%s", key.groupID, key.apiKeyID, key.accountID, key.credentialHash, key.promptBridge, key.responseID)
	digest := sha256.Sum256([]byte(data))
	return hex.EncodeToString(digest[:])
}

type prismStoredSession struct {
	State         OpenAIPrismSessionState
	ProjectHandle string
}

func (s *OpenAIGatewayService) persistPrismSession(ctx context.Context, key openAIPrismSessionKey, state OpenAIPrismSessionState, projectHandle string) error {
	cache, ok := s.cache.(openAIPrismSessionCache)
	if !ok {
		return nil
	}
	data, err := json.Marshal(prismStoredSession{State: state, ProjectHandle: projectHandle})
	if err != nil {
		return errors.New("encode Prism continuation state")
	}
	cacheCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := cache.SetPrismSession(cacheCtx, prismSessionCacheKey(key), data, openaiStickySessionTTL); err != nil {
		return errors.New("save Prism continuation state")
	}
	return nil
}

func (s *OpenAIGatewayService) loadPrismSession(ctx context.Context, key openAIPrismSessionKey) (openAIPrismSession, error) {
	missing := &OpenAIPrismHTTPError{StatusCode: http.StatusBadRequest, Message: "Prism continuation is unavailable, expired, or uses a different tool mode; resend full history without previous_response_id"}
	if cache, ok := s.cache.(openAIPrismSessionCache); ok {
		cacheCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		data, err := cache.GetPrismSession(cacheCtx, prismSessionCacheKey(key))
		if err != nil {
			return openAIPrismSession{}, &OpenAIPrismHTTPError{StatusCode: http.StatusServiceUnavailable, Message: "Prism continuation store is unavailable"}
		}
		if len(data) == 0 {
			return openAIPrismSession{}, missing
		}
		var session prismStoredSession
		if json.Unmarshal(data, &session) != nil || session.State.ResponseID != key.responseID || session.State.ProjectID == "" {
			return openAIPrismSession{}, missing
		}
		return openAIPrismSession{state: session.State, projectHandle: session.ProjectHandle}, nil
	}
	value, found := s.openaiPrismSessions.Load(key)
	session, valid := value.(openAIPrismSession)
	if !found || !valid || !session.expiresAt.After(time.Now()) {
		s.openaiPrismSessions.Delete(key)
		return openAIPrismSession{}, missing
	}
	return session, nil
}

// Use the existing compare-and-delete lease provider under a Prism-specific
// name; neither Web cursors nor Web locks can collide with this lease.
func (s *OpenAIGatewayService) lockPrismContinuation(ctx context.Context, key openAIPrismSessionKey, state OpenAIPrismSessionState) (func(), error) {
	provider, ok := s.cache.(openAIWebConversationDistributedLockProvider)
	if !ok || key.groupID <= 0 || state.ProjectID == "" {
		return func() {}, nil
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", key.apiKeyID, key.accountID, state.ProjectID)))
	lockKey := "prism-continuation:" + hex.EncodeToString(digest[:])
	cacheCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	owner, acquired, err := provider.TryAcquireOpenAIWebConversationLock(cacheCtx, key.groupID, lockKey, 15*time.Minute)
	cancel()
	if err != nil || !acquired {
		return nil, &OpenAIPrismHTTPError{StatusCode: http.StatusConflict, Message: "Prism conversation is busy; wait for its current operation to finish"}
	}
	return func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer releaseCancel()
		_ = provider.ReleaseOpenAIWebConversationLock(releaseCtx, key.groupID, lockKey, owner)
	}, nil
}
