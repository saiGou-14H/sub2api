package repository

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/webcodex/runner"
)

// NewWebCodexHostUserResolver adapts the existing uncached User repository.
// Usernames, roles, balance and model groups never become Runner authority.
// DeletedAt is checked explicitly even if a caller bypasses Ent soft deletion.
func NewWebCodexHostUserResolver(users service.UserRepository) runner.HostUserResolver {
	return func(ctx context.Context, id int64) (runner.HostUserIdentity, error) {
		if err := ctx.Err(); err != nil {
			return runner.HostUserIdentity{}, err
		}
		if id <= 0 {
			return runner.HostUserIdentity{}, runner.ErrAgentCredentialInvalid
		}
		if users == nil {
			return runner.HostUserIdentity{}, runner.ErrAgentCredentialUnavailable
		}
		user, err := users.GetByID(ctx, id)
		if ctx.Err() != nil {
			return runner.HostUserIdentity{}, ctx.Err()
		}
		if errors.Is(err, service.ErrUserNotFound) {
			return runner.HostUserIdentity{}, nil
		}
		if err != nil {
			return runner.HostUserIdentity{}, runner.ErrAgentCredentialUnavailable
		}
		if user == nil || user.ID != id {
			return runner.HostUserIdentity{}, nil
		}
		return runner.HostUserIdentity{ID: user.ID, Active: user.DeletedAt == nil && user.IsActive()}, nil
	}
}
