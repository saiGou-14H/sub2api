package handler

import "github.com/Wei-Shaw/sub2api/internal/service"

// httpContinuationRequiresAPIKey keeps the legacy Codex/API-key restriction
// while allowing ChatGPT Web accounts to use previous_response_id as the
// public alias for their server-side Web conversation cursor.
func httpContinuationRequiresAPIKey(account *service.Account) bool {
	return account != nil && !account.IsOpenAIApiKey() && !account.IsOpenAIWebTransport()
}
