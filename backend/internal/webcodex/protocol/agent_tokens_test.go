// SPDX-License-Identifier: Apache-2.0
package protocol

import (
	"encoding/json"
	"testing"
)

func TestAgentTokenManagementJSON(t *testing.T) {
	for _, test := range []struct {
		name, body string
		create     bool
		valid      bool
	}{
		{"create-default", `{"username":"u","client_id":"n","ignored":true}`, true, true},
		{"create-null", `{"username":"u","client_id":"n","scopes":null}`, true, true},
		{"create-empty", `{"username":"u","client_id":"n","scopes":[]}`, true, true},
		{"create-element-null", `{"username":"u","client_id":"n","scopes":[null]}`, true, false},
		{"create-missing", `{"username":"u"}`, true, false},
		{"create-null-required", `{"username":null,"client_id":"n"}`, true, false},
		{"create-uppercase", `{"Username":"u","client_id":"n"}`, true, false},
		{"create-duplicate", `{"username":"u","username":"v","client_id":"n"}`, true, false},
		{"create-bad-unicode", `{"username":"u","client_id":"n","name":"\ud800"}`, true, false},
		{"create-wide-int", `{"username":"u","client_id":"n","expires_at":9223372036854775807}`, true, true},
		{"create-overflow", `{"username":"u","client_id":"n","expires_at":9223372036854775808}`, true, false},
		{"register-default", `{"username":"u","client_id":"n","token_hash":"h","token_prefix":"p"}`, false, true},
		{"register-plaintext", `{"username":"u","client_id":"n","token_hash":"h","token_prefix":"p","token":"private"}`, false, false},
		{"register-null", `{"username":"u","client_id":"n","token_hash":"h","token_prefix":"p","scopes":null}`, false, false},
		{"register-element-null", `{"username":"u","client_id":"n","token_hash":"h","token_prefix":"p","scopes":[null]}`, false, false},
		{"not-object", `[]`, false, false},
		{"trailing", `{"username":"u","client_id":"n"} {}`, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var err error
			if test.create {
				var body CreateAgentTokenRequest
				err = json.Unmarshal([]byte(test.body), &body)
			} else {
				var body RegisterAgentTokenHashRequest
				err = json.Unmarshal([]byte(test.body), &body)
			}
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v, error=%v", test.valid, err)
			}
		})
	}
	var omitted, null, empty CreateAgentTokenRequest
	_ = json.Unmarshal([]byte(`{"username":"u","client_id":"n"}`), &omitted)
	_ = json.Unmarshal([]byte(`{"username":"u","client_id":"n","scopes":null}`), &null)
	_ = json.Unmarshal([]byte(`{"username":"u","client_id":"n","scopes":[]}`), &empty)
	if omitted.Scopes != nil || null.Scopes != nil || empty.Scopes == nil || len(*empty.Scopes) != 0 {
		t.Fatal("lost original Option/empty distinction")
	}
}
