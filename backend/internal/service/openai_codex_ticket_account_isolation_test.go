//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type ticketIsolationAccounts struct {
	AccountRepository
	accounts map[int64]*Account
}

func (r *ticketIsolationAccounts) GetByID(_ context.Context, id int64) (*Account, error) {
	a := r.accounts[id]
	if a == nil {
		return nil, ErrAccountNotFound
	}
	copy := *a
	return &copy, nil
}

// Even two local account records for the same upstream identity remain separate.
// A cache implementation returning another account's payload must fail closed.
type ticketIsolationStore struct {
	*ticketTestStore
	tickets  map[int64]*CodexTicket
	misroute bool
}

func (s *ticketIsolationStore) ReadForRequest(_ context.Context, id int64, scope, model string, _ ...string) (CodexTicketRuntimeView, error) {
	v := s.v
	v.Ticket = s.tickets[id]
	if s.misroute {
		v.Ticket = s.tickets[1]
	}
	return v, nil
}

func TestCodexTicketHTTPAccountIsolationAcrossRefreshAndFailover(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%t", passthrough), func(t *testing.T) {
			runtime, one, base, key := ticketRuntimeFixture()
			accountA := *one.a
			accountB := accountA
			accountB.ID = 2
			accounts := &ticketIsolationAccounts{accounts: map[int64]*Account{1: &accountA, 2: &accountB}}
			require.Equal(t, CodexTicketIdentityScope(&accountA), CodexTicketIdentityScope(&accountB))
			runtime.accounts = accounts
			makeTicket := func(id int64, fill string) *CodexTicket {
				k := key
				k.AccountID = id
				return &CodexTicket{Key: k, State: "gAAAAA" + strings.Repeat(fill, base.v.Control.Settings.TargetLength-6), CapturedAt: base.v.ServerTime, ExpiresAt: base.v.ServerTime.Add(time.Hour), Verified: true, VerifiedAt: base.v.ServerTime, ActualModel: k.Model, VerificationModel: k.Model, TargetLength: base.v.Control.Settings.TargetLength}
			}
			store := &ticketIsolationStore{ticketTestStore: base, tickets: map[int64]*CodexTicket{1: makeTicket(1, "a"), 2: makeTicket(2, "b")}}
			runtime.cache = store
			s := &OpenAIGatewayService{accountRepo: accounts, codexTicketRuntime: runtime}
			build := func(account *Account, echo string, origin *Account) (*http.Request, error) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				c.Request.Header.Set("session_id", "isolation-session")
				c.Request.Header.Set("X-Codex-Turn-State", echo)
				if origin != nil {
					s.noteOpenAICodexTurnStateProvenance(c, origin)
				}
				body := []byte(fmt.Sprintf(`{"model":%q,"stream":true,"input":[]}`, key.Model))
				if passthrough {
					return s.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, account, body, "fixture-token")
				}
				return s.buildUpstreamRequest(context.Background(), c, account, body, "fixture-token", true, "", true)
			}
			requestA, err := build(&accountA, "", nil)
			require.NoError(t, err)
			require.Equal(t, store.tickets[1].State, requestA.Header.Get("X-Codex-Turn-State"))
			requestB, err := build(&accountB, store.tickets[1].State, &accountA)
			require.NoError(t, err)
			require.Equal(t, store.tickets[2].State, requestB.Header.Get("X-Codex-Turn-State"))
			require.NotEqual(t, requestA.Header.Get("X-Codex-Turn-State"), requestB.Header.Get("X-Codex-Turn-State"))

			store.v.Control.Settings.MissingPolicy = CodexTicketPassthrough
			store.tickets[1].ExpiresAt = store.v.ServerTime
			requestA, err = build(&accountA, "", &accountA)
			require.NoError(t, err)
			require.Empty(t, requestA.Header.Get("X-Codex-Turn-State"))
			requestB, err = build(&accountB, "", nil)
			require.NoError(t, err)
			require.Equal(t, store.tickets[2].State, requestB.Header.Get("X-Codex-Turn-State"))

			oldB := store.tickets[2].State
			store.tickets[1] = makeTicket(1, "c")
			requestA, err = build(&accountA, "", nil)
			require.NoError(t, err)
			require.Equal(t, store.tickets[1].State, requestA.Header.Get("X-Codex-Turn-State"))
			requestB, err = build(&accountB, "", nil)
			require.NoError(t, err)
			require.Equal(t, oldB, requestB.Header.Get("X-Codex-Turn-State"))

			delete(store.tickets, 2)
			requestB, err = build(&accountB, store.tickets[1].State, &accountA)
			require.NoError(t, err)
			require.Empty(t, requestB.Header.Get("X-Codex-Turn-State"), "missing B ticket must not fall back to A or echo A")
			store.misroute = true
			requestB, err = build(&accountB, "", nil)
			require.NoError(t, err)
			require.Empty(t, requestB.Header.Get("X-Codex-Turn-State"), "account key validation must reject misrouted cache data")
			store.v.Control.Settings.MissingPolicy = CodexTicketReject
			requestB, err = build(&accountB, "", nil)
			require.Nil(t, requestB)
			require.ErrorIs(t, err, ErrCodexTicketMissing)
		})
	}
}
