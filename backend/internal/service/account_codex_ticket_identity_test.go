//go:build unit

package service

import (
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCodexTicketIdentitySetupTokenAndRefresh(t *testing.T) {
	a := &Account{Platform: PlatformOpenAI, Type: AccountTypeSetupToken, Credentials: map[string]any{"chatgpt_account_id": "workspace", "chatgpt_user_id": "person-a", "organization_id": "org-a"}}
	before := CodexTicketIdentityScope(a)
	a.Credentials["chatgpt_user_id"] = "person-b"
	require.NotEqual(t, before, CodexTicketIdentityScope(a), "setup tokens must include user identity")
	before = CodexTicketIdentityScope(a)
	a.Credentials["organization_id"] = "org-b"
	require.NotEqual(t, before, CodexTicketIdentityScope(a))

	jwt := func(subject string, issued int) string {
		payload := fmt.Sprintf(`{"sub":%q,"iat":%d,"exp":%d,"aud":["api"],"https://api.openai.com/auth":{"chatgpt_account_id":"workspace","chatgpt_user_id":%q}}`, subject, issued, issued+3600, subject)
		return "e30." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".signature"
	}
	a.Credentials = map[string]any{"access_token": jwt("person-a", 100)}
	before = CodexTicketIdentityScope(a)
	require.NotEmpty(t, before)
	a.Credentials["access_token"] = jwt("person-a", 200)
	require.Equal(t, before, CodexTicketIdentityScope(a), "token issue and expiry times are not identity")
	a.Credentials["access_token"] = jwt("person-b", 200)
	require.NotEqual(t, before, CodexTicketIdentityScope(a), "same workspace with a new token subject must fence old state")
}
