// SPDX-License-Identifier: Apache-2.0
package runner

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/webcodex/protocol"
)

func TestReconciliationExactSourceJSONBudget(t *testing.T) {
	for _, alphabet := range []string{"<>&", "\u2028\u2029", `\u2028\u2029\u003c`} {
		t.Run(fmt.Sprintf("%x", alphabet), func(t *testing.T) {
			r, reg := reconciliationRegistry(t)
			base := reconciliationStart(t, r, "a")
			// Sixteen 64KiB streams would exceed the budget once the full typed
			// context/metadata is included. Fifteen full streams plus a final filler
			// reach the exact source-serialized boundary; whitespace is irrelevant.
			items := make([]protocol.ShellJobSnapshot, 16)
			for i := range items {
				items[i], _ = copyJSON(base)
				items[i].JobID = fmt.Sprintf("budget-job-%02d", i)
				items[i].RequestID = fmt.Sprintf("budget-request-%02d", i)
				if i < 15 {
					items[i].Stdout.Tail = strings.Repeat(alphabet, 65536/len(alphabet))
					items[i].Stdout.NextLine = 2
				}
			}
			// Canonical serde_json string bytes: literal non-ASCII/HTML; actual
			// backslashes and quotes are escaped. This fixture has no controls.
			sourceStringBytes := func(s string) int { return len(s) + strings.Count(s, `\`) + strings.Count(s, `"`) }
			empty := items
			for i := range empty {
				empty[i].Stdout.Tail = ""
				empty[i].Stdout.NextLine = 2
			}
			emptyWire, err := json.Marshal(protocol.ShellJobInventory{ActiveComplete: true, Jobs: empty})
			if err != nil {
				t.Fatal(err)
			}
			// empty aliases items intentionally above; fill them after measuring all
			// metadata, including the numeric cursor width, at the source boundary.
			size := len(emptyWire)
			for i := 0; i < 15; i++ {
				items[i].Stdout.Tail = strings.Repeat(alphabet, 32000/len(alphabet))
				size += sourceStringBytes(items[i].Stdout.Tail)
			}
			// Use both streams to fit the remainder within the 64KiB per-stream bound.
			for i := 0; i < 16 && size < jobInventoryMaxBytes; i++ {
				remaining := jobInventoryMaxBytes - size
				if remaining > 65536 {
					remaining = 65536
				}
				items[i].Stderr.Tail = strings.Repeat("x", remaining)
				items[i].Stderr.NextLine = 2
				size += remaining
			}
			if size != jobInventoryMaxBytes {
				t.Fatal("fixture cannot reach budget")
			}
			items[15].Stdout.NextLine = 1
			setInventory(t, &reg, items)
			if _, err := r.registrationInventory(reg); err != nil {
				t.Fatalf("exact source boundary refused: %v", err)
			}
			// All stdout tails still have room. One byte of metadata also counts.
			items[15].Stdout.Tail = "x"
			items[15].Stdout.NextLine = 2
			setInventory(t, &reg, items)
			if _, err := r.registrationInventory(reg); err == nil {
				t.Fatal("one byte over source budget accepted")
			}
		})
	}
}
func TestReconciliationLateUpdateCannotEscapeDeadline(t *testing.T) {
	for _, kind := range []string{"owner", "instance", "invalid snapshot", "valid"} {
		t.Run(kind, func(t *testing.T) {
			r, _ := reconciliationRegistry(t)
			s := reconciliationStart(t, r, "a")
			if err := r.Offline(jobPrincipal(), protocol.RunnerOfflineRequest{ClientID: "node-a", AgentInstanceID: "process-a"}); err != nil {
				t.Fatal(err)
			}
			r.mu.Lock()
			j := r.jobs[s.JobID]
			j.stopRecoveryTimer()
			j.recoveringSince = jobPtr(time.Now().Add(-2 * time.Minute))
			before, _ := copyJSON(j.info)
			seen := r.nodes["node-a"].lastSeen
			r.mu.Unlock()
			u := sequencedUpdate("a", "running", 2)
			u.LogSnapshot = &protocol.ShellJobLogSnapshot{Stdout: s.Stdout, Stderr: s.Stderr}
			p := jobPrincipal()
			switch kind {
			case "owner":
				p.Username = "mallory"
			case "instance":
				u.AgentInstanceID = "old"
			case "invalid snapshot":
				u.LogSnapshot.Stdout.NextLine = 0
			}
			got, err := r.UpdateJob(p, u)
			if kind == "valid" {
				if err != nil || got.Status != "lost" || *got.RecoveryReasonCode != "runner_recovery_deadline_exceeded" {
					t.Fatalf("late snapshot escaped: %+v %v", got, err)
				}
			} else {
				if err == nil || !reflect.DeepEqual(j.info, before) || seen != r.nodes["node-a"].lastSeen {
					t.Fatal("invalid request triggered deadline/liveness mutation")
				}
			}
		})
	}
}
func TestReconciliationOnlineSnapshotRestoresStop(t *testing.T) {
	r, reg := reconciliationRegistry(t)
	s := reconciliationStart(t, r, "a")
	if _, err := r.StopJob(Access{Username: "alice"}, s.JobID, "alice"); err != nil {
		t.Fatal(err)
	}
	jobPoll(t, r) // stop was delivered; no queued duplicate remains
	setInventory(t, &reg, []protocol.ShellJobSnapshot{s})
	if _, err := r.Register(jobPrincipal(), reg); err != nil {
		t.Fatal(err)
	}
	if _, err := r.StopJob(Access{Username: "alice"}, s.JobID, "alice"); err != nil {
		t.Fatal(err)
	}
	if len(r.pending) != 1 {
		t.Fatal("online authoritative running snapshot left permanent stop latch")
	}
}
