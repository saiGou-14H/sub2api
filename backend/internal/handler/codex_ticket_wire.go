package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func ProvideCodexTicketHandler(repo service.CodexTicketSettingsRepository, proxies service.ProxyRepository, runtime *service.CodexTicketRuntime, gateway *service.OpenAIGatewayService) *admin.CodexTicketHandler {
	gateway.SetCodexTicketRuntime(runtime)
	runtime.SetProbe(gateway.ProbeCodexTicket)
	h := admin.NewCodexTicketHandler(repo, proxies)
	h.SetStatusProvider(func(ctx context.Context, _ service.CodexTicketSettings) (any, error) {
		return runtime.Status(ctx)
	})
	return h
}
