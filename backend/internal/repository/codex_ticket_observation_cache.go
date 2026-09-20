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
	if k.AccountID <= 0 || k.IdentityScope == "" || k.Model == "" || k.PolicyScope == "" {
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

// Invalidate only the exact ticket used by the completed request. A late
// response must not delete a newer capture or a different account/policy.
func (c *codexTicketCache) InvalidateCodexTicket(ctx context.Context, t service.CodexTicket, reason string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if (reason != "model_mismatch" && reason != "state_312") || t.Key.AccountID <= 0 || t.Key.IdentityScope == "" || t.Key.Model == "" || t.Key.PolicyScope == "" || t.State == "" || t.CapturedAt.IsZero() {
		return false, service.ErrCodexTicketControlUnavailable
	}
	expected, err := json.Marshal(struct {
		Key        service.CodexTicketKey
		State      string
		CapturedAt time.Time
	}{Key: t.Key, State: t.State, CapturedAt: t.CapturedAt})
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	cmd := &codexObservationCommand{redis.NewCmd(ctx, "eval", codexLuaNow+`
local raw=redis.call('GET',KEYS[1]);if not raw then return 0 end
local c=cjson.decode(raw);local expected=cjson.decode(ARGV[1])
if c.settings.enabled~=true or tonumber(c.settings.revision)~=expected.Key.Revision or c.valid_until_ms<=now or not c.owner or redis.call('GET',KEYS[2])~=c.owner then return 0 end
local configured=false;for _,model in ipairs(c.settings.models or {}) do if model==expected.Key.Model then configured=true end end;if not configured then return 0 end
local rawTicket=redis.call('GET',KEYS[3]);if not rawTicket then return 0 end
local payload=cjson.decode(rawTicket);local key=payload.Key;local wanted=expected.Key
if not key or key.Revision~=wanted.Revision or key.AccountID~=wanted.AccountID or key.IdentityScope~=wanted.IdentityScope or key.Model~=wanted.Model or key.PolicyScope~=wanted.PolicyScope or payload.State~=expected.State or payload.CapturedAt~=expected.CapturedAt then return 0 end
redis.call('HINCRBY',KEYS[4],'invalidation_count',1)
redis.call('HSET',KEYS[4],'last_invalidated_at',tostring(now),'last_invalidation_reason',ARGV[2])
if redis.call('PTTL',KEYS[4])<0 then redis.call('PEXPIRE',KEYS[4],ARGV[3]) end
redis.call('DEL',KEYS[3]);return 1`, 4, codexPrefix+"control", codexPrefix+"leader", codexPayloadKey(t.Key), codexObservationKey(t.Key), string(expected), reason, codexObservationTTL.Milliseconds())}
	if err = c.client.WithTimeout(100*time.Millisecond).Process(ctx, cmd); err != nil {
		return false, err
	}
	n, err := cmd.Int64()
	return n == 1, err
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
			if meta.CapturedAt.IsZero() || !meta.ExpiresAt.After(meta.CapturedAt) || meta.VerifiedAt.IsZero() || meta.VerifiedAt.Before(meta.CapturedAt) || meta.VerifiedAt.After(meta.ExpiresAt) || meta.ActualModel != k.Model || meta.VerificationModel != k.Model {
				s.Unavailable = true
			} else {
				s.Metadata = &meta
			}
		}
		decode(cmds[i].retry, &s.Retry)
		raw, e := cmds[i].cooldown.Result()
		if e == nil {
			ms, parseErr := strconv.ParseInt(raw, 10, 64)
			if parseErr != nil || ms <= 0 || ms > 253402300799999 {
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
			if outcome != "" || reason != "" {
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
			}
			if count, exists := obs["invalidation_count"]; exists {
				n, e := strconv.ParseInt(count, 10, 64)
				if e != nil || n <= 0 {
					s.Unavailable = true
				} else {
					s.Observation.InvalidationCount = n
				}
				ms, e := strconv.ParseInt(obs["last_invalidated_at"], 10, 64)
				if e != nil || ms <= 0 || ms > 253402300799999 {
					s.Unavailable = true
				} else {
					stamp := time.UnixMilli(ms).UTC()
					s.Observation.LastInvalidatedAt = &stamp
				}
				why := obs["last_invalidation_reason"]
				if why != "model_mismatch" && why != "state_312" {
					s.Unavailable = true
				} else {
					s.Observation.LastInvalidationReason = &why
				}
			} else if obs["last_invalidated_at"] != "" || obs["last_invalidation_reason"] != "" {
				s.Unavailable = true
			}
			if outcome == "" && s.Observation.InvalidationCount == 0 {
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
				if e != nil || ms <= 0 || ms > 253402300799999 {
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
