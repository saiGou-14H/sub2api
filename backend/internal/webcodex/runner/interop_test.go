// SPDX-License-Identifier: Apache-2.0
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

// This test-only source launcher is explicit and optional: ordinary backend
// tests require neither Node nor a neighboring DSH checkout. No app profile or
// external credential is loaded. DSH_RUNNER_CHECKOUT names the reviewed checkout.
func runDSH(t *testing.T, mode string, input any) []byte {
	t.Helper()
	root := os.Getenv("DSH_RUNNER_CHECKOUT")
	if root == "" {
		t.Skip("set DSH_RUNNER_CHECKOUT for cross-language Runner tests")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--import", "tsx/esm", "packages/runner/runner/tests/interop.ts", mode)
	cmd.Dir = root
	cmd.Stdin = bytes.NewReader(wire)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "NODE_NO_WARNINGS=1"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("DSH interop %s: %v; %s", mode, err, stderr.String())
	}
	return output
}

func TestDSHRunnerWireRoundTrip(t *testing.T) {
	fixtures := []json.RawMessage{
		[]byte(`{"request_id":"r-default","client_id":"node-a","command":"printf 猫","timeout_secs":18446744073709551615,"requested_by":"alice","created_at":-9223372036854775808}`),
		[]byte(`{"request_id":"r-process","client_id":"node-a","kind":"run_process","command":"","process":{"executable":"printf","args":["a b","$(do-not-run)","猫",""]},"stdin":"a\nb","timeout_secs":30,"requested_by":"alice","created_at":9223372036854775807}`),
		[]byte(`{"request_id":"r-script","client_id":"node-a","kind":"run_script","command":"","script":{"language":"bash","script":"printf '%s' \"$1\"","args":["猫"]},"timeout_secs":30,"requested_by":"alice","created_at":1}`),
		[]byte(`{"request_id":"r-read","client_id":"node-a","kind":"file_read","command":"","path":"notes.txt","start_line":1,"end_line":3,"max_bytes":4096,"timeout_secs":30,"requested_by":"alice","created_at":1}`),
		[]byte(`{"request_id":"r-write","client_id":"node-a","kind":"file_write","command":"","path":"new.txt","content":"猫\n","create_dirs":true,"timeout_secs":30,"requested_by":"alice","created_at":1}`),
		[]byte(`{"request_id":"r-list","client_id":"node-a","kind":"file_list","command":"","path":".","timeout_secs":30,"requested_by":"alice","created_at":1}`),
	}
	output := runDSH(t, "codec", fixtures)
	var results []struct {
		Wire string `json:"wire"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(output, &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != len(fixtures) {
		t.Fatalf("got %d fixtures", len(results))
	}
	for i, fixture := range fixtures {
		original, err := protocol.ReadRequest(fixture)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := protocol.ReadRequest([]byte(results[i].Wire))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(original, decoded) {
			t.Fatalf("fixture %d changed across Go/TS JSON: original=%s returned=%s", i, fixture, results[i].Wire)
		}
		invocation, err := decoded.DecodeInvocation()
		if err != nil {
			t.Fatal(err)
		}
		if invocation.Operation.WireKind() != results[i].Kind {
			t.Fatalf("fixture %d dispatch kind changed", i)
		}
	}
}

func TestDSHPartialGenerationCannotExecuteThroughGoRegistry(t *testing.T) {
	r := testRegistry(t)
	server := httptest.NewServer(testHTTP(t, r, 8192))
	defer server.Close()
	config := map[string]any{"serverUrl": server.URL, "token": "fixture-agent", "clientId": "node-a", "httpTimeoutMs": 2000, "retryDelayMs": 10, "reconnectAttempts": 1, "resultAttempts": 1}
	output := runDSH(t, "rejection", config)
	var result struct {
		Rejected bool   `json:"rejected"`
		Executed int    `json:"executed"`
		State    string `json:"state"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Rejected || result.Executed != 0 || result.State != "failed" || !strings.Contains(result.Error, "HTTP 400") {
		t.Fatalf("partial generation accepted or executed: %s", output)
	}
	if _, err := r.View(Access{Username: "alice"}, "node-a"); err == nil {
		t.Fatal("rejected Runner acquired a lease")
	}
}
