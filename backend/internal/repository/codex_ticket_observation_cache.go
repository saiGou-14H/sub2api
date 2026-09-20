package repository

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const codexObservationTTL = 24 * time.Hour

func codexMetadataKey(k service.CodexTicketKey) string    { return codexPayloadKey(k) + ":metadata" }
func codexObservationKey(k service.CodexTicketKey) string { return codexPayloadKey(k) + ":observation" }

// Never replay a possibly applied increment after a lost Redis response.
type codexObservationCommand struct{ *redis.Cmd }

func (*codexObservationCommand) NoRetry() bool { return true }

// One expiring hash per scope, no history list. The count describes this cache window.
func (c *codexTicketCache) RecordCodexTicketDecision(ctx context.Context, k service.CodexTicketKey, outcome, reason, requestID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if k.AccountID <= 0 || k.IdentityScope == "" || k.Model == "" {
		return service.ErrCodexTicketControlUnavailable
	}
	switch outcome {
	case "header_set", "skipped", "rejected":
	default:
		return service.ErrCodexTicketControlUnavailable
	}
	switch reason {
	case "ticket_ready", "ticket_missing", "control_unavailable", "proxy_unavailable", "compact", "identity_changed":
	default:
		return service.ErrCodexTicketControlUnavailable
	}
	if !validCodexRequestID(requestID) {
		requestID = ""
	}
	// Share the pool while bounding socket I/O even if the host Redis client
	// ignores context deadlines. NoRetry prevents ambiguous increments replaying.
	cmd := &codexObservationCommand{redis.NewCmd(ctx, "eval", codexLuaNow+`local key=KEYS[1]
 redis.call('HSET',key,'outcome',ARGV[1],'reason',ARGV[2])
 if ARGV[1]=='header_set' then
 redis.call('HINCRBY',key,'count',1);redis.call('HSET',key,'injected_at',tostring(now),'request_id',ARGV[3])
 end
 if redis.call('PTTL',key)<0 then redis.call('PEXPIRE',key,ARGV[4]) end
 return 1`, 1, codexObservationKey(k), outcome, reason, requestID, codexObservationTTL.Milliseconds())}
	// The timeout clone shares the parent pool; never Close it.
	return c.client.WithTimeout(100*time.Millisecond).Process(ctx, cmd)
}

func (c *codexTicketCache) ReadCodexTicketAccounts(ctx context.Context, keys []service.CodexTicketKey) (map[service.CodexTicketKey]service.CodexTicketAccountSnapshot, error) {
	out := make(map[service.CodexTicketKey]service.CodexTicketAccountSnapshot, len(keys))
	if len(keys) > 1600 {
		return nil, service.ErrCodexTicketControlUnavailable
	}
	type commands struct {
		ticket, meta, retry, cooldown *redis.StringCmd
		observation                   *redis.MapStringStringCmd
	}
	cmds := make([]commands, len(keys))
	pipe := c.client.Pipeline()
	for i, k := range keys {
		cmds[i] = commands{pipe.Get(ctx, codexPayloadKey(k)), pipe.Get(ctx, codexMetadataKey(k)), pipe.Get(ctx, codexRetryKey(k)), pipe.Get(ctx, codexCooldownKey(k)), pipe.HGetAll(ctx, codexObservationKey(k))}
	}
	_, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, service.ErrCodexTicketControlUnavailable
	}
	for i, k := range keys {
		var s service.CodexTicketAccountSnapshot
		decode := func(cmd *redis.StringCmd, dst any) bool {
			raw, e := cmd.Result()
			if e == redis.Nil {
				return false
			}
			if e != nil || json.Unmarshal([]byte(raw), dst) != nil || raw == "null" {
				s.Unavailable = true
				return false
			}
			return true
		}
		var ticket service.CodexTicket
		if decode(cmds[i].ticket, &ticket) {
			if ticket.Key != k {
				s.Unavailable = true
			} else {
				s.Ticket = &ticket
			}
		}
		var meta service.CodexTicketMetadata
		if decode(cmds[i].meta, &meta) {
			if meta.CapturedAt.IsZero() || !meta.ExpiresAt.After(meta.CapturedAt) {
				s.Unavailable = true
			} else {
				s.Metadata = &meta
			}
		}
		decode(cmds[i].retry, &s.Retry)
		raw, e := cmds[i].cooldown.Result()
		if e == nil {
			ms, parseErr := strconv.ParseInt(raw, 10, 64)
			if parseErr != nil || ms <= 0 {
				s.Unavailable = true
			} else {
				s.CooldownUntil = time.UnixMilli(ms).UTC()
			}
		} else if e != redis.Nil {
			s.Unavailable = true
		}
		obs, e := cmds[i].observation.Result()
		if e != nil {
			s.Unavailable = true
		} else if len(obs) > 0 {
			outcome, reason := obs["outcome"], obs["reason"]
			switch outcome {
			case "header_set", "skipped", "rejected":
				s.Observation.LastOutcome = &outcome
			default:
				s.Unavailable = true
			}
			switch reason {
			case "ticket_ready", "ticket_missing", "control_unavailable", "proxy_unavailable", "compact", "identity_changed":
				s.Observation.LastReason = &reason
			default:
				s.Unavailable = true
			}
			if count, exists := obs["count"]; exists {
				n, e := strconv.ParseInt(count, 10, 64)
				if e != nil || n < 0 {
					s.Unavailable = true
				} else {
					s.Observation.InjectionCount = n
				}
			}
			if stamp, exists := obs["injected_at"]; exists {
				ms, e := strconv.ParseInt(stamp, 10, 64)
				if e != nil || ms <= 0 {
					s.Unavailable = true
				} else {
					t := time.UnixMilli(ms).UTC()
					s.Observation.LastInjectedAt = &t
				}
			}
			if id := obs["request_id"]; id != "" {
				if validCodexRequestID(id) {
					s.Observation.LastRequestID = &id
				} else {
					s.Unavailable = true
				}
			}
		}
		out[k] = s
	}
	return out, nil
}
func validCodexRequestID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
