//go:build unit

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCodexTicketInvalidationFreshControlAndClosedReasons(t *testing.T) {
	for _, kind := range []string{"disabled", "revision", "model_removed", "proxy_inactive", "proxy_expired", "control_expired", "leader", "reason"} {
		t.Run(kind, func(t *testing.T) {
			c, mr, control, ticket := ticketCacheFixture(t)
			ctx := context.Background()
			ok, err := c.CommitIfCurrent(ctx, "one", ticket)
			require.NoError(t, err)
			require.True(t, ok)
			reason := "state_312"
			switch kind {
			case "disabled":
				control.Settings.Enabled = false
			case "revision":
				control.Settings.Revision++
			case "model_removed":
				control.Settings.Models = []string{"other-model"}
			case "proxy_inactive":
				control.ProxyState = "inactive"
			case "proxy_expired":
				control.ProxyExpiresAtMS = ticket.CapturedAt.Add(-time.Second).UnixMilli()
			case "reason":
				reason = "untrusted response body"
			}
			ok, err = c.PublishControl(ctx, control)
			require.NoError(t, err)
			require.True(t, ok)
			if kind == "control_expired" {
				mr.SetTime(ticket.CapturedAt.Add(7 * time.Second))
			}
			if kind == "leader" {
				require.NoError(t, c.client.Set(ctx, codexPrefix+"leader", "other", time.Minute).Err())
			}
			ok, err = c.InvalidateCodexTicket(ctx, ticket, reason)
			if kind == "reason" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if kind == "proxy_inactive" || kind == "proxy_expired" {
				require.True(t, ok, "harvest proxy availability must not prevent invalidating a matched bad ticket")
				require.False(t, mr.Exists(codexPayloadKey(ticket.Key)))
				require.Equal(t, "1", mr.HGet(codexObservationKey(ticket.Key), "invalidation_count"))
			} else {
				require.False(t, ok)
				require.True(t, mr.Exists(codexPayloadKey(ticket.Key)))
				require.False(t, mr.Exists(codexObservationKey(ticket.Key)))
			}
		})
	}
}

func TestCodexTicketInvalidationMetadataAndObservationCorruption(t *testing.T) {
	for _, kind := range []string{"negative_count", "invalid_count", "missing_reason", "bad_reason", "bad_timestamp", "missing_count", "metadata_model", "metadata_unverified"} {
		t.Run(kind, func(t *testing.T) {
			c, _, _, ticket := ticketCacheFixture(t)
			ctx := context.Background()
			ok, err := c.CommitIfCurrent(ctx, "one", ticket)
			require.NoError(t, err)
			require.True(t, ok)
			fields := map[string]any{"invalidation_count": "1", "last_invalidated_at": ticket.CapturedAt.UnixMilli(), "last_invalidation_reason": "state_312"}
			switch kind {
			case "negative_count":
				fields["invalidation_count"] = "-1"
			case "invalid_count":
				fields["invalidation_count"] = "nan"
			case "missing_reason":
				delete(fields, "last_invalidation_reason")
			case "bad_reason":
				fields["last_invalidation_reason"] = "raw upstream error secret"
			case "bad_timestamp":
				fields["last_invalidated_at"] = "bad"
			case "missing_count":
				delete(fields, "invalidation_count")
			case "metadata_model", "metadata_unverified":
				meta := service.CodexTicketMetadata{CapturedAt: ticket.CapturedAt, ExpiresAt: ticket.ExpiresAt, VerifiedAt: ticket.VerifiedAt, ActualModel: ticket.ActualModel, VerificationModel: ticket.VerificationModel}
				if kind == "metadata_model" {
					meta.VerificationModel = "wrong"
				} else {
					meta.VerifiedAt = time.Time{}
				}
				raw, err := json.Marshal(meta)
				require.NoError(t, err)
				require.NoError(t, c.client.Set(ctx, codexMetadataKey(ticket.Key), raw, time.Hour).Err())
			}
			require.NoError(t, c.client.HSet(ctx, codexObservationKey(ticket.Key), fields).Err())
			rows, err := c.ReadCodexTicketAccounts(ctx, []service.CodexTicketKey{ticket.Key})
			require.NoError(t, err)
			require.True(t, rows[ticket.Key].Unavailable)
		})
	}
}

func TestCodexTicketInvalidationCounterFailureKeepsTicket(t *testing.T) {
	c, mr, _, ticket := ticketCacheFixture(t)
	ctx := context.Background()
	ok, err := c.CommitIfCurrent(ctx, "one", ticket)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, c.client.HSet(ctx, codexObservationKey(ticket.Key), "invalidation_count", "corrupt").Err())
	ok, err = c.InvalidateCodexTicket(ctx, ticket, "state_312")
	require.Error(t, err)
	require.False(t, ok)
	require.True(t, mr.Exists(codexPayloadKey(ticket.Key)), "do not delete before increment is known to succeed")
}
