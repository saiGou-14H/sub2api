package admin

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCodexTicketAccountIDsValidation(t *testing.T) {
	ids, err := parseCodexTicketAccountIDs("2,1,2")
	require.NoError(t, err)
	require.Equal(t, []int64{2, 1}, ids)
	for _, raw := range []string{"", "0", "-1", "+1", " 1", "1,", "1,,2", "1.0", "9223372036854775808", strings.Repeat("1,", 100) + "1"} {
		_, err := parseCodexTicketAccountIDs(raw)
		require.Error(t, err, raw)
	}
	_, err = parseCodexTicketAccountIDs(strings.Repeat("1,", 99) + "1")
	require.NoError(t, err)
}
func TestCodexTicketAccountsHandlerBoundsAndSanitizes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewCodexTicketHandler(nil, nil)
	calls := 0
	h.SetAccountsProvider(func(_ context.Context, ids []int64) (service.CodexTicketAccountsStatus, error) {
		calls++
		require.Equal(t, []int64{1, 2}, ids)
		return service.CodexTicketAccountsStatus{CounterScope: "shared_cache_window", Accounts: []service.CodexTicketAccountStatus{}}, nil
	})
	r := gin.New()
	r.GET("/accounts", h.Accounts)
	for _, query := range []string{"", "?account_ids=0", "?account_ids=1&account_ids=2", "?account_ids=1%20", "?account_ids=" + strings.Repeat("1,", 100) + "1"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/accounts"+query, nil))
		require.Equal(t, 400, w.Code)
	}
	require.Zero(t, calls)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/accounts?account_ids=1,2,1", nil))
	require.Equal(t, 200, w.Code)
	require.Equal(t, 1, calls)
	require.Contains(t, w.Body.String(), `"applied_revision":null`)
	h.SetAccountsProvider(func(context.Context, []int64) (service.CodexTicketAccountsStatus, error) {
		return service.CodexTicketAccountsStatus{}, errors.New("token secret")
	})
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/accounts?account_ids=1", nil))
	require.Equal(t, 503, w.Code)
	require.NotContains(t, w.Body.String(), "token secret")
}
