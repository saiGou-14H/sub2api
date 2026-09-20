package main

import (
	"context"
	"log"
	"time"
)

type codexTicketShutdown interface {
	Shutdown(context.Context) error
}

// A timeout is not a successful join. Retain the dependencies until process
// exit on a broken shutdown rather than closing them under a still-live probe.
func shutdownCodexTicketBeforeDependencies(runtime codexTicketShutdown) bool {
	if runtime == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if err := runtime.Shutdown(ctx); err != nil {
		log.Print("Codex ticket shutdown did not join; dependent services retained until process exit")
		return false
	}
	return true
}
