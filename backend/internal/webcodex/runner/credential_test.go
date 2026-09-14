// SPDX-License-Identifier: Apache-2.0
package runner

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// Public SHA-256("abc") vector, not a generated or deployed credential.
const credentialToken = "abc"
const credentialHash = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"

// These fakes return owned snapshots and record only inputs needed by tests.
type credentialFixture struct {
	mu                                    sync.Mutex
	record                                *AgentCredentialRecord
	user                                  HostUserIdentity
	lookupErr, resolveErr, touchErr       error
	afterLookup, afterResolve, afterTouch func()
	hashes                                []string
	userIDs                               []int64
	touches                               []credentialTouch
	now                                   time.Time
}
type credentialTouch struct {
	id      string
	seconds int64
}

func (f *credentialFixture) GetByHash(_ context.Context, hash string) (*AgentCredentialRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hashes = append(f.hashes, hash)
	if f.afterLookup != nil {
		f.afterLookup()
	}
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	if f.record == nil || hash != credentialHash {
		return nil, nil
	}
	copy := *f.record
	if copy.AllowedClientID != nil {
		value := *copy.AllowedClientID
		copy.AllowedClientID = &value
	}
	if copy.ExpiresAt != nil {
		value := *copy.ExpiresAt
		copy.ExpiresAt = &value
	}
	if copy.RevokedAt != nil {
		value := *copy.RevokedAt
		copy.RevokedAt = &value
	}
	return &copy, nil
}
func (f *credentialFixture) UpdateLastUsed(_ context.Context, id string, seconds int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.touches = append(f.touches, credentialTouch{id, seconds})
	if f.afterTouch != nil {
		f.afterTouch()
	}
	return f.touchErr
}
func (f *credentialFixture) resolve(_ context.Context, id int64) (HostUserIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userIDs = append(f.userIDs, id)
	if f.afterResolve != nil {
		f.afterResolve()
	}
	return f.user, f.resolveErr
}
func newCredentialFixture() *credentialFixture {
	clientID := "node-a"
	return &credentialFixture{
		record: &AgentCredentialRecord{ID: "original-key-id", UserID: "sub2api_user_42", KeyHash: credentialHash, Kind: "agent", Scopes: "agent:register agent:poll agent:result agent:job_update", AllowedClientID: &clientID},
		user:   HostUserIdentity{ID: 42, Active: true},
		now:    time.Unix(1_800_000_000, 999_999_999),
	}
}
func (f *credentialFixture) authenticator(t *testing.T) Authenticate {
	t.Helper()
	auth, err := NewAgentTokenAuthenticator(f, f.resolve, AgentTokenOptions{MaxTokenBytes: 128, Now: func() time.Time { return f.now }})
	if err != nil {
		t.Fatal(err)
	}
	return auth
}
func assertCredentialFailure(t *testing.T, auth Authenticate, ctx context.Context, token string, expected error) {
	t.Helper()
	principal, err := auth(ctx, token)
	if !errors.Is(err, expected) || !reflect.DeepEqual(principal, Principal{}) {
		t.Fatalf("failure returned principal=%+v error=%v; want %v", principal, err, expected)
	}
}

func TestAgentCredentialRequiresExplicitDependencies(t *testing.T) {
	f := newCredentialFixture()
	valid := AgentTokenOptions{MaxTokenBytes: 128, Now: time.Now}
	for _, tc := range []struct {
		name    string
		repo    AgentCredentialRepository
		resolve HostUserResolver
		options AgentTokenOptions
	}{
		{"repository", nil, f.resolve, valid},
		{"resolver", f, nil, valid},
		{"clock", f, f.resolve, AgentTokenOptions{MaxTokenBytes: 128}},
		{"zero byte bound", f, f.resolve, AgentTokenOptions{Now: time.Now}},
		{"negative byte bound", f, f.resolve, AgentTokenOptions{MaxTokenBytes: -1, Now: time.Now}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if auth, err := NewAgentTokenAuthenticator(tc.repo, tc.resolve, tc.options); err == nil || auth != nil {
				t.Fatal("incomplete dependencies admitted")
			}
		})
	}
}

func TestAgentCredentialHashesExactBytesAndProjectsManagedIdentity(t *testing.T) {
	f := newCredentialFixture()
	auth := f.authenticator(t)
	principal, err := auth(context.Background(), credentialToken)
	if err != nil {
		t.Fatal(err)
	}
	want := Principal{Kind: AgentToken, Username: "sub2api_user_42", AllowedClientID: "node-a", Scopes: []string{ScopeRegister, ScopePoll, ScopeResult, "agent:job_update"}}
	if !reflect.DeepEqual(principal, want) {
		t.Fatalf("projection: %+v", principal)
	}
	if !reflect.DeepEqual(f.hashes, []string{credentialHash}) || !reflect.DeepEqual(f.userIDs, []int64{42}) || !reflect.DeepEqual(f.touches, []credentialTouch{{"original-key-id", f.now.Unix()}}) {
		t.Fatal("hash lookup, immutable user lookup or original last-used projection changed")
	}
	// There is no wc_agent_ prefix gate in the original DB verifier. Conversely,
	// trimming a supplied credential would authorize the wrong exact bytes.
	assertCredentialFailure(t, auth, context.Background(), " abc ", ErrAgentCredentialInvalid)
	if len(f.hashes) != 2 || f.hashes[1] == credentialHash || len(f.touches) != 1 {
		t.Fatal("plaintext was normalized or invalid token was touched")
	}
	principal.Scopes[0] = "admin"
	again, err := auth(context.Background(), credentialToken)
	if err != nil || again.Scopes[0] != ScopeRegister {
		t.Fatal("returned scope mutation changed later authority")
	}
}

func TestAgentCredentialInputByteBound(t *testing.T) {
	for _, token := range []string{"", strings.Repeat("a", 129), strings.Repeat("猫", 43), string([]byte{0xff})} {
		f := newCredentialFixture()
		assertCredentialFailure(t, f.authenticator(t), context.Background(), token, ErrAgentCredentialInvalid)
		if len(f.hashes) != 0 {
			t.Fatal("invalid input reached repository")
		}
	}
	f := newCredentialFixture()
	auth, err := NewAgentTokenAuthenticator(f, f.resolve, AgentTokenOptions{MaxTokenBytes: 3, Now: func() time.Time { return f.now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = auth(context.Background(), credentialToken); err != nil {
		t.Fatal("exact byte bound rejected")
	}
}

func TestAgentCredentialRejectsInvalidRowsAndKinds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*credentialFixture)
	}{
		{"not found", func(f *credentialFixture) { f.record = nil }},
		{"wrong hash", func(f *credentialFixture) { f.record.KeyHash = strings.Repeat("0", 64) }},
		{"uppercase hash", func(f *credentialFixture) { f.record.KeyHash = strings.ToUpper(credentialHash) }},
		{"empty hash", func(f *credentialFixture) { f.record.KeyHash = "" }},
		{"missing credential ID", func(f *credentialFixture) { f.record.ID = "" }},
		{"unmapped user", func(f *credentialFixture) { f.record.UserID = "" }},
		{"negative user", func(f *credentialFixture) { f.record.UserID = "sub2api_user_-1" }},
		{"zero user", func(f *credentialFixture) { f.record.UserID = "sub2api_user_0" }},
		{"noncanonical user", func(f *credentialFixture) { f.record.UserID = "sub2api_user_042" }},
		{"signed user", func(f *credentialFixture) { f.record.UserID = "sub2api_user_+42" }},
		{"bare user ID", func(f *credentialFixture) { f.record.UserID = "42" }},
		{"profile user", func(f *credentialFixture) { f.record.UserID = "alice" }},
		{"unmapped legacy user", func(f *credentialFixture) { f.record.UserID = "d2888227-1e99-44fd-a013-708fce0d7e83" }},
		{"overflow user", func(f *credentialFixture) { f.record.UserID = "sub2api_user_9223372036854775808" }},
		{"revoked", func(f *credentialFixture) { at := f.now.Unix(); f.record.RevokedAt = &at }},
		{"future revocation marker", func(f *credentialFixture) { at := f.now.Unix() + 100; f.record.RevokedAt = &at }},
		{"unbound client", func(f *credentialFixture) { f.record.AllowedClientID = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCredentialFixture()
			tc.change(f)
			assertCredentialFailure(t, f.authenticator(t), context.Background(), credentialToken, ErrAgentCredentialInvalid)
			if len(f.userIDs) != 0 || len(f.touches) != 0 {
				t.Fatal("invalid credential reached user/touch")
			}
		})
	}
	for _, kind := range []string{"api_key", "agent_token", "Agent", "agent ", "bootstrap", "shared_key", "project", "oauth2", "account"} {
		t.Run("kind_"+kind, func(t *testing.T) {
			f := newCredentialFixture()
			f.record.Kind = kind
			assertCredentialFailure(t, f.authenticator(t), context.Background(), credentialToken, ErrAgentCredentialInvalid)
			if len(f.touches) != 0 {
				t.Fatal("non-managed-agent token was touched")
			}
		})
	}
}

func TestAgentCredentialUserKindsRemainNonTransport(t *testing.T) {
	for _, kind := range []string{"user", ""} {
		f := newCredentialFixture()
		f.record.Kind = kind
		f.record.AllowedClientID = nil
		f.record.Scopes = "admin agent:register agent:future"
		auth := f.authenticator(t)
		p, err := auth(context.Background(), credentialToken)
		if err != nil || p.Kind != "api_token" || p.Username != "sub2api_user_42" || p.AllowedClientID != "" {
			t.Fatalf("original user kind projection: %+v %v", p, err)
		}
		if _, err := p.authorize("node-a", ScopeRegister); !errors.Is(err, ErrForbidden) {
			t.Fatal("verified user token granted transport")
		}
		at := f.now.Unix()
		f.record.ExpiresAt = &at
		assertCredentialFailure(t, auth, context.Background(), credentialToken, ErrAgentCredentialInvalid)
	}
}

func TestAgentCredentialSourceClientBounds(t *testing.T) {
	for _, client := range []string{"", " node-a", "node-a ", "a/b", "猫", strings.Repeat("a", 81)} {
		f := newCredentialFixture()
		f.record.AllowedClientID = &client
		assertCredentialFailure(t, f.authenticator(t), context.Background(), credentialToken, ErrAgentCredentialInvalid)
	}
	for _, client := range []string{"A_b-1.2", strings.Repeat("a", 80)} {
		f := newCredentialFixture()
		f.record.AllowedClientID = &client
		p, err := f.authenticator(t)(context.Background(), credentialToken)
		if err != nil || p.AllowedClientID != client {
			t.Fatalf("source-valid client changed: %q %v", client, err)
		}
	}
}

func TestAgentCredentialSourceScopeWhitespaceAndOrder(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want []string
	}{
		{"agent:poll\u0085agent:register\t\nagent:poll\u00a0agent:job_update\u2003agent:result", []string{ScopePoll, ScopeRegister, ScopePoll, "agent:job_update", ScopeResult}},
		{"agent:poll agent:future agent:poll", []string{ScopePoll, "agent:future", ScopePoll}},
		{"agent:poll\ufeffagent:result", []string{"agent:poll\ufeffagent:result"}},
		{"\ufeffagent:poll", []string{"\ufeffagent:poll"}},
		{"", []string{}},
		{" \r\n\u0085", []string{}},
	} {
		f := newCredentialFixture()
		f.record.Scopes = tc.raw
		p, err := f.authenticator(t)(context.Background(), credentialToken)
		if err != nil || !reflect.DeepEqual(p.Scopes, tc.want) {
			t.Fatalf("scope projection: %#v %v", p.Scopes, err)
		}
	}
	for _, scopes := range []string{"admin", "runtime:read", "agent:*", "agent:unknown", "AGENT:POLL", "agent:poll\ufeffagent:result", "\ufeffagent:poll"} {
		f := newCredentialFixture()
		f.record.Scopes = scopes
		p, err := f.authenticator(t)(context.Background(), credentialToken)
		if err != nil || !reflect.DeepEqual(p.Scopes, []string{scopes}) {
			t.Fatalf("stored scope was filtered instead of projected: %+v %v", p, err)
		}
		if _, err := p.authorize("node-a", ScopeRegister); !errors.Is(err, ErrForbidden) {
			t.Fatal("extra/admin scope granted registration or bootstrap")
		}
	}
}

func TestAgentCredentialExpiryEqualityAndCurrentHostUser(t *testing.T) {
	for _, delta := range []int64{-1, 0, 1} {
		f := newCredentialFixture()
		exp := f.now.Unix() + delta
		f.record.ExpiresAt = &exp
		p, err := f.authenticator(t)(context.Background(), credentialToken)
		if delta <= 0 {
			if !errors.Is(err, ErrAgentCredentialInvalid) || !reflect.DeepEqual(p, Principal{}) || len(f.touches) != 0 {
				t.Fatalf("expiry equality admitted: %d %+v %v", delta, p, err)
			}
		} else if err != nil {
			t.Fatal("unexpired Unix second rejected", err)
		}
	}
	for _, user := range []HostUserIdentity{{}, {ID: 42}, {ID: 43, Active: true}, {ID: -1, Active: true}} {
		f := newCredentialFixture()
		f.user = user
		assertCredentialFailure(t, f.authenticator(t), context.Background(), credentialToken, ErrAgentCredentialInvalid)
		if !reflect.DeepEqual(f.userIDs, []int64{42}) || len(f.touches) != 0 {
			t.Fatal("host resolution did not enforce the requested active ID")
		}
	}
	f := newCredentialFixture()
	exp := f.now.Unix() + 1
	f.record.ExpiresAt = &exp
	f.afterResolve = func() { f.now = f.now.Add(time.Second) }
	assertCredentialFailure(t, f.authenticator(t), context.Background(), credentialToken, ErrAgentCredentialInvalid)
}

func TestAgentCredentialStoreFailuresAndLastUsed(t *testing.T) {
	privateError := errors.New("storage error containing a private token")
	for _, stage := range []string{"lookup", "resolve"} {
		f := newCredentialFixture()
		if stage == "lookup" {
			f.lookupErr = privateError
		} else {
			f.resolveErr = privateError
		}
		assertCredentialFailure(t, f.authenticator(t), context.Background(), credentialToken, ErrAgentCredentialUnavailable)
		if len(f.touches) != 0 {
			t.Fatal("lookup failure updated last used")
		}
	}
	f := newCredentialFixture()
	f.touchErr = privateError
	p, err := f.authenticator(t)(context.Background(), credentialToken)
	if err != nil || p.Kind != AgentToken || len(f.touches) != 1 {
		t.Fatal("best-effort last-used failure prevented valid authentication")
	}
}

func TestAgentCredentialCancellationNeverReturnsAuthority(t *testing.T) {
	for _, stage := range []string{"before", "lookup", "resolve", "touch"} {
		t.Run(stage, func(t *testing.T) {
			f := newCredentialFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch stage {
			case "before":
				cancel()
			case "lookup":
				f.afterLookup = cancel
			case "resolve":
				f.afterResolve = cancel
			case "touch":
				f.afterTouch = cancel
			}
			assertCredentialFailure(t, f.authenticator(t), ctx, credentialToken, context.Canceled)
			if stage == "before" && len(f.hashes) != 0 {
				t.Fatal("cancelled request queried credentials")
			}
			if stage != "touch" && len(f.touches) != 0 {
				t.Fatal("cancelled request touched credential")
			}
		})
	}
}

func TestAgentCredentialHostOwnerIgnoresProfileChanges(t *testing.T) {
	f := newCredentialFixture()
	profile := struct {
		id             int64
		username, role string
	}{42, "alice", "user"}
	resolve := func(_ context.Context, id int64) (HostUserIdentity, error) {
		if id != profile.id {
			t.Fatal("resolver selected by something other than user ID")
		}
		return HostUserIdentity{ID: profile.id, Active: true}, nil
	}
	auth, err := NewAgentTokenAuthenticator(f, resolve, AgentTokenOptions{MaxTokenBytes: 128, Now: func() time.Time { return f.now }})
	if err != nil {
		t.Fatal(err)
	}
	first, err := auth(context.Background(), credentialToken)
	if err != nil {
		t.Fatal(err)
	}
	profile.username, profile.role = "someone-else", "admin"
	second, err := auth(context.Background(), credentialToken)
	if err != nil || !reflect.DeepEqual(first, second) || second.Kind != AgentToken {
		t.Fatal("profile rename/role changed Runner authority")
	}
	for _, id := range []int64{1, 42, math.MaxInt64} {
		owner, err := HostUserOwner(id)
		if err != nil || len(owner) > 64 || strings.ContainsAny(owner, ". /:") {
			t.Fatalf("owner representation: %q %v", owner, err)
		}
	}
	other, _ := HostUserOwner(43)
	if other == first.Username {
		t.Fatal("different immutable users share owner")
	}
	for _, id := range []int64{0, -1} {
		if _, err := HostUserOwner(id); !errors.Is(err, ErrAgentCredentialInvalid) {
			t.Fatal("invalid host identity encoded")
		}
	}
}

func TestAgentCredentialConcurrentAuthentication(t *testing.T) {
	f := newCredentialFixture()
	auth := f.authenticator(t)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := auth(context.Background(), credentialToken)
			if err != nil || p.Username != "sub2api_user_42" {
				t.Errorf("concurrent authentication failed: %v", err)
			}
		}()
	}
	wg.Wait()
	if len(f.hashes) != 32 || len(f.userIDs) != 32 || len(f.touches) != 32 {
		t.Fatal("authentication unexpectedly cached or lost a lookup")
	}
}
