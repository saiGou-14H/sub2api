package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var ErrCodexTicketMissing = errors.New("codex ticket unavailable")
var ErrCodexTicketControlUnavailable = errors.New("codex ticket control unavailable")

type CodexTicketKey struct {
	Revision             uint64
	AccountID            int64
	IdentityScope, Model string
	PolicyScope          string
}
type CodexTicket struct {
	Key                            CodexTicketKey
	State                          string
	CapturedAt, ExpiresAt          time.Time
	Verified                       bool
	VerifiedAt                     time.Time
	ActualModel, VerificationModel string
	TargetLength                   int
}
type CodexTicketControl struct {
	Owner            string              `json:"owner"`
	Settings         CodexTicketSettings `json:"settings"`
	ProxyState       string              `json:"proxy_state"`
	ProxyExpiresAtMS int64               `json:"proxy_expires_at_ms"`
	ObservedAtMS     int64               `json:"observed_at_ms"`
	ValidUntilMS     int64               `json:"valid_until_ms"`
}
type CodexTicketRuntimeView struct {
	Control    CodexTicketControl
	Ticket     *CodexTicket
	ServerTime time.Time
}
type CodexTicketRetry struct {
	Attempt       int
	NextAttemptAt time.Time
	ErrorCode     string
}

// Ticket/retry mutations are fenced against owner, revision and fresh control.
// Observed probe cooldowns are monotonic account-identity restrictions and must
// survive generation changes. No raw payload is exposed by Status.
type CodexTicketRuntimeStore interface {
	Now(context.Context) (time.Time, error)
	TryAcquireLeader(context.Context, string, time.Duration) (bool, error)
	RenewLeader(context.Context, string, time.Duration) (bool, error)
	ReleaseLeader(context.Context, string) error
	PublishControl(context.Context, CodexTicketControl) (bool, error)
	ReadForRequest(context.Context, int64, string, string, ...string) (CodexTicketRuntimeView, error)
	CheckCurrent(context.Context, string, uint64) (bool, error)
	CommitIfCurrent(context.Context, string, CodexTicket) (bool, error)
	AcquireProbeBudget(context.Context, string, uint64, int) (bool, error)
	ReadRetry(context.Context, CodexTicketKey) (CodexTicketRetry, error)
	WriteRetry(context.Context, string, CodexTicketKey, CodexTicketRetry) error
	ProbeCooldownActive(context.Context, CodexTicketKey) (bool, error)
	ExtendProbeCooldown(context.Context, string, CodexTicketKey, time.Time) error
}
type CodexTicketProbeResult struct {
	State, IdentityScope           string
	HTTPStatus                     int
	RetryAfter                     string
	Completed                      bool
	ErrorCode                      string
	PolicyScope                    string
	Verified                       bool
	VerifiedAt                     time.Time
	ActualModel, VerificationModel string
}
type CodexTicketProbe func(context.Context, int64, string, string) (CodexTicketProbeResult, error)
type CodexTicketRuntimeStatus struct {
	DesiredRevision     string   `json:"desired_revision"`
	AppliedRevision     *string  `json:"applied_revision"`
	Phase               string   `json:"phase"`
	ProxyState          *string  `json:"proxy_state"`
	ReadyCount          int      `json:"ready_count"`
	PendingCount        int      `json:"pending_count"`
	InflightCount       int      `json:"inflight_count"`
	LastErrorCode       *string  `json:"last_error_code"`
	CounterScope        string   `json:"counter_scope"`
	SupportedTransports []string `json:"supported_transports"`
}

func CodexTicketModelHash(s string) string {
	v := sha256.Sum256([]byte(s))
	return hex.EncodeToString(v[:])
}
func ValidateCodexTicket(t CodexTicket, k CodexTicketKey, c CodexTicketSettings, now time.Time) error {
	if t.Key != k || k.Revision != c.Revision || k.AccountID <= 0 || k.IdentityScope == "" || k.Model == "" || k.PolicyScope == "" {
		return errors.New("ticket scope mismatch")
	}
	if !t.Verified || t.VerifiedAt.IsZero() || t.VerifiedAt.Before(t.CapturedAt) || t.VerifiedAt.After(now.Add(5*time.Second)) || t.VerifiedAt.After(t.ExpiresAt) || t.ActualModel != k.Model || t.VerificationModel != k.Model || t.TargetLength != c.TargetLength {
		return errors.New("ticket verification invalid")
	}
	if len(t.State) != c.TargetLength || !strings.HasPrefix(t.State, "gAAAAA") {
		return errors.New("ticket format mismatch")
	}
	for _, ch := range t.State {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '=') {
			return errors.New("ticket format mismatch")
		}
	}
	if t.CapturedAt.IsZero() || t.CapturedAt.After(now.Add(5*time.Second)) || !t.ExpiresAt.After(now) || !t.ExpiresAt.After(t.CapturedAt) || t.ExpiresAt.Sub(t.CapturedAt) > time.Duration(c.TTLSeconds)*time.Second {
		return errors.New("ticket time bounds invalid")
	}
	return nil
}
func codexTicketModelEnabled(c CodexTicketSettings, m string) bool {
	for _, x := range c.Models {
		if x == m {
			return true
		}
	}
	return false
}
