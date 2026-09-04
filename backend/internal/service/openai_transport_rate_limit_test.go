package service

import (
	"context"
	"testing"
	"time"
)

func TestOpenAITransportRateLimitReasonSeparatesWebAndCodex(t *testing.T) {
	resetAt := time.Now().Add(time.Minute)
	tests := []struct {
		name      string
		transport string
		scope     string
		want      string
	}{
		{name: "web", transport: OpenAITransportWeb, scope: openAIWebTransportRateLimitKey, want: "web_rate_limited"},
		{name: "codex", transport: OpenAITransportCodex, scope: openAICodexTransportRateLimitKey, want: "codex_rate_limited"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &Account{
				ID:       1,
				Platform: PlatformOpenAI,
				Type:     AccountTypeOAuth,
				Status:   StatusActive,
				Extra: map[string]any{
					OpenAIWebTransportExtraKey: tt.transport,
				},
			}
			setAccountModelRateLimitSnapshot(account, tt.scope, resetAt, "test", time.Now())

			if got := account.OpenAITransportRateLimitReason(); got != tt.want {
				t.Fatalf("OpenAITransportRateLimitReason() = %q, want %q", got, tt.want)
			}
			if account.IsSchedulableForModelWithContext(context.Background(), "auto") {
				t.Fatalf("IsSchedulableForModelWithContext() = true, want false for %s limit", tt.name)
			}
			if !shouldClearStickySession(account, "auto") {
				t.Fatalf("shouldClearStickySession() = false, want true for %s limit", tt.name)
			}
			scheduler := &defaultOpenAIAccountScheduler{}
			compatible, reason := scheduler.isAccountRequestCompatibleReason(context.Background(), account, OpenAIAccountScheduleRequest{})
			if compatible || reason != tt.want {
				t.Fatalf("scheduler compatibility = (%v, %q), want (false, %q)", compatible, reason, tt.want)
			}
		})
	}
}

func TestOpenAITransportRateLimitDoesNotCrossProtocols(t *testing.T) {
	resetAt := time.Now().Add(time.Minute)
	web := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Extra:       map[string]any{OpenAIWebTransportExtraKey: OpenAITransportWeb},
	}
	setAccountModelRateLimitSnapshot(web, openAICodexTransportRateLimitKey, resetAt, "test", time.Now())
	if got := web.OpenAITransportRateLimitReason(); got != "" {
		t.Fatalf("Web account inherited Codex limit: %q", got)
	}
	if !web.IsSchedulableForModelWithContext(context.Background(), "auto") {
		t.Fatal("Web account inherited Codex limit through model scheduling")
	}

	codex := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Extra:       map[string]any{OpenAIWebTransportExtraKey: OpenAITransportCodex},
	}
	setAccountModelRateLimitSnapshot(codex, openAIWebTransportRateLimitKey, resetAt, "test", time.Now())
	if got := codex.OpenAITransportRateLimitReason(); got != "" {
		t.Fatalf("Codex account inherited Web limit: %q", got)
	}
	if !codex.IsSchedulableForModelWithContext(context.Background(), "gpt-5.6-luna") {
		t.Fatal("Codex account inherited Web limit through model scheduling")
	}
}
