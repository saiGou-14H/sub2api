package service

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCodexTicketCookieScopeAndLifetime(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	u, err := url.Parse(chatgptCodexURL)
	require.NoError(t, err)
	for _, tc := range []struct {
		name, attrs string
		ok          bool
		life        time.Duration
		path        string
	}{
		{"root domain", "; Domain=.chatgpt.com; Path=/; Max-Age=240; Secure; HttpOnly", true, 240 * time.Second, "/"},
		{"host session default directory", "", true, 240 * time.Second, "/backend-api/codex"},
		{"short max age", "; Max-Age=45; Path=/backend-api/codex", true, 45 * time.Second, "/backend-api/codex"},
		{"exact path", "; Path=/backend-api/codex/responses", true, 240 * time.Second, "/backend-api/codex/responses"},
		{"trailing slash directory", "; Path=/backend-api/codex/", true, 240 * time.Second, "/backend-api/codex/"},
		{"case insensitive domain", "; Domain=CHATGPT.COM; Path=/", true, 240 * time.Second, "/"},
		{"short expires", "; Expires=" + now.Add(70*time.Second).Format(http.TimeFormat), true, 70 * time.Second, "/backend-api/codex"},
		{"max age precedence", "; Max-Age=80; Expires=" + now.Add(-time.Hour).Format(http.TimeFormat), true, 80 * time.Second, "/backend-api/codex"},
		{"large max age capped", "; Max-Age=9223372036854775807", true, 240 * time.Second, "/backend-api/codex"},
		{"other host", "; Domain=example.com", false, 0, ""},
		{"parent public suffix", "; Domain=com", false, 0, ""},
		{"suffix trick", "; Domain=chatgpt.com.evil.example", false, 0, ""},
		{"subdomain not parent", "; Domain=api.chatgpt.com", false, 0, ""},
		{"other path", "; Path=/other", false, 0, ""},
		{"path prefix without boundary", "; Path=/backend-api/code", false, 0, ""},
		{"deleted", "; Max-Age=0", false, 0, ""},
		{"negative age", "; Max-Age=-10", false, 0, ""},
		{"expired", "; Expires=" + now.Add(-time.Second).Format(http.TimeFormat), false, 0, ""},
		{"invalid age", "; Max-Age=invalid", false, 0, ""},
		{"invalid expires", "; Expires=invalid", false, 0, ""},
		{"duplicate age", "; Max-Age=20; Max-Age=240", false, 0, ""},
		{"partitioned", "; Secure; Partitioned", false, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{"Set-Cookie": {"cflb=c-secret" + tc.attrs, "oailb=o-secret" + tc.attrs, "login=not-for-cache; Path=/"}}}
			cookies := CodexTicketCookiesFromResponse(resp, u, now)
			if !tc.ok {
				require.Empty(t, cookies)
				return
			}
			require.Len(t, cookies, 2)
			require.True(t, CodexTicketCookiesValid(cookies, now))
			require.Equal(t, now.Add(tc.life), CodexTicketCookieExpiry(cookies))
			require.Equal(t, tc.path, cookies[0].Path)
			require.Equal(t, "chatgpt.com", cookies[0].Domain)
			require.Equal(t, "cflb=c-secret; oailb=o-secret", CodexTicketCookieHeader(cookies, now))
			require.Empty(t, CodexTicketCookieHeader(cookies, now.Add(tc.life)), "expiry is exclusive")
		})
	}
}

func TestCodexTicketCookiesRejectIncompleteAmbiguousAndInvalidPairs(t *testing.T) {
	now := time.Now()
	u, _ := url.Parse(chatgptCodexURL)
	for _, headers := range [][]string{
		nil, {"cflb=x"}, {"oailb=y"}, {"CFLB=x", "oailb=y"},
		{"cflb=x", "oailb=y", "cflb=z"}, {"cflb=x", "oailb=y", "oailb=; Max-Age=0"},
		{"cflb=bad\r\ninjected", "oailb=y"}, {"cflb=bad\x00value", "oailb=y"},
		{"cflb=" + strings.Repeat("x", codexTicketMaxCookieBytes), "oailb=y"},
	} {
		r := &http.Response{Header: http.Header{"Set-Cookie": headers}}
		require.Empty(t, CodexTicketCookiesFromResponse(r, u, now))
	}
	r := &http.Response{Header: http.Header{"Set-Cookie": {"cflb=x", "oailb=y"}}}
	for _, target := range []string{"http://chatgpt.com/backend-api/codex/responses", "https://example.com/backend-api/codex/responses", "https://chatgpt.com:444/backend-api/codex/responses", "https://chatgpt.com/backend-api/codex/responses/compact"} {
		other, _ := url.Parse(target)
		require.Empty(t, CodexTicketCookiesFromResponse(r, other, now))
	}
	require.Empty(t, CodexTicketCookiesFromResponse(r, nil, now))
	good := CodexTicketCookiesFromResponse(r, u, now)
	for _, mutate := range []func([]CodexTicketCookie){
		func(c []CodexTicketCookie) { c[1].Name = "cflb" },
		func(c []CodexTicketCookie) { c[0].Value = "secret; injected=value" },
		func(c []CodexTicketCookie) { c[0].Value = "secret\r\n" },
		func(c []CodexTicketCookie) { c[0].Domain = "example.com" },
		func(c []CodexTicketCookie) { c[0].Domain = "" },
		func(c []CodexTicketCookie) { c[0].Path = "/wrong" },
		func(c []CodexTicketCookie) { c[0].ExpiresAt = now },
	} {
		bad := append([]CodexTicketCookie(nil), good...)
		mutate(bad)
		require.False(t, CodexTicketCookiesValid(bad, now))
		require.Empty(t, CodexTicketCookieHeader(bad, now), "never inject half a bundle")
	}
}

func TestCodexTicketRefreshUsesEarliestExpiryAndPreservesEarlyRefresh(t *testing.T) {
	cfg := DefaultCodexTicketSettings()
	now := time.Now()
	for _, tc := range []struct{ life, refresh time.Duration }{
		{240 * time.Second, 30 * time.Second}, {90 * time.Second, 0}, {10 * time.Second, 0},
	} {
		require.Equal(t, now.Add(tc.refresh), codexTicketRefreshAt(now, now.Add(tc.life), cfg))
	}
}
