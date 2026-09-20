//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type ticketWatchAccounts struct {
	AccountRepository
	mu       sync.Mutex
	accounts map[int64]*Account
	err      error
	block    bool
	reads    chan int64
}

func (r *ticketWatchAccounts) GetByID(ctx context.Context, id int64) (*Account, error) {
	r.mu.Lock()
	a, err, block := r.accounts[id], r.err, r.block
	var copy *Account
	if a != nil {
		value := *a
		copy = &value
	}
	r.mu.Unlock()
	select {
	case r.reads <- id:
	default:
	}
	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return copy, err
}

func newTicketWatchFixture() (*CodexTicketRuntime, *ticketWatchAccounts, CodexTicketKey) {
	r, a, _, key := ticketRuntimeFixture()
	repo := &ticketWatchAccounts{accounts: map[int64]*Account{a.a.ID: a.a}, reads: make(chan int64, 100)}
	r.accounts = repo
	return r, repo, key
}

func TestCodexTicketAccountWatcherRevokesChangedAccounts(t *testing.T) {
	for _, change := range []string{"disabled", "deleted", "identity", "prism", "inactive", "database"} {
		t.Run(change, func(t *testing.T) {
			r, repo, key := newTicketWatchFixture()
			ctx, stop := r.watchCodexTicketAccount(context.Background(), key, 5*time.Millisecond)
			defer stop()
			select {
			case <-repo.reads:
			case <-time.After(time.Second):
				t.Fatal("watcher did not check account")
			}
			repo.mu.Lock()
			a := repo.accounts[key.AccountID]
			switch change {
			case "disabled":
				a.Extra = map[string]any{CodexTurnStateEnabledExtraKey: false}
			case "deleted":
				delete(repo.accounts, key.AccountID)
			case "identity":
				a.Credentials = map[string]any{"chatgpt_account_id": "other"}
			case "prism":
				a.Extra = map[string]any{CodexTurnStateEnabledExtraKey: true, OpenAIWebTransportExtraKey: OpenAITransportPrism}
			case "inactive":
				a.Status = StatusDisabled
			case "database":
				repo.err = errors.New("database unavailable")
			}
			repo.mu.Unlock()
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("account change did not cancel")
			}
			require.ErrorIs(t, ctx.Err(), context.Canceled)
		})
	}
}

func TestCodexTicketAccountWatcherDoesNotCancelOtherAccount(t *testing.T) {
	r, repo, key := newTicketWatchFixture()
	other := *repo.accounts[key.AccountID]
	other.ID = 2
	repo.accounts[other.ID] = &other
	otherKey := key
	otherKey.AccountID = 2
	ctxA, stopA := r.watchCodexTicketAccount(context.Background(), key, 5*time.Millisecond)
	defer stopA()
	ctxB, stopB := r.watchCodexTicketAccount(context.Background(), otherKey, 5*time.Millisecond)
	defer stopB()
	repo.mu.Lock()
	repo.accounts[key.AccountID].Extra = map[string]any{CodexTurnStateEnabledExtraKey: false}
	repo.mu.Unlock()
	select {
	case <-ctxA.Done():
	case <-time.After(time.Second):
		t.Fatal("disabled account did not cancel")
	}
	for {
		select {
		case id := <-repo.reads:
			if id == other.ID {
				require.NoError(t, ctxB.Err())
				return
			}
		case <-time.After(time.Second):
			t.Fatal("other account watcher stopped")
		}
	}
}

func TestCodexTicketAccountWatcherCancelsBlockedReadAndJoins(t *testing.T) {
	r, repo, key := newTicketWatchFixture()
	repo.block = true
	ctx, stop := r.watchCodexTicketAccount(context.Background(), key, time.Millisecond)
	select {
	case <-repo.reads:
	case <-time.After(time.Second):
		t.Fatal("read did not start")
	}
	joined := make(chan struct{})
	go func() { stop(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("blocked repository query did not cancel/join")
	}
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestCodexTicketAccountWatcherBoundsDatabaseTimeout(t *testing.T) {
	r, repo, key := newTicketWatchFixture()
	repo.block = true
	ctx, stop := r.watchCodexTicketAccount(context.Background(), key, time.Millisecond)
	defer stop()
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("database read exceeded its two-second deadline")
	}
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}
