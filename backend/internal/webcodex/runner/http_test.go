// SPDX-License-Identifier: Apache-2.0
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

func testHTTP(t *testing.T, r *Registry, limit int64) *HTTPHandler {
	t.Helper()
	handler, err := NewHTTPHandler(r, func(_ context.Context, token string) (Principal, error) {
		switch token {
		case "fixture-agent":
			return testPrincipal("node-a"), nil
		case "fixture-model-key":
			return Principal{Kind: "api_key", Username: "alice", Scopes: []string{ScopeRegister, ScopePoll, ScopeResult}}, nil
		case "fixture-no-result":
			p := testPrincipal("node-a")
			p.Scopes = []string{ScopeRegister, ScopePoll}
			return p, nil
		default:
			return Principal{}, errors.New("credential failure must not be echoed")
		}
	}, limit)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}
func sendHTTP(handler http.Handler, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func TestHTTPRequiresExplicitHostAuthentication(t *testing.T) {
	registry := testRegistry(t)
	if _, err := NewHTTPHandler(registry, nil, 100); err == nil {
		t.Fatal("missing authenticator accepted")
	}
	if _, err := NewHTTPHandler(registry, func(context.Context, string) (Principal, error) { return Principal{}, nil }, 0); err == nil {
		t.Fatal("unbounded body accepted")
	}
	handler := testHTTP(t, registry, 8192)
	for _, token := range []string{"", "invalid"} {
		response := sendHTTP(handler, "/api/shell/agent/register", token, completeRegistration)
		if response.Code != 401 || strings.Contains(response.Body.String(), "credential failure") {
			t.Fatalf("unauthenticated: %d %s", response.Code, response.Body.String())
		}
	}
	response := sendHTTP(handler, "/api/shell/agent/register?token=fixture-agent", "", completeRegistration)
	if response.Code != 401 {
		t.Fatal("query token was accepted")
	}
	response = sendHTTP(handler, "/api/shell/agent/register", "fixture-model-key", completeRegistration)
	if response.Code != 403 {
		t.Fatalf("model API key admitted: %d", response.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/shell/agent/register", strings.NewReader(completeRegistration))
	req.Header.Add("Authorization", "Bearer fixture-agent")
	req.Header.Add("Authorization", "Bearer fixture-agent")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != 401 {
		t.Fatal("ambiguous credentials accepted")
	}
}

func TestHTTPRejectsPartialGenerationAndMalformedPayloads(t *testing.T) {
	handler := testHTTP(t, testRegistry(t), 8192)
	for _, payload := range []string{
		`{"client_id":"node-a","agent_instance_id":"process-a","agent_protocol_generation":2,"capabilities":{"shell":true}}`,
		strings.Replace(completeRegistration, `"shell":true,`, "", 1),
		strings.Replace(completeRegistration, `"agent_protocol_generation":2`, `"agent_protocol_generation":1`, 1),
		strings.Replace(completeRegistration, `"client_id":"node-a"`, `"client_id":null`, 1),
		completeRegistration + `{}`,
		`{"client_id":"node-a","client_id":"node-b"}`,
	} {
		response := sendHTTP(handler, "/api/shell/agent/register", "fixture-agent", payload)
		if response.Code != 400 {
			t.Fatalf("invalid registration accepted: %d %s", response.Code, response.Body.String())
		}
	}
	response := sendHTTP(handler, "/api/shell/agent/register", "fixture-agent", completeRegistration)
	if response.Code != 200 {
		t.Fatalf("full fixture rejected: %s", response.Body.String())
	}
	for _, path := range []string{"/api/shell/agent/poll", "/api/shell/agent/offline", "/api/shell/agent/result"} {
		response = sendHTTP(handler, path, "fixture-agent", `{"client_id":"node-a"}`)
		if response.Code != 400 {
			t.Fatalf("missing instance at %s: %d", path, response.Code)
		}
	}
}

func TestHTTPBodyLimitCountsWholeUTF8Payload(t *testing.T) {
	payload := completeRegistration
	handler := testHTTP(t, testRegistry(t), int64(len(payload)))
	if response := sendHTTP(handler, "/api/shell/agent/register", "fixture-agent", payload); response.Code != 200 {
		t.Fatalf("exact limit: %s", response.Body.String())
	}
	for _, extra := range []string{" ", "猫"} {
		response := sendHTTP(handler, "/api/shell/agent/register", "fixture-agent", payload+extra)
		if response.Code != 413 {
			t.Fatalf("oversized body: %d", response.Code)
		}
	}
}

func TestHTTPPollingExchangeOnRealLoopback(t *testing.T) {
	r := testRegistry(t)
	server := httptest.NewServer(testHTTP(t, r, 8192))
	defer server.Close()
	client := &http.Client{Timeout: time.Second}
	post := func(path string, body any) (int, []byte) {
		t.Helper()
		wire, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, server.URL+"/api/shell/agent/"+path, bytes.NewReader(wire))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer fixture-agent")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("private transport response may be cached")
		}
		return response.StatusCode, data
	}
	if status, body := post("register", json.RawMessage(completeRegistration)); status != 200 {
		t.Fatalf("register %d %s", status, body)
	}
	pending, err := r.Enqueue(Access{Username: "alice"}, input("loopback"))
	if err != nil {
		t.Fatal(err)
	}
	status, body := post("poll", protocol.RunnerPollRequest{ClientID: "node-a", AgentInstanceID: "process-a"})
	if status != 200 {
		t.Fatalf("poll %d %s", status, body)
	}
	var response protocol.RunnerPollResponse
	if err = json.Unmarshal(body, &response); err != nil || response.Request == nil || response.Request.RequestID != "loopback" {
		t.Fatalf("wire request: %s %v", body, err)
	}
	if status, body = post("result", result("node-a", "process-a", "loopback")); status != 200 {
		t.Fatalf("result %d %s", status, body)
	}
	if got := wait(t, pending); got.Err != nil || got.Result == nil || !got.Dispatched {
		t.Fatalf("exchange %+v", got)
	}
	if status, body = post("result", result("node-a", "process-a", "loopback")); status != 400 {
		t.Fatalf("duplicate %d %s", status, body)
	}
	if status, body = post("offline", protocol.RunnerOfflineRequest{ClientID: "node-a", AgentInstanceID: "process-a"}); status != 200 {
		t.Fatalf("offline %d %s", status, body)
	}
	view, err := r.View(Access{Username: "alice"}, "node-a")
	if err != nil || view.Connected {
		t.Fatalf("offline view: %+v %v", view, err)
	}
}

func TestHTTPMissingResultScopeDoesNotConsumeWaiter(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	p, _ := r.Enqueue(Access{Username: "alice"}, input("scoped"))
	if _, err := poll(r, "node-a", "process-a"); err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(result("node-a", "process-a", "scoped"))
	if err != nil {
		t.Fatal(err)
	}
	handler := testHTTP(t, r, 8192)
	response := sendHTTP(handler, "/api/shell/agent/result", "fixture-no-result", string(wire))
	if response.Code != 403 {
		t.Fatalf("missing scope: %d", response.Code)
	}
	response = sendHTTP(handler, "/api/shell/agent/result", "fixture-agent", string(wire))
	if response.Code != 200 {
		t.Fatalf("valid result rejected: %s", response.Body.String())
	}
	if got := wait(t, p); got.Err != nil {
		t.Fatal(got.Err)
	}
}
