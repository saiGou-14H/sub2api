//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type statekitConcurrentBudgetStore struct {
	*ticketTestStore
	mu        sync.Mutex
	used      int
	committed chan struct{}
	once      sync.Once
}

func (s *statekitConcurrentBudgetStore) AcquireProbeBudget(context.Context, string, uint64, int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used >= 2 {
		return false, nil
	}
	s.used++
	return true, nil
}
func (s *statekitConcurrentBudgetStore) CommitIfCurrent(_ context.Context, _ string, t CodexTicket) (bool, error) {
	if t.Verified {
		s.once.Do(func() { close(s.committed) })
	}
	return true, nil
}

func TestCodexTicketTwoRequestBudgetDoesNotStarveConcurrentVerification(t *testing.T) {
	r, accounts, base, _ := ticketRuntimeFixture()
	cfg := base.v.Control.Settings
	cfg.Models = []string{"first-model", "second-model"}
	cfg.MaxConcurrency = 2
	cfg.MaxProbesPerMinute = 2
	r.settings = &ticketTestSettings{cfg: cfg}
	base.v.Control.Settings = cfg
	base.v.Ticket = nil
	accounts.a.Credentials["access_token"] = "test-token"
	store := &statekitConcurrentBudgetStore{ticketTestStore: base, committed: make(chan struct{})}
	r.cache = store
	gateway := &OpenAIGatewayService{accountRepo: accounts, codexTicketRuntime: r}
	var captures, verifications atomic.Int32
	gateway.httpUpstream = &codexProbeUpstream{call: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}
		if req.Header.Get("X-Codex-Turn-State") == "" {
			captures.Add(1)
			// Give a wrongly admitted second worker time to consume the last budget
			// unit before the first worker attempts verification.
			timer := time.NewTimer(30 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-timer.C:
			}
			return codexProbeSuccessResponse(body.Model, codexProbeValidCandidate()), nil
		}
		verifications.Add(1)
		return codexProbeSuccessResponse(body.Model, ""), nil
	}}
	r.SetProbe(gateway.ProbeCodexTicket)
	r.SetCandidatePager(ticketTestPager{*accounts.a})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); r.collect(ctx, "owner", cfg, "http://harvest.invalid:8080") }()
	committed := false
	select {
	case <-store.committed:
		committed = true
	case <-ctx.Done():
	}
	cancel()
	<-done
	require.True(t, committed, "budget=2 and configured workers=2 must allow one complete capture+verify")
	require.EqualValues(t, 1, captures.Load())
	require.EqualValues(t, 1, verifications.Load())
	store.mu.Lock()
	used := store.used
	store.mu.Unlock()
	require.Equal(t, 2, used)
	require.Equal(t, 2, cfg.MaxConcurrency, "worker admission must not rewrite the configured concurrency")
}
