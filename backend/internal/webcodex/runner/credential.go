// SPDX-License-Identifier: Apache-2.0
// Adapted from WebCodex 97ad66949a859174911c2f6da2ff1063be98bfa9,
// src/auth/{pat,tokens,scopes}.rs and crates/webcodex-store/src/{accounts,models}.rs.
package runner

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrAgentCredentialInvalid     = errors.New("invalid Runner agent credential")
	ErrAgentCredentialUnavailable = errors.New("Runner agent credential lookup unavailable")
)

// AgentCredentialRecord is the authentication projection of an original api_keys
// row. UserID is a canonical host subject string produced by HostUserOwner;
// legacy user references require explicit external import mapping and rewriting.
// ID remains the credential ID, not the user identity.
// Times are nullable Unix seconds. KeyHash is the lowercase SHA-256 digest.
// Kind preserves the original database value: "agent", "user", or legacy "".
//
// Only managed-user database credentials belong here. ProjectAgentTokenVerifier,
// OAuth, shared keys and bootstrap have separate authority sources; this record
// deliberately cannot carry a project grant or shared-key group.
type AgentCredentialRecord struct {
	ID              string
	UserID          string
	KeyHash         string
	Kind            string
	Scopes          string
	AllowedClientID *string
	ExpiresAt       *int64
	RevokedAt       *int64
}

// AgentCredentialRepository reads current, caller-owned snapshots. Not found
// returns (nil, nil); infrastructure errors return a non-nil error. Lookups must
// observe revocation and must never synthesize rows from request-body identity.
// Implementations must be safe for concurrent calls and honor context deadlines.
// UpdateLastUsed is best effort and must not change credential authority.
// The verifier neither caches records nor retains plaintext credentials.
type AgentCredentialRepository interface {
	GetByHash(ctx context.Context, keyHash string) (*AgentCredentialRecord, error)
	UpdateLastUsed(ctx context.Context, credentialID string, unixSeconds int64) error
}

// HostUserIdentity is resolved from the existing host User by immutable ID.
// Active must mean the current user exists, is not deleted, and is active.
// Profile username, role, balance and model billing groups are not authority here.
type HostUserIdentity struct {
	ID     int64
	Active bool
}

// HostUserResolver must query the requested host ID, never select by username.
// A missing user may return a zero identity or an error; both fail closed.
// The callback must be safe for concurrent calls and honor context deadlines.
type HostUserResolver func(context.Context, int64) (HostUserIdentity, error)

// AgentTokenOptions supplies explicit operational bounds and a concurrency-safe
// clock. MaxTokenBytes is a host ingress limit, not an original wire format limit.
// No default enables authentication or accepts missing dependencies.
type AgentTokenOptions struct {
	MaxTokenBytes int
	Now           func() time.Time
}

// HostUserOwner is the stable managed owner representation for new records.
// Imported records require explicit user-ID mapping and corresponding owner
// rewrites before use. Changing this encoding requires migrating those owners.
// This does not read or modify the user's profile username.
func HostUserOwner(userID int64) (string, error) {
	if userID <= 0 {
		return "", ErrAgentCredentialInvalid
	}
	return "sub2api_user_" + strconv.FormatInt(userID, 10), nil
}

func parseHostUserSubject(subject string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimPrefix(subject, "sub2api_user_"), 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrAgentCredentialInvalid
	}
	canonical, _ := HostUserOwner(id)
	if subject != canonical {
		return 0, ErrAgentCredentialInvalid
	}
	return id, nil
}

// NewAgentTokenAuthenticator builds the existing HTTP Authenticate callback.
// Only managed agent rows may authorize transport. Original user/legacy-empty
// kinds are verified and projected as non-transport api_token for Registry's 403.
// OAuth/account/project credentials are outside this repository contract.
// Registry checks requested scope, exact client ID, owner and generation before
// mutation. Constructing this callback mounts no route and starts no worker.
func NewAgentTokenAuthenticator(repository AgentCredentialRepository, resolve HostUserResolver, options AgentTokenOptions) (Authenticate, error) {
	if repository == nil || resolve == nil || options.Now == nil || options.MaxTokenBytes <= 0 {
		return nil, errors.New("credential repository, host user resolver, clock and positive token byte limit are required")
	}
	return func(ctx context.Context, token string) (Principal, error) {
		if err := ctx.Err(); err != nil {
			return Principal{}, err
		}
		if len(token) == 0 || len(token) > options.MaxTokenBytes || !utf8.ValidString(token) {
			return Principal{}, ErrAgentCredentialInvalid
		}
		// The original database verifier hashes exact UTF-8 bytes, without trim
		// or prefix-based credential classification.
		digest := sha256.Sum256([]byte(token))
		keyHash := hex.EncodeToString(digest[:])
		record, err := repository.GetByHash(ctx, keyHash)
		if contextErr := ctx.Err(); contextErr != nil {
			return Principal{}, contextErr
		}
		if err != nil {
			// Repository errors may contain secrets. Never wrap or echo them.
			return Principal{}, ErrAgentCredentialUnavailable
		}
		if record == nil || record.ID == "" || record.RevokedAt != nil ||
			subtle.ConstantTimeCompare([]byte(record.KeyHash), []byte(keyHash)) != 1 {
			return Principal{}, ErrAgentCredentialInvalid
		}
		hostUserID, err := parseHostUserSubject(record.UserID)
		if err != nil {
			return Principal{}, err
		}
		principalKind := AgentToken
		switch record.Kind {
		case "agent":
		case "user", "":
			principalKind = "api_token"
		default:
			return Principal{}, ErrAgentCredentialInvalid
		}
		clientID := ""
		if record.AllowedClientID != nil {
			clientID = *record.AllowedClientID
		}
		if principalKind == AgentToken && validateID(clientID, 80, true) != nil {
			return Principal{}, ErrAgentCredentialInvalid
		}
		// Rust scopes_vec splits stored whitespace and keeps all values, order
		// and duplicates. Issuance-time scope validation is not authentication.
		// Registry checks exact operation scopes; admin/unknown never bypass it.
		scopes := strings.Fields(record.Scopes)
		user, err := resolve(ctx, hostUserID)
		if contextErr := ctx.Err(); contextErr != nil {
			return Principal{}, contextErr
		}
		if err != nil {
			return Principal{}, ErrAgentCredentialUnavailable
		}
		if !user.Active || user.ID != hostUserID {
			return Principal{}, ErrAgentCredentialInvalid
		}
		now := options.Now().Unix()
		if record.ExpiresAt != nil && now >= *record.ExpiresAt {
			return Principal{}, ErrAgentCredentialInvalid
		}
		owner, err := HostUserOwner(user.ID)
		if err != nil {
			return Principal{}, err
		}
		principal := Principal{Kind: principalKind, Username: owner, AllowedClientID: clientID, Scopes: scopes}
		// Preserve the source's best-effort last-used write, without a cache,
		// background retry or logging of repository errors.
		_ = repository.UpdateLastUsed(ctx, record.ID, now)
		if err := ctx.Err(); err != nil {
			return Principal{}, err
		}
		return principal, nil
	}, nil
}
