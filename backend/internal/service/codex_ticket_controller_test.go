//go:build unit

package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type ticketTestPager struct{ a Account }

func (p ticketTestPager) ListOAuthRefreshCandidatePage(context.Context, OAuthRefreshPageOptions) (*OAuthRefreshCandidatePage, error) {
	return &OAuthRefreshCandidatePage{Accounts: []Account{p.a}}, nil
}
func TestCodexTicketCollectorFixedWorkersCancelAndJoin(t *testing.T) {
	r, a, s, _ := ticketRuntimeFixture()
	cfg := s.v.Control.Settings
	cfg.MaxConcurrency = 2
	cfg.Models = []string{"one", "two", "three"}
	s.v.Ticket = nil
	r.SetCandidatePager(ticketTestPager{*a.a})
	entered := make(chan struct{}, 4)
	var active, peak atomic.Int32
	r.SetProbe(func(ctx context.Context, _ int64, _, _ string) (CodexTicketProbeResult, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		entered <- struct{}{}
		<-ctx.Done()
		return CodexTicketProbeResult{}, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); r.collect(ctx, "owner", cfg, "http://proxy") }()
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("fixed workers did not enter probe")
		}
	}
	require.Equal(t, int32(2), peak.Load())
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("collector did not join canceled workers")
	}
	require.Zero(t, active.Load())
	r.mu.Lock()
	defer r.mu.Unlock()
	require.Zero(t, r.inflight)
	require.Zero(t, r.pending)
}

type ticketLifecycleStore struct {
	*ticketTestStore
	published chan struct{}
	released  chan struct{}
}

func (s *ticketLifecycleStore) TryAcquireLeader(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}
func (s *ticketLifecycleStore) RenewLeader(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}
func (s *ticketLifecycleStore) PublishControl(context.Context, CodexTicketControl) (bool, error) {
	select {
	case s.published <- struct{}{}:
	default:
	}
	return true, nil
}
func (s *ticketLifecycleStore) ReleaseLeader(context.Context, string) error {
	close(s.released)
	return nil
}
func TestCodexTicketRuntimeShutdownJoinsAndReleases(t *testing.T) {
	r, _, s, _ := ticketRuntimeFixture()
	r.settings.(*ticketTestSettings).cfg.Enabled = false
	store := &ticketLifecycleStore{ticketTestStore: s, published: make(chan struct{}, 1), released: make(chan struct{})}
	r.cache = store
	r.Start(context.Background())
	r.Start(context.Background())
	select {
	case <-store.published:
	case <-time.After(time.Second):
		t.Fatal("controller did not publish")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, r.Shutdown(ctx))
	select {
	case <-store.released:
	default:
		t.Fatal("shutdown returned before ownership was released")
	}
	require.NoError(t, r.Shutdown(ctx))
}

type ticketFailingSettings struct {
	CodexTicketSettingsRepository
	cfg  CodexTicketSettings
	fail atomic.Bool
}

func (s *ticketFailingSettings) Load(context.Context) (CodexTicketSettings, error) {
	if s.fail.Load() {
		return CodexTicketSettings{}, errors.New("database unavailable")
	}
	return s.cfg, nil
}

type ticketTestProxy struct{ ProxyRepository }

func (ticketTestProxy) GetByID(context.Context, int64) (*Proxy, error) {
	return &Proxy{ID: 1, Protocol: "http", Host: "proxy.invalid", Port: 8080, Status: StatusActive}, nil
}
func TestCodexTicketSnapshotFailureCancelsGenerationWithoutRepublishing(t *testing.T) {
	r, a, s, _ := ticketRuntimeFixture()
	cfg := s.v.Control.Settings
	cfg.Models = cfg.Models[:1]
	cfg.MaxConcurrency = 1
	id := int64(1)
	cfg.HarvestProxyID = &id
	s.v.Ticket = nil
	settings := &ticketFailingSettings{cfg: cfg}
	r.settings = settings
	r.proxies = ticketTestProxy{}
	r.SetCandidatePager(ticketTestPager{*a.a})
	store := &ticketLifecycleStore{ticketTestStore: s, published: make(chan struct{}, 4), released: make(chan struct{})}
	r.cache = store
	entered := make(chan struct{})
	canceled := make(chan struct{})
	r.SetProbe(func(ctx context.Context, _ int64, _, _ string) (CodexTicketProbeResult, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		return CodexTicketProbeResult{}, ctx.Err()
	})
	root, stop := context.WithCancel(context.Background())
	defer stop()
	r.Start(root)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("probe did not start")
	}
	<-store.published
	settings.fail.Store(true)
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("failed fresh DB snapshot did not cancel probe")
	}
	select {
	case <-store.published:
		t.Fatal("failed DB snapshot was republished")
	default:
	}
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, r.Shutdown(shutdown))
}
