package server

import (
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/webcodex/runner"
	"github.com/gin-gonic/gin"
)

// WebCodexRunner owns opt-in polling and panel-JWT credential management.
// Credentials use the host SQL pool; leases and pending results remain in memory.
// It does not grant projects or dispatch business operations.
type WebCodexRunner struct {
	registry *runner.Registry
	handler  *runner.HTTPHandler
	tokens   *webCodexAgentTokens
}

// ProvideWebCodexRunner assembles the original transport with current host identity.
// Disabled configuration allocates no registry and performs no credential lookup.
func ProvideWebCodexRunner(cfg *config.Config, db *sql.DB, users service.UserRepository) (*WebCodexRunner, error) {
	if cfg == nil {
		return nil, errors.New("WebCodex Runner configuration is required")
	}
	options := cfg.WebCodexRunner
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if !options.Enabled {
		return &WebCodexRunner{}, nil
	}
	if db == nil || users == nil {
		return nil, errors.New("enabled WebCodex Runner requires host database and users")
	}
	keys := repository.NewWebCodexAPIKeyRepository(db)
	authenticate, err := runner.NewAgentTokenAuthenticator(
		keys,
		repository.NewWebCodexHostUserResolver(users),
		runner.AgentTokenOptions{MaxTokenBytes: options.MaxTokenBytes, Now: time.Now},
	)
	if err != nil {
		return nil, err
	}
	registry, err := runner.NewRegistry(runner.Options{
		MaxRunners: options.MaxRunners, MaxPendingPerRunner: options.MaxPendingPerRunner,
		OnlineWindow:     time.Duration(options.OnlineWindowSeconds) * time.Second,
		MaxJobsPerRunner: options.MaxJobsPerRunner,
		JobRecoveryGrace: time.Duration(options.JobRecoveryGraceSeconds) * time.Second,
	})
	if err != nil {
		return nil, err
	}
	handler, err := runner.NewHTTPHandler(registry, authenticate, options.MaxBodyBytes)
	if err != nil {
		registry.Close()
		return nil, err
	}
	return &WebCodexRunner{registry: registry, handler: handler, tokens: &webCodexAgentTokens{keys: keys, users: users, maxBodyBytes: options.MaxBodyBytes}}, nil
}

// mount registers the original Runner transport paths, without model-key or JWT middleware.
// Runner handler method behavior is preserved; global router middleware may handle OPTIONS first.
func (r *WebCodexRunner) mount(router *gin.Engine) {
	if r == nil || r.handler == nil {
		return
	}
	for _, action := range []string{"register", "poll", "result", "offline", "job_update"} {
		router.Any("/api/shell/agent/"+action, gin.WrapH(r.handler))
	}
}

// Close stops admission and settles in-memory waiters. It cannot stop remote work.
func (r *WebCodexRunner) Close() {
	if r != nil && r.registry != nil {
		r.registry.Close()
	}
}
