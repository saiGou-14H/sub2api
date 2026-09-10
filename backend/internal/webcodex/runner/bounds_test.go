// SPDX-License-Identifier: Apache-2.0
package runner

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestResultRetainsOriginalBoundedUTF8Tail(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	p, err := r.Enqueue(Access{Username: "alice"}, input("large-output"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = poll(r, "node-a", "process-a"); err != nil {
		t.Fatal(err)
	}
	body := result("node-a", "process-a", "large-output")
	original := strings.Repeat("猫", maxOutputBytes/3+17) + "tail"
	body.Stdout = &original
	if err = r.Complete(testPrincipal("node-a"), body); err != nil {
		t.Fatal(err)
	}
	got := wait(t, p)
	if got.Err != nil || got.Result.Stdout == nil {
		t.Fatalf("missing outcome: %+v", got)
	}
	output := *got.Result.Stdout
	const notice = "[output truncated to last 262144 bytes]\n"
	if !strings.HasPrefix(output, notice) || !strings.HasSuffix(output, "tail") || !utf8.ValidString(output) || len(output) > len(notice)+maxOutputBytes {
		t.Fatal("output tail, UTF-8, or original byte bound changed")
	}
	if *body.Stdout != original {
		t.Fatal("caller result was mutated")
	}
}

func TestRegistryCapacityAndCancelledQueueAreBounded(t *testing.T) {
	r, err := NewRegistry(Options{MaxRunners: 1, MaxPendingPerRunner: 1, OnlineWindow: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	register(t, r, "node-a", "process-a")
	if _, err = r.Register(testPrincipal("node-b"), registration(t, "node-b", "process-b")); !errors.Is(err, ErrCapacity) {
		t.Fatalf("node limit: %v", err)
	}
	for i := 0; i < 100; i++ {
		p, err := r.Enqueue(Access{Username: "alice"}, input("cancelled"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = r.Enqueue(Access{Username: "alice"}, input("overflow")); !errors.Is(err, ErrCapacity) {
			t.Fatalf("pending limit: %v", err)
		}
		p.Cancel(nil)
	}
	r.mu.Lock()
	retained := len(r.nodes["node-a"].queue) + len(r.pending)
	r.mu.Unlock()
	if retained != 0 {
		t.Fatalf("cancelled requests retained: %d", retained)
	}
	if request, err := poll(r, "node-a", "process-a"); err != nil || request != nil {
		t.Fatalf("cancelled work delivered: %+v %v", request, err)
	}
}
