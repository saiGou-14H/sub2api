// SPDX-License-Identifier: Apache-2.0
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

func credentialHTTP(t *testing.T, f *credentialFixture) (*Registry, *HTTPHandler) {
	t.Helper()
	r := testRegistry(t)
	h, err := NewHTTPHandler(r, f.authenticator(t), 8192)
	if err != nil {
		t.Fatal(err)
	}
	return r, h
}
func credentialPost(t *testing.T, h http.Handler, action string, body any) *httptest.ResponseRecorder {
	t.Helper()
	wire, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return sendHTTP(h, "/api/shell/agent/"+action, credentialToken, string(wire))
}

func TestAgentCredentialHTTPPollingExchange(t *testing.T) {
	f := newCredentialFixture()
	r, h := credentialHTTP(t, f)
	response := credentialPost(t, h, "register", json.RawMessage(completeRegistration))
	if response.Code != 200 {
		t.Fatalf("register %d %s", response.Code, response.Body.String())
	}
	var registrationResponse protocol.RunnerRegisterResponse
	if err := json.Unmarshal(response.Body.Bytes(), &registrationResponse); err != nil {
		t.Fatal(err)
	}
	if registrationResponse.Client == nil || registrationResponse.Client.Owner == nil || *registrationResponse.Client.Owner != "sub2api_user_42" {
		t.Fatal("registration did not project the immutable host owner")
	}
	owner, _ := HostUserOwner(42)
	pending, err := r.Enqueue(Access{Username: owner}, input("credential-exchange"))
	if err != nil {
		t.Fatal(err)
	}
	defer pending.Cancel(nil)
	response = credentialPost(t, h, "poll", protocol.RunnerPollRequest{ClientID: "node-a", AgentInstanceID: "process-a"})
	var polled protocol.RunnerPollResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &polled) != nil || polled.Request == nil || polled.Request.RequestID != "credential-exchange" {
		t.Fatalf("poll: %d %s", response.Code, response.Body.String())
	}
	response = credentialPost(t, h, "result", result("node-a", "process-a", "credential-exchange"))
	if response.Code != 200 {
		t.Fatalf("result %d %s", response.Code, response.Body.String())
	}
	if outcome := wait(t, pending); outcome.Err != nil || !outcome.Dispatched || outcome.Result == nil {
		t.Fatalf("exchange: %+v", outcome)
	}
	response = credentialPost(t, h, "offline", protocol.RunnerOfflineRequest{ClientID: "node-a", AgentInstanceID: "process-a"})
	if response.Code != 200 {
		t.Fatalf("offline %d %s", response.Code, response.Body.String())
	}
	view, err := r.View(Access{Username: owner}, "node-a")
	if err != nil || view.Connected {
		t.Fatal("offline did not close the original lease")
	}
	if len(f.hashes) != 4 || len(f.userIDs) != 4 || len(f.touches) != 4 {
		t.Fatal("transport skipped fresh credential/user checks")
	}
}

func TestAgentCredentialHTTPRevocationAndDisabledUserLeaveResultPending(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*credentialFixture)
	}{
		{"credential revocation", func(f *credentialFixture) { at := f.now.Unix(); f.record.RevokedAt = &at }},
		{"credential deletion", func(f *credentialFixture) { f.record = nil }},
		{"user disabled", func(f *credentialFixture) { f.user.Active = false }},
		{"user mismatch", func(f *credentialFixture) { f.user.ID = 43 }},
		{"expired", func(f *credentialFixture) { at := f.now.Unix(); f.record.ExpiresAt = &at }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCredentialFixture()
			r, h := credentialHTTP(t, f)
			if response := credentialPost(t, h, "register", json.RawMessage(completeRegistration)); response.Code != 200 {
				t.Fatal("register failed")
			}
			owner, _ := HostUserOwner(42)
			pending, err := r.Enqueue(Access{Username: owner}, input("held-result"))
			if err != nil {
				t.Fatal(err)
			}
			defer pending.Cancel(nil)
			if response := credentialPost(t, h, "poll", protocol.RunnerPollRequest{ClientID: "node-a", AgentInstanceID: "process-a"}); response.Code != 200 {
				t.Fatal("poll failed")
			}
			tc.change(f)
			response := credentialPost(t, h, "result", result("node-a", "process-a", "held-result"))
			if response.Code != 401 || response.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("revoked authority: %d %s", response.Code, response.Body.String())
			}
			select {
			case <-pending.request.done:
				t.Fatal("failed authentication consumed pending result")
			default:
			}
			view, err := r.View(Access{Username: owner}, "node-a")
			if err != nil || !view.Connected || view.AgentInstanceID != "process-a" {
				t.Fatal("failed authentication mutated lease")
			}
		})
	}
}

func TestAgentCredentialHTTPExactScopesAndBindings(t *testing.T) {
	for _, action := range []string{"register", "poll", "result", "offline"} {
		t.Run(action, func(t *testing.T) {
			f := newCredentialFixture()
			r, h := credentialHTTP(t, f)
			if response := credentialPost(t, h, "register", json.RawMessage(completeRegistration)); response.Code != 200 {
				t.Fatal("register failed")
			}
			owner, _ := HostUserOwner(42)
			pending, err := r.Enqueue(Access{Username: owner}, input("scope-held"))
			if err != nil {
				t.Fatal(err)
			}
			defer pending.Cancel(nil)
			if response := credentialPost(t, h, "poll", protocol.RunnerPollRequest{ClientID: "node-a", AgentInstanceID: "process-a"}); response.Code != 200 {
				t.Fatal("poll failed")
			}
			var body any
			switch action {
			case "register":
				body = registration(t, "node-a", "process-b")
				f.record.Scopes = "agent:poll agent:result agent:job_update"
			case "poll":
				body = protocol.RunnerPollRequest{ClientID: "node-a", AgentInstanceID: "process-a"}
				f.record.Scopes = "agent:register agent:result agent:job_update"
			case "result":
				body = result("node-a", "process-a", "scope-held")
				f.record.Scopes = "agent:register agent:poll agent:job_update"
			case "offline":
				body = protocol.RunnerOfflineRequest{ClientID: "node-a", AgentInstanceID: "process-a"}
				f.record.Scopes = "agent:poll agent:result agent:job_update"
			}
			response := credentialPost(t, h, action, body)
			if response.Code != 403 {
				t.Fatalf("missing exact scope admitted on %s: %d", action, response.Code)
			}
			select {
			case <-pending.request.done:
				t.Fatal("scope rejection settled pending request")
			default:
			}
			view, err := r.View(Access{Username: owner}, "node-a")
			if err != nil || !view.Connected || view.AgentInstanceID != "process-a" {
				t.Fatal("missing scope mutated lease")
			}
		})
	}
	for _, kind := range []string{"wrong client", "profile owner", "different host owner"} {
		t.Run(kind, func(t *testing.T) {
			f := newCredentialFixture()
			r, h := credentialHTTP(t, f)
			if response := credentialPost(t, h, "register", json.RawMessage(completeRegistration)); response.Code != 200 {
				t.Fatal("register failed")
			}
			body := registration(t, "node-a", "process-b")
			switch kind {
			case "wrong client":
				client := "node-b"
				f.record.AllowedClientID = &client
			case "profile owner":
				owner := "alice"
				body.Owner = &owner
			case "different host owner":
				f.record.UserID = "sub2api_user_43"
				f.user.ID = 43
			}
			response := credentialPost(t, h, "register", body)
			if response.Code != 403 {
				t.Fatalf("binding mismatch admitted: %d %s", response.Code, response.Body.String())
			}
			owner, _ := HostUserOwner(42)
			view, err := r.View(Access{Username: owner}, "node-a")
			if err != nil || view.AgentInstanceID != "process-a" {
				t.Fatal("rejected binding replaced lease")
			}
		})
	}
}

func TestAgentCredentialHTTPStoredScopesAreNotIssuanceValidated(t *testing.T) {
	for _, tc := range []struct {
		scopes string
		status int
	}{
		{"agent:future", 403},
		{"admin", 403},
		{"agent:register agent:future", 200},
		{"agent:register admin", 200},
	} {
		t.Run(tc.scopes, func(t *testing.T) {
			f := newCredentialFixture()
			f.record.Scopes = tc.scopes
			_, h := credentialHTTP(t, f)
			response := credentialPost(t, h, "register", json.RawMessage(completeRegistration))
			if response.Code != tc.status {
				t.Fatalf("stored scopes changed authentication: %d %s", response.Code, response.Body.String())
			}
			if tc.status == 200 {
				response = credentialPost(t, h, "poll", protocol.RunnerPollRequest{ClientID: "node-a", AgentInstanceID: "process-a"})
				if response.Code != 403 {
					t.Fatal("known register plus extra/admin scope also granted poll")
				}
				body := registration(t, "node-a", "process-b")
				otherOwner := "other_user"
				body.Owner = &otherOwner
				response = credentialPost(t, h, "register", body)
				if response.Code != 403 {
					t.Fatal("extra/admin scope bypassed authenticated owner")
				}
			}
		})
	}
	t.Run("BOM does not grant poll", func(t *testing.T) {
		f := newCredentialFixture()
		r, h := credentialHTTP(t, f)
		if response := credentialPost(t, h, "register", json.RawMessage(completeRegistration)); response.Code != 200 {
			t.Fatal("register failed")
		}
		owner, _ := HostUserOwner(42)
		pending, err := r.Enqueue(Access{Username: owner}, input("bom-held"))
		if err != nil {
			t.Fatal(err)
		}
		defer pending.Cancel(nil)
		body := protocol.RunnerPollRequest{ClientID: "node-a", AgentInstanceID: "process-a"}
		for _, scopes := range []string{"agent:poll\ufeff", "\ufeffagent:poll", "agent:poll\ufeffagent:result"} {
			f.record.Scopes = scopes
			response := credentialPost(t, h, "poll", body)
			if response.Code != 403 {
				t.Fatalf("BOM scope did not authenticate then fail poll authorization: %d", response.Code)
			}
			view, err := r.View(Access{Username: owner}, "node-a")
			if err != nil || view.PendingRequests != 1 {
				t.Fatal("BOM scope consumed queued work")
			}
		}
		f.record.Scopes = "agent:poll"
		response := credentialPost(t, h, "poll", body)
		var polled protocol.RunnerPollResponse
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &polled) != nil || polled.Request == nil || polled.Request.RequestID != "bom-held" {
			t.Fatal("exact poll scope did not deliver retained request")
		}
	})
}

func TestAgentCredentialHTTPDoesNotBypassGenerationOrUseBodyAuthority(t *testing.T) {
	f := newCredentialFixture()
	r, h := credentialHTTP(t, f)
	partial := `{"client_id":"node-a","agent_instance_id":"process-a","agent_protocol_generation":2,"capabilities":{"shell":true}}`
	response := sendHTTP(h, "/api/shell/agent/register", credentialToken, partial)
	if response.Code != 400 {
		t.Fatalf("partial generation: %d", response.Code)
	}
	owner, _ := HostUserOwner(42)
	if _, err := r.View(Access{Username: owner}, "node-a"); err == nil {
		t.Fatal("partial generation created lease")
	}
	for _, header := range []string{"query", "x-api-key", "duplicate", "body"} {
		req := httptest.NewRequest(http.MethodPost, "/api/shell/agent/register", strings.NewReader(completeRegistration))
		switch header {
		case "query":
			req.URL.RawQuery = "token=abc"
		case "x-api-key":
			req.Header.Set("x-api-key", credentialToken)
		case "duplicate":
			req.Header.Add("Authorization", "Bearer abc")
			req.Header.Add("Authorization", "Bearer abc")
		case "body":
			req = httptest.NewRequest(http.MethodPost, "/api/shell/agent/register", strings.NewReader(`{"token":"abc","kind":"agent_token","owner":"sub2api_user_42","scopes":["agent:register"],"host_user_id":42}`))
		}
		before := len(f.hashes)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, req)
		if response.Code != 401 || len(f.hashes) != before {
			t.Fatalf("credential from %s used as authority", header)
		}
	}
	f.lookupErr = errors.New("private-repository-token-material")
	response = sendHTTP(h, "/api/shell/agent/register", credentialToken, completeRegistration)
	if response.Code != 401 || strings.Contains(response.Body.String(), "private-repository") {
		t.Fatal("repository error escaped generic authentication response")
	}
	f.lookupErr = nil
	for _, kind := range []string{"user", ""} {
		f.record.Kind = kind
		response = sendHTTP(h, "/api/shell/agent/register", credentialToken, completeRegistration)
		if response.Code != 403 {
			t.Fatalf("verified original user kind %q did not get transport 403: %d", kind, response.Code)
		}
	}
	f.record.Kind = "agent"
	f.record.Scopes = ""
	response = sendHTTP(h, "/api/shell/agent/register", credentialToken, completeRegistration)
	if response.Code != 403 {
		t.Fatal("empty source scopes received default registration scope")
	}
	p, err := f.authenticator(t)(context.Background(), credentialToken)
	if err != nil || p.ProjectGrantID != "" || p.SharedKeyHash != "" {
		t.Fatal("managed hash was projected into a group")
	}
}
