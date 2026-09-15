// SPDX-License-Identifier: Apache-2.0
// Adapted from WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9,
// src/runner_http/auth.rs and crates/webcodex-runner-registry/src/access*.rs.
package runner

import (
	"errors"
	"strings"
)

// ErrForbidden means authenticated authority cannot access this Runner operation.
var ErrForbidden = errors.New("Runner access denied")

// Principal is a verified host-auth projection, never decoded from a Runner body.
// The host must validate credential expiry, revocation and scope before producing it.
type Principal struct {
	Kind            string
	Username        string
	AllowedClientID string
	SharedKeyHash   string
	ProjectGrantID  string
	Scopes          []string
}

// Original WebCodex transport credential kinds and scopes.
const (
	AgentToken     = "agent_token"
	SharedKey      = "shared_key"
	Bootstrap      = "bootstrap"
	ScopeRegister  = "agent:register"
	ScopePoll      = "agent:poll"
	ScopeResult    = "agent:result"
	ScopeJobUpdate = "agent:job_update"
)

// Access is the existing non-secret owner/group projection for trusted dispatchers.
// GlobalVisibility grants observation, not the managed owner execution bypass.
type Access struct {
	GlobalVisibility bool
	OwnerBypass      bool
	Username         string
	GroupKind        string
	GroupID          string
}

func (p Principal) authorize(clientID, scope string) (Access, error) {
	if p.Kind == Bootstrap {
		return Access{GlobalVisibility: true, OwnerBypass: true}, nil
	}
	permitted := false
	for _, value := range p.Scopes {
		if value == scope {
			permitted = true
			break
		}
	}
	if !permitted {
		return Access{}, ErrForbidden
	}
	switch p.Kind {
	case AgentToken:
		if p.AllowedClientID != clientID || strings.TrimSpace(p.Username) == "" {
			return Access{}, ErrForbidden
		}
		access := Access{Username: p.Username}
		if p.ProjectGrantID != "" {
			access.GroupKind = "project_grant"
			access.GroupID = p.ProjectGrantID
		}
		return access, nil
	case SharedKey:
		if len(p.SharedKeyHash) != 64 || strings.IndexFunc(p.SharedKeyHash, func(r rune) bool { return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') }) >= 0 {
			return Access{}, ErrForbidden
		}
		return Access{GroupKind: "shared_key", GroupID: p.SharedKeyHash}, nil
	default:
		return Access{}, ErrForbidden
	}
}

func (p Principal) owner(requested *string) (*string, error) {
	switch p.Kind {
	case Bootstrap:
		if requested == nil || strings.TrimSpace(*requested) == "" {
			return nil, nil
		}
		value := *requested
		return &value, nil
	case SharedKey:
		return nil, nil
	case AgentToken:
		if requested != nil && strings.TrimSpace(*requested) != "" && *requested != p.Username {
			return nil, ErrForbidden
		}
		value := p.Username
		return &value, nil
	default:
		return nil, ErrForbidden
	}
}

func visible(access Access, record *node) bool {
	if access.GlobalVisibility {
		return true
	}
	if access.GroupKind != record.access.GroupKind || access.GroupID != record.access.GroupID {
		return false
	}
	if record.access.GroupKind != "" {
		return true
	}
	return owns(access, record)
}

func owns(access Access, record *node) bool {
	return record.registration.Owner != nil && strings.TrimSpace(access.Username) != "" && access.Username == *record.registration.Owner
}

func permitted(access Access, record *node) bool {
	if !visible(access, record) {
		return false
	}
	return record.access.GroupKind != "" || access.OwnerBypass || owns(access, record)
}
