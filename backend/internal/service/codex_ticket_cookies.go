package service

import (
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	codexTicketMaxCookieBytes = 16 << 10
	codexTicketBundleTTL      = 240 * time.Second
)

// CodexTicketCookie is private cache data. Public status DTOs never contain it.
// Domain is canonicalized even for host-only cookies, so an absent Domain never
// becomes permission to replay on an arbitrary host after persistence.
type CodexTicketCookie struct {
	Name      string    `json:"name"`
	Value     string    `json:"value"`
	Domain    string    `json:"domain"`
	Path      string    `json:"path"`
	ExpiresAt time.Time `json:"expires_at"`
	HostOnly  bool      `json:"host_only,omitempty"`
	Secure    bool      `json:"secure,omitempty"`
	HttpOnly  bool      `json:"http_only,omitempty"`
}

func codexTicketCookieName(name string) bool { return name == "cflb" || name == "oailb" }

// Cookie replay is confined to the Codex Responses endpoint used for capture
// and verification. Redirects on the capture transport are disabled separately.
func codexTicketCookieTarget(u *url.URL) bool {
	return u != nil && u.Scheme == "https" && strings.EqualFold(u.Hostname(), "chatgpt.com") &&
		(u.Port() == "" || u.Port() == "443") && u.User == nil && u.Opaque == "" &&
		u.EscapedPath() == "/backend-api/codex/responses"
}

func codexTicketCookiePathMatches(cookiePath, requestPath string) bool {
	return cookiePath != "" && strings.HasPrefix(cookiePath, "/") &&
		(requestPath == cookiePath || (strings.HasPrefix(requestPath, cookiePath) &&
			(strings.HasSuffix(cookiePath, "/") || strings.HasPrefix(requestPath[len(cookiePath):], "/"))))
}

// CodexTicketCookiesFromResponse returns a complete pair from this response
// only. No jar, prior response, caller cookie, or verification response can fill
// a missing member. Ambiguous, invalid, deleted, or expired members reject the
// pair. Unrelated cookies are ignored and never stored.
func CodexTicketCookiesFromResponse(resp *http.Response, requestURL *url.URL, now time.Time) []CodexTicketCookie {
	if resp == nil || !codexTicketCookieTarget(requestURL) || now.IsZero() {
		return nil
	}
	byName := make(map[string]CodexTicketCookie, 2)
	for _, raw := range resp.Header.Values("Set-Cookie") {
		name, _, _ := strings.Cut(raw, "=")
		name = strings.TrimSpace(name)
		if !codexTicketCookieName(name) {
			continue
		}
		if _, duplicate := byName[name]; duplicate {
			return nil
		}
		c, err := http.ParseSetCookie(raw)
		if err != nil || c.Valid() != nil || c.Value == "" || c.MaxAge < 0 || c.Partitioned {
			return nil
		}
		// Invalid lifetime/scope attributes must not degrade into session cookies
		// or host-only/root defaults. Unknown extensions have no replay effect.
		seenAttrs := make(map[string]bool)
		for _, attr := range strings.Split(raw, ";")[1:] {
			key, _, _ := strings.Cut(strings.TrimSpace(attr), "=")
			key = strings.ToLower(key)
			switch key {
			case "max-age", "expires", "domain", "path":
				if seenAttrs[key] {
					return nil
				}
				seenAttrs[key] = true
			}
		}
		for _, attr := range c.Unparsed {
			key, _, _ := strings.Cut(strings.TrimSpace(attr), "=")
			switch strings.ToLower(key) {
			case "max-age", "expires", "domain", "path":
				return nil
			}
		}
		domain := strings.ToLower(strings.TrimPrefix(c.Domain, "."))
		hostOnly := c.Domain == ""
		if hostOnly {
			domain = strings.ToLower(requestURL.Hostname())
		}
		if domain != "chatgpt.com" {
			return nil
		}
		path := c.Path
		if path == "" || !strings.HasPrefix(path, "/") {
			// RFC 6265 default-path: the request directory, not automatically '/'.
			path = requestURL.Path[:strings.LastIndex(requestURL.Path, "/")]
		}
		if !codexTicketCookiePathMatches(path, requestURL.Path) {
			return nil
		}
		expiry := now.Add(codexTicketBundleTTL)
		if c.MaxAge > 0 {
			// Max-Age takes precedence over Expires. Compare before converting to
			// duration so a huge value cannot overflow and extend the local cap.
			if c.MaxAge < int(codexTicketBundleTTL/time.Second) {
				expiry = now.Add(time.Duration(c.MaxAge) * time.Second)
			}
		} else if !c.Expires.IsZero() && c.Expires.Before(expiry) {
			expiry = c.Expires
		}
		byName[c.Name] = CodexTicketCookie{Name: c.Name, Value: c.Value, Domain: domain,
			Path: path, ExpiresAt: expiry, HostOnly: hostOnly, Secure: c.Secure, HttpOnly: c.HttpOnly}
	}
	cookies := []CodexTicketCookie{byName["cflb"], byName["oailb"]}
	if !CodexTicketCookiesValid(cookies, now) {
		return nil
	}
	return cookies
}

// Validate without normalizing or dropping elements: a malformed cache record
// must never turn into a partial cookie package during injection.
func CodexTicketCookiesValid(cookies []CodexTicketCookie, now time.Time) bool {
	if len(cookies) != 2 || now.IsZero() {
		return false
	}
	seen := make(map[string]bool, 2)
	total := 0
	for _, c := range cookies {
		if !codexTicketCookieName(c.Name) || seen[c.Name] || c.Value == "" ||
			strings.ToLower(strings.TrimPrefix(c.Domain, ".")) != "chatgpt.com" ||
			!codexTicketCookiePathMatches(c.Path, "/backend-api/codex/responses") ||
			!c.ExpiresAt.After(now) {
			return false
		}
		if err := (&http.Cookie{Name: c.Name, Value: c.Value, Path: c.Path}).Valid(); err != nil {
			return false
		}
		seen[c.Name] = true
		total += len(c.Name) + len(c.Value) + 2
	}
	return total <= codexTicketMaxCookieBytes
}

func CodexTicketCookiesRequired(c CodexTicketSettings) bool {
	return c.CookiePinMode == CodexTicketCookiePinRequired
}

func CodexTicketCookieExpiry(cookies []CodexTicketCookie) time.Time {
	var expiry time.Time
	for _, cookie := range cookies {
		if cookie.ExpiresAt.IsZero() {
			return time.Time{}
		}
		if expiry.IsZero() || cookie.ExpiresAt.Before(expiry) {
			expiry = cookie.ExpiresAt
		}
	}
	return expiry
}

func CodexTicketCookieHeader(cookies []CodexTicketCookie, now time.Time) string {
	if !CodexTicketCookiesValid(cookies, now) {
		return ""
	}
	parts := make([]string, 0, 2)
	for _, c := range cookies {
		// Cookie.String preserves quoting when the value contains spaces/commas.
		parts = append(parts, (&http.Cookie{Name: c.Name, Value: c.Value}).String())
	}
	return strings.Join(parts, "; ")
}

// Use the configured early-refresh window against the actual bundle expiry.
// Short-lived cookies may require immediate refresh, subject to the existing
// shared budget and cooldown. Never advertise a refresh before capture.
func codexTicketRefreshAt(captured, expiry time.Time, cfg CodexTicketSettings) time.Time {
	refresh := expiry.Add(-time.Duration(cfg.RefreshBeforeSeconds) * time.Second)
	if refresh.Before(captured) {
		refresh = captured
	}
	return refresh
}
