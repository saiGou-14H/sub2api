package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const codexPrefix = "s2a:{codex-ticket}:"

type codexTicketCache struct{ client *redis.Client }

func NewCodexTicketCache(c *redis.Client) service.CodexTicketRuntimeStore {
	return &codexTicketCache{c}
}
func (c *codexTicketCache) Now(ctx context.Context) (time.Time, error) {
	return c.client.Time(ctx).Result()
}
func (c *codexTicketCache) TryAcquireLeader(ctx context.Context, o string, d time.Duration) (bool, error) {
	return c.client.SetNX(ctx, codexPrefix+"leader", o, d).Result()
}
func (c *codexTicketCache) RenewLeader(ctx context.Context, o string, d time.Duration) (bool, error) {
	n, e := c.client.Eval(ctx, `if redis.call('GET',KEYS[1])~=ARGV[1] then return 0 end return redis.call('PEXPIRE',KEYS[1],ARGV[2])`, []string{codexPrefix + "leader"}, o, d.Milliseconds()).Int()
	return n == 1, e
}
func (c *codexTicketCache) ReleaseLeader(ctx context.Context, o string) error {
	return c.client.Eval(ctx, `if redis.call('GET',KEYS[1])==ARGV[1] then redis.call('DEL',KEYS[1]); local raw=redis.call('GET',KEYS[2]); if raw and cjson.decode(raw).owner==ARGV[1] then redis.call('DEL',KEYS[2]) end end return 1`, []string{codexPrefix + "leader", codexPrefix + "control"}, o).Err()
}

const codexLuaNow = `local t=redis.call('TIME'); local now=tonumber(t[1])*1000+math.floor(tonumber(t[2])/1000); `
const codexLuaFence = codexLuaNow + `if redis.call('GET',KEYS[1])~=ARGV[1] then return 0 end
local raw=redis.call('GET',KEYS[2]); if not raw then return 0 end
local c=cjson.decode(raw); if c.owner~=ARGV[1] or c.settings.revision~=ARGV[2] or c.settings.enabled~=true or c.proxy_state~='active' or c.valid_until_ms<=now or (c.proxy_expires_at_ms>0 and c.proxy_expires_at_ms<=now) then return 0 end
`

func (c *codexTicketCache) PublishControl(ctx context.Context, v service.CodexTicketControl) (bool, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return false, e
	}
	n, e := c.client.Eval(ctx, codexLuaNow+`if redis.call('GET',KEYS[1])~=ARGV[1] then return 0 end
local v=cjson.decode(ARGV[2]); if v.valid_until_ms<=now then return 0 end
local old=redis.call('GET',KEYS[2]); if old and tonumber(cjson.decode(old).settings.revision)>tonumber(v.settings.revision) then return 0 end
redis.call('SET',KEYS[2],ARGV[2],'PX',v.valid_until_ms-now);return 1`, []string{codexPrefix + "leader", codexPrefix + "control"}, v.Owner, string(b)).Int()
	return n == 1, e
}
func codexPayloadKey(k service.CodexTicketKey) string {
	return fmt.Sprintf("%sticket:%d:%d:%s:%s:%s", codexPrefix, k.Revision, k.AccountID, service.CodexTicketModelHash(k.IdentityScope), service.CodexTicketModelHash(k.Model), service.CodexTicketModelHash(k.PolicyScope))
}
func codexRetryKey(k service.CodexTicketKey) string { return codexPayloadKey(k) + ":retry" }

// Rejections survive model, configuration and proxy changes, but not identity changes.
func codexCooldownKey(k service.CodexTicketKey) string {
	return fmt.Sprintf("%scooldown:%d:%s", codexPrefix, k.AccountID, service.CodexTicketModelHash(k.IdentityScope))
}
func (c *codexTicketCache) ProbeCooldownActive(ctx context.Context, k service.CodexTicketKey) (bool, error) {
	n, err := c.client.Eval(ctx, codexLuaNow+`local deadline=tonumber(redis.call('GET',KEYS[1]) or '0'); if deadline>now then return 1 end return 0`, []string{codexCooldownKey(k)}).Int()
	return n == 1, err
}
func (c *codexTicketCache) ExtendProbeCooldown(ctx context.Context, _ string, k service.CodexTicketKey, until time.Time) error {
	// Observed rejections outlive the generation that made the request. This
	// monotonic restriction must not be fenced like a usable ticket.
	return c.client.Eval(ctx, codexLuaNow+`local deadline=tonumber(ARGV[1]); local old=tonumber(redis.call('GET',KEYS[1]) or '0');
if deadline>now and deadline>old then redis.call('SET',KEYS[1],ARGV[1],'PX',deadline-now) end return 1`, []string{codexCooldownKey(k)}, until.UnixMilli()).Err()
}
func (c *codexTicketCache) ReadForRequest(ctx context.Context, id int64, scope, model string, policyScopes ...string) (service.CodexTicketRuntimeView, error) {
	policy := ""
	if len(policyScopes) > 0 {
		policy = policyScopes[0]
	}
	started := time.Now()
	var v service.CodexTicketRuntimeView
	a, e := c.client.Eval(ctx, codexLuaNow+`local raw=redis.call('GET',KEYS[1]); if not raw then return {} end
local c=cjson.decode(raw);if c.valid_until_ms<=now then return {} end
local key=ARGV[1]..c.settings.revision..':'..ARGV[2]..':'..ARGV[3]..':'..ARGV[4]..':'..ARGV[5]
return {raw,redis.call('GET',key) or '',tostring(now)}`, []string{codexPrefix + "control"}, codexPrefix+"ticket:", id, service.CodexTicketModelHash(scope), service.CodexTicketModelHash(model), service.CodexTicketModelHash(policy)).Slice()
	if e != nil {
		return v, e
	}
	if len(a) != 3 {
		return v, service.ErrCodexTicketControlUnavailable
	}
	if e = json.Unmarshal([]byte(a[0].(string)), &v.Control); e != nil {
		return v, service.ErrCodexTicketControlUnavailable
	}
	var ms int64
	if _, e = fmt.Sscan(a[2].(string), &ms); e != nil {
		return v, e
	}
	// Conservatively include the full round trip so a delayed Redis response
	// cannot revive a control or ticket that expired while in transit.
	v.ServerTime = time.UnixMilli(ms).Add(time.Since(started))
	if v.Control.ValidUntilMS <= v.ServerTime.UnixMilli() {
		return v, service.ErrCodexTicketControlUnavailable
	}
	if a[1].(string) != "" {
		var t service.CodexTicket
		if json.Unmarshal([]byte(a[1].(string)), &t) == nil {
			v.Ticket = &t
		}
	}
	return v, nil
}
func (c *codexTicketCache) CheckCurrent(ctx context.Context, o string, r uint64) (bool, error) {
	n, e := c.client.Eval(ctx, codexLuaFence+`return 1`, []string{codexPrefix + "leader", codexPrefix + "control"}, o, fmt.Sprint(r)).Int()
	return n == 1, e
}
func (c *codexTicketCache) CommitIfCurrent(ctx context.Context, o string, t service.CodexTicket) (bool, error) {
	view, e := c.ReadForRequest(ctx, t.Key.AccountID, t.Key.IdentityScope, t.Key.Model, t.Key.PolicyScope)
	if e != nil {
		return false, e
	}
	cfg := view.Control.Settings
	cfg.TargetLength = t.TargetLength
	if t.TargetLength < 64 || t.TargetLength > 4096 {
		return false, service.ErrCodexTicketControlUnavailable
	}
	if e = service.ValidateCodexTicket(t, t.Key, cfg, view.ServerTime); e != nil {
		return false, e
	}
	b, e := json.Marshal(t)
	if e != nil {
		return false, e
	}
	metadata, e := json.Marshal(service.CodexTicketMetadata{CapturedAt: t.CapturedAt, ExpiresAt: t.ExpiresAt, VerifiedAt: t.VerifiedAt, ActualModel: t.ActualModel, VerificationModel: t.VerificationModel})
	if e != nil {
		return false, e
	}
	n, e := c.client.Eval(ctx, codexLuaFence+`local expiry=tonumber(ARGV[4]);if expiry<=now then return 0 end
local payload=cjson.decode(ARGV[3]);local target=tonumber(payload.TargetLength)
if not target or target<64 or target>4096 or string.len(payload.State)~=target or payload.Verified~=true or not payload.Key.PolicyScope or payload.Key.PolicyScope=='' or payload.ActualModel~=payload.Key.Model or payload.VerificationModel~=payload.Key.Model or not payload.VerifiedAt then return 0 end
local configured=false;for _,model in ipairs(c.settings.models) do if model==payload.Key.Model then configured=true end end;if not configured then return 0 end
redis.call('SET',KEYS[3],ARGV[3],'PX',expiry-now);redis.call('DEL',KEYS[4]);redis.call('SET',KEYS[5],ARGV[5],'PX',ARGV[6]);return 1`, []string{codexPrefix + "leader", codexPrefix + "control", codexPayloadKey(t.Key), codexRetryKey(t.Key), codexMetadataKey(t.Key)}, o, fmt.Sprint(t.Key.Revision), string(b), t.ExpiresAt.UnixMilli(), string(metadata), codexObservationTTL.Milliseconds()).Int()
	return n == 1, e
}
func (c *codexTicketCache) AcquireProbeBudget(ctx context.Context, o string, r uint64, limit int) (bool, error) {
	n, e := c.client.Eval(ctx, codexLuaFence+`local key=ARGV[3]..math.floor(now/60000);local count=tonumber(redis.call('GET',key) or '0');if count>=tonumber(ARGV[4]) then return 0 end
redis.call('INCR',key);redis.call('PEXPIRE',key,120000);return 1`, []string{codexPrefix + "leader", codexPrefix + "control"}, o, fmt.Sprint(r), codexPrefix+"budget:", limit).Int()
	return n == 1, e
}
func (c *codexTicketCache) ReadRetry(ctx context.Context, k service.CodexTicketKey) (service.CodexTicketRetry, error) {
	var r service.CodexTicketRetry
	b, e := c.client.Get(ctx, codexRetryKey(k)).Bytes()
	if e == redis.Nil {
		return r, nil
	}
	if e != nil {
		return r, e
	}
	e = json.Unmarshal(b, &r)
	return r, e
}
func (c *codexTicketCache) WriteRetry(ctx context.Context, o string, k service.CodexTicketKey, r service.CodexTicketRetry) error {
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	return c.client.Eval(ctx, codexLuaFence+`local ttl=tonumber(ARGV[4])-now;if ttl>0 then redis.call('SET',KEYS[3],ARGV[3],'PX',ttl) end return 1`, []string{codexPrefix + "leader", codexPrefix + "control", codexRetryKey(k)}, o, fmt.Sprint(k.Revision), string(b), r.NextAttemptAt.Add(time.Hour).UnixMilli()).Err()
}
