package service

import (
	"context"
	"time"
)

// Each running worker owns at most one watcher; queued accounts allocate none.
// It is independent of the candidate queue so a full queue cannot delay revoke.
func (r *CodexTicketRuntime) watchCodexTicketAccount(parent context.Context, key CodexTicketKey, interval time.Duration) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer cancel()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !r.codexTicketAccountCurrent(ctx, key) {
					return
				}
			}
		}
	}()
	return ctx, func() {
		cancel()
		<-done
	}
}

func (r *CodexTicketRuntime) codexTicketAccountCurrent(ctx context.Context, key CodexTicketKey) bool {
	check, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	account, err := r.accounts.GetByID(check, key.AccountID)
	return err == nil && check.Err() == nil && account != nil && account.ID == key.AccountID &&
		account.CodexTurnStateEnabled() && CodexTicketIdentityScope(account) == key.IdentityScope &&
		account.IsSchedulableForModelWithContext(check, key.Model)
}
