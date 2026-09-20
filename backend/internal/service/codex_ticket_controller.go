package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type codexTicketGeneration struct {
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	timer    *time.Timer
	revision uint64
	proxyURL string
}

func (r *CodexTicketRuntime) run(ctx context.Context) {
	for ctx.Err() == nil {
		var random [16]byte
		if _, e := rand.Read(random[:]); e != nil {
			r.setError("random_unavailable")
			return
		}
		owner := hex.EncodeToString(random[:])
		op, cancel := context.WithTimeout(ctx, 2*time.Second)
		ok, err := r.cache.TryAcquireLeader(op, owner, 15*time.Second)
		cancel()
		if err == nil && ok {
			r.lead(ctx, owner)
		} else if err != nil {
			r.setError("control_unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}
func (r *CodexTicketRuntime) lead(root context.Context, owner string) {
	ctx, cancel := context.WithCancel(root)
	defer cancel()
	renewed := make(chan struct{})
	go func() {
		defer close(renewed)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				op, stop := context.WithTimeout(ctx, 2*time.Second)
				ok, e := r.cache.RenewLeader(op, owner, 15*time.Second)
				stop()
				if e != nil || !ok {
					r.setError("lease_lost")
					cancel()
					return
				}
			}
		}
	}()
	var gen *codexTicketGeneration
	stopGen := func() {
		if gen != nil {
			gen.cancel()
			gen.timer.Stop()
			<-gen.done
			gen = nil
		}
	}
	defer func() {
		cancel()
		// Cleanup has its own bounded context only to revoke ownership. It is
		// never used to continue a probe after root cancellation.
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
		_ = r.cache.ReleaseLeader(cleanup, owner)
		stop()
		stopGen()
		<-renewed
	}()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		// Freshness starts before both DB reads; Redis latency is subtracted using
		// local monotonic elapsed time. Never extend a failed previous snapshot.
		started := time.Now()
		op, stop := context.WithTimeout(ctx, 2*time.Second)
		now, e := r.cache.Now(op)
		var cfg CodexTicketSettings
		var p *Proxy
		state := "missing"
		if e == nil {
			cfg, e = r.settings.Load(op)
		}
		if e == nil && cfg.HarvestProxyID != nil {
			p, e = r.proxies.GetByID(op, *cfg.HarvestProxyID)
			if e != nil {
				state = "missing"
				p = nil
				if errors.Is(e, ErrProxyNotFound) {
					e = nil
				}
			}
		}
		// A missing proxy is an applied unavailable state; DB failures must not
		// publish. Re-read settings to fence a proxy change between the two reads.
		if e == nil {
			var confirm CodexTicketSettings
			confirm, e = r.settings.Load(op)
			if e == nil && confirm.Revision != cfg.Revision {
				e = ErrCodexTicketControlUnavailable
			}
		}
		if e == nil && op.Err() != nil {
			e = op.Err()
		}
		stop()
		if e != nil || time.Since(started) >= 2*time.Second {
			stopGen()
			r.setError("snapshot_unavailable")
		} else {
			r.rememberSettings(cfg)
			if p != nil {
				state = "active"
				switch {
				case !p.IsActive():
					state = "inactive"
				case p.IsExpired(now):
					state = "expired"
				case p.Host == "" || p.Port <= 0:
					state = "unsupported"
				case p.Protocol != "http" && p.Protocol != "https" && p.Protocol != "socks5" && p.Protocol != "socks5h":
					state = "unsupported"
				}
			}
			control := CodexTicketControl{Owner: owner, Settings: cfg, ProxyState: state, ObservedAtMS: now.UnixMilli(), ValidUntilMS: now.Add(6 * time.Second).UnixMilli()}
			if p != nil && p.ExpiresAt != nil {
				control.ProxyExpiresAtMS = p.ExpiresAt.UnixMilli()
			}
			op, stop = context.WithTimeout(ctx, 2*time.Second)
			ok, err := r.cache.PublishControl(op, control)
			stop()
			if err != nil || !ok {
				stopGen()
				r.setError("control_unavailable")
			} else {
				r.mu.Lock()
				switch r.lastError {
				case "snapshot_unavailable", "control_unavailable", "lease_lost":
					r.lastError = ""
				}
				for k, expires := range r.ready {
					if k.Revision != cfg.Revision || !expires.After(now) {
						delete(r.ready, k)
					}
				}
				r.mu.Unlock()
				if gen != nil && (gen.revision != cfg.Revision || !cfg.Enabled || state != "active" || gen.ctx.Err() != nil || gen.proxyURL != p.URL()) {
					stopGen()
				}
				remaining := 6*time.Second - time.Since(started)
				if p != nil && p.ExpiresAt != nil {
					until := p.ExpiresAt.Sub(now) - time.Since(started)
					if until < remaining {
						remaining = until
					}
				}
				if cfg.Enabled && state == "active" && remaining > 0 {
					if gen == nil {
						gctx, gcancel := context.WithCancel(ctx)
						gen = &codexTicketGeneration{ctx: gctx, cancel: gcancel, done: make(chan struct{}), revision: cfg.Revision, proxyURL: p.URL()}
						gen.timer = time.AfterFunc(remaining, gcancel)
						go func(g *codexTicketGeneration) {
							defer close(g.done)
							defer g.cancel()
							r.collect(g.ctx, owner, cfg, g.proxyURL)
						}(gen)
					} else {
						gen.timer.Reset(remaining)
					}
				} else {
					stopGen()
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (r *CodexTicketRuntime) collect(ctx context.Context, owner string, cfg CodexTicketSettings, proxyURL string) {
	r.mu.Lock()
	pager := r.pager
	probe := r.probe
	r.mu.Unlock()
	if pager == nil || probe == nil || cfg.MaxConcurrency < 1 || cfg.MaxConcurrency > 8 {
		r.setError("collector_unavailable")
		return
	}
	jobs := make(chan CodexTicketKey, 32)
	var workers sync.WaitGroup
	var mu sync.Mutex
	queued := map[CodexTicketKey]bool{}
	for i := 0; i < cfg.MaxConcurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case k, ok := <-jobs:
					if !ok {
						return
					}
					r.mu.Lock()
					r.pending--
					r.inflight++
					r.mu.Unlock()
					if ctx.Err() == nil {
						r.harvest(ctx, owner, cfg, proxyURL, k, probe)
					}
					r.mu.Lock()
					r.inflight--
					r.mu.Unlock()
					mu.Lock()
					delete(queued, k)
					mu.Unlock()
				}
			}
		}()
	}
	defer func() { close(jobs); workers.Wait(); r.mu.Lock(); r.pending = 0; r.mu.Unlock() }()
	var after int64
	for ctx.Err() == nil {
		op, cancel := context.WithTimeout(ctx, 2*time.Second)
		page, err := pager.ListOAuthRefreshCandidatePage(op, OAuthRefreshPageOptions{Platforms: []string{"openai"}, AfterID: after, Limit: 100, ActiveOnly: true, IncludeSetupToken: true})
		cancel()
		if err != nil || page == nil {
			r.setError("candidate_read_failed")
		} else {
			for i := range page.Accounts {
				a := &page.Accounts[i]
				if !a.CodexTurnStateEnabled() || !CodexTicketAccountSupported(a) {
					continue
				}
				scope := CodexTicketIdentityScope(a)
				if scope == "" {
					continue
				}
				for _, model := range cfg.Models {
					if !a.IsSchedulableForModelWithContext(ctx, model) {
						continue
					}
					k := CodexTicketKey{cfg.Revision, a.ID, scope, model}
					mu.Lock()
					exists := queued[k]
					mu.Unlock()
					if exists {
						continue
					}
					op, cancel = context.WithTimeout(ctx, 2*time.Second)
					v, e := r.cache.ReadForRequest(op, a.ID, scope, model)
					retry, re := r.cache.ReadRetry(op, k)
					cancel()
					if e != nil || re != nil {
						continue
					}
					if v.Control.Settings.Revision != cfg.Revision || retry.NextAttemptAt.After(v.ServerTime) {
						continue
					}
					if v.Ticket != nil && ValidateCodexTicket(*v.Ticket, k, cfg, v.ServerTime) == nil {
						r.mu.Lock()
						r.ready[k] = v.Ticket.ExpiresAt
						r.mu.Unlock()
						if v.Ticket.ExpiresAt.Sub(v.ServerTime) > time.Duration(cfg.RefreshBeforeSeconds)*time.Second {
							continue
						}
					}
					mu.Lock()
					queued[k] = true
					mu.Unlock()
					r.mu.Lock()
					r.pending++
					r.mu.Unlock()
					select {
					case jobs <- k:
					case <-ctx.Done():
						return
					}
				}
			}
			if page.HasMore && page.NextAfterID > after {
				after = page.NextAfterID
				continue
			}
			after = 0
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}
func (r *CodexTicketRuntime) harvest(generationCtx context.Context, owner string, cfg CodexTicketSettings, proxyURL string, k CodexTicketKey, probe CodexTicketProbe) {
	ctx, cancel := context.WithTimeout(generationCtx, 30*time.Second)
	defer cancel()
	r.harvestWithTaskContext(generationCtx, ctx, owner, cfg, proxyURL, k, probe)
}

// Keeping both contexts distinguishes an exhausted task deadline from losing
// generation ownership. Tests can supply a short task deadline without timers
// or timeout settings leaking into the public runtime API.
func (r *CodexTicketRuntime) harvestWithTaskContext(generationCtx, ctx context.Context, owner string, cfg CodexTicketSettings, proxyURL string, k CodexTicketKey, probe CodexTicketProbe) {
	a, e := r.accounts.GetByID(ctx, k.AccountID)
	if e != nil || a == nil || !a.CodexTurnStateEnabled() || !CodexTicketAccountSupported(a) || CodexTicketIdentityScope(a) != k.IdentityScope || !a.IsSchedulableForModelWithContext(ctx, k.Model) {
		return
	}
	retry, e := r.cache.ReadRetry(ctx, k)
	if e != nil {
		return
	}
	now, e := r.cache.Now(ctx)
	if e != nil || retry.NextAttemptAt.After(now) {
		return
	}
	blocked, e := r.cache.ProbeCooldownActive(ctx, k)
	if e != nil || blocked {
		return
	}
	ok, e := r.cache.AcquireProbeBudget(ctx, owner, k.Revision, cfg.MaxProbesPerMinute)
	if e != nil || !ok {
		return
	}
	ok, e = r.cache.CheckCurrent(ctx, owner, k.Revision)
	if e != nil || !ok || ctx.Err() != nil {
		return
	}
	blocked, e = r.cache.ProbeCooldownActive(ctx, k)
	if e != nil || blocked || ctx.Err() != nil {
		return
	}
	result, err := probe(ctx, k.AccountID, k.Model, proxyURL)
	if generationCtx.Err() != nil {
		return
	}
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			// Only retry metadata may outlive the expired task, and only while
			// this generation still owns fresh control. Never continue probing.
			retryCtx, cancel := context.WithTimeout(generationCtx, 2*time.Second)
			defer cancel()
			r.recordProbeFailure(retryCtx, owner, k, retry, result, context.DeadlineExceeded)
		}
		return
	}
	now, e = r.cache.Now(ctx)
	if e != nil {
		return
	}
	t := CodexTicket{Key: k, State: result.State, CapturedAt: now, ExpiresAt: now.Add(time.Duration(cfg.TTLSeconds) * time.Second)}
	if err == nil && result.HTTPStatus == http.StatusOK && result.Completed && result.IdentityScope == k.IdentityScope && ValidateCodexTicket(t, k, cfg, now) == nil {
		a, e = r.accounts.GetByID(ctx, k.AccountID)
		if e != nil || a == nil || !a.CodexTurnStateEnabled() || !CodexTicketAccountSupported(a) || CodexTicketIdentityScope(a) != k.IdentityScope {
			return
		}
		ok, e = r.cache.CommitIfCurrent(ctx, owner, t)
		if e == nil && ok {
			r.mu.Lock()
			r.ready[k] = t.ExpiresAt
			r.lastError = ""
			r.mu.Unlock()
		} else {
			r.setError("commit_unavailable")
		}
		return
	}
	r.recordProbeFailure(ctx, owner, k, retry, result, err)
}

func (r *CodexTicketRuntime) recordProbeFailure(ctx context.Context, owner string, k CodexTicketKey, retry CodexTicketRetry, result CodexTicketProbeResult, err error) {
	if ctx.Err() != nil {
		return
	}
	now, e := r.cache.Now(ctx)
	if e != nil || ctx.Err() != nil {
		return
	}
	code := "probe_failed"
	switch {
	case result.HTTPStatus == 401 || result.HTTPStatus == 403:
		code = "authentication_failed"
	case result.HTTPStatus == 429 || result.ErrorCode == "quota_limited":
		code = "rate_limited"
	case result.HTTPStatus >= 500:
		code = "upstream_unavailable"
	case err == nil && !result.Completed:
		code = "stream_failed"
	case err == nil:
		code = "state_mismatch"
	}
	// Adapters can refine classification using a closed set of safe codes.
	// Never surface their raw transport error or an arbitrary ErrorCode value.
	switch result.ErrorCode {
	case "transport_unsupported", "identity_unresolved", "token_unavailable", "stream_failed", "proxy_unavailable":
		code = result.ErrorCode
	}
	if errors.Is(err, context.DeadlineExceeded) {
		code = "probe_timeout"
	}
	if retry.Attempt >= 8 {
		retry.Attempt = 0
	}
	retry.Attempt++
	var random [1]byte
	jitter := 0.5
	if _, e := rand.Read(random[:]); e == nil {
		jitter = float64(random[0]) / 255
	}
	delay := CodexTicketRetryDelay(retry.Attempt, parseCodexRetryAfter(result.RetryAfter, now), jitter)
	authRejected := result.HTTPStatus == 401 || result.HTTPStatus == 403
	quotaRejected := result.ErrorCode == "quota_limited"
	if (authRejected || quotaRejected) && delay < time.Hour {
		delay = time.Hour
	}
	// Store shared restrictions before per-model bookkeeping. Successful probes
	// only clear their ordinary retry key, never this account-wide deadline.
	if authRejected || quotaRejected || result.HTTPStatus == 429 {
		if e := r.cache.ExtendProbeCooldown(ctx, owner, k, now.Add(delay)); e != nil {
			r.setError("retry_unavailable")
			return
		}
	}
	retry.NextAttemptAt = now.Add(delay)
	retry.ErrorCode = code
	if ctx.Err() != nil {
		return
	}
	if e := r.cache.WriteRetry(ctx, owner, k, retry); e != nil {
		if ctx.Err() == nil {
			r.setError("retry_unavailable")
		}
		return
	}
	if ctx.Err() == nil {
		r.setError(code)
	}
}
func CodexTicketRetryDelay(attempt int, retryAfter time.Duration, jitter float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := 30 * time.Second
	for i := 1; i < attempt && d < 30*time.Minute; i++ {
		d *= 2
	}
	if d > 30*time.Minute {
		d = 30 * time.Minute
	}
	if attempt >= 8 {
		d = time.Hour
	}
	if jitter < 0 {
		jitter = 0
	}
	if jitter > 1 {
		jitter = 1
	}
	d += time.Duration(float64(d) * .2 * jitter)
	if retryAfter > d {
		return retryAfter
	}
	return d
}
func parseCodexRetryAfter(s string, now time.Time) time.Duration {
	if seconds, e := strconv.ParseInt(s, 10, 32); e == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if t, e := http.ParseTime(s); e == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}
