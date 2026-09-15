// SPDX-License-Identifier: Apache-2.0
package runner

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestJobsCapacityIsPerRunnerAndOnlyExpiresTerminalRecords(t *testing.T) {
	r := jobsRegistry(t, 2)
	register(t, r, "node-b", "process-b")
	start := func(client, id string) error {
		invocation := jobInvocation(id)
		invocation.Metadata.ClientID = client
		_, err := r.StartProcessJob(Access{Username: "alice"}, invocation)
		return err
	}
	for _, id := range []string{"a1", "a2"} {
		if err := start("node-a", id); err != nil {
			t.Fatal(err)
		}
	}
	// A full Runner has no right to consume another Runner's retention budget.
	for _, id := range []string{"b1", "b2"} {
		if err := start("node-b", id); err != nil {
			t.Fatal("other Runner capacity", err)
		}
	}
	if len(r.jobs) != 4 {
		t.Fatal("per-runner budget became global")
	}
	for _, client := range []string{"node-a", "node-b"} {
		if err := start(client, client+"-overflow"); !errors.Is(err, ErrCapacity) {
			t.Fatal("overflow admitted", err)
		}
	}
	// Age is not an eviction policy for an active queued or dispatched Job.
	jobPoll(t, r)
	r.jobs["job-a1"].info.CreatedAt = 1
	r.jobs["job-a2"].info.CreatedAt = 1
	beforeFirst := getJob(t, r, "a1")
	beforeSecond := getJob(t, r, "a2")
	if err := start("node-a", "must-not-evict"); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeFirst, getJob(t, r, "a1")) || !reflect.DeepEqual(beforeSecond, getJob(t, r, "a2")) {
		t.Fatal("capacity evicted active record")
	}
	if r.requestToJob["request-a1"] != "job-a1" || r.requestToJob["request-a2"] != "job-a2" {
		t.Fatal("capacity discarded active binding")
	}
	// A terminal Job still owns capacity until its Server observation TTL expires.
	if _, err := r.StopJob(Access{Username: "alice"}, "job-a2", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := start("node-a", "too-early"); !errors.Is(err, ErrCapacity) {
		t.Fatal("fresh terminal was discarded", err)
	}
	r.jobs["job-a2"].observedTerminal = jobPtr(time.Now().Add(-jobTerminalRetention - time.Second))
	// No read/sweep before this admission: Start must reclaim expired records first.
	if err := start("node-a", "replacement"); err != nil {
		t.Fatal("expired terminal blocked admission", err)
	}
	if r.jobs["job-a2"] != nil || r.requestToJob["request-a2"] != "" || r.pending["request-a2"] != nil {
		t.Fatal("expired record or request mapping retained")
	}
	if len(r.jobs) != 4 || !reflect.DeepEqual(beforeFirst, getJob(t, r, "a1")) {
		t.Fatal("terminal sweep evicted active record")
	}
	for _, id := range []string{"job-b1", "job-b2"} {
		if r.jobs[id] == nil {
			t.Fatal("other Runner's active Job removed")
		}
	}
	if err := start("node-b", "still-full"); !errors.Is(err, ErrCapacity) {
		t.Fatal("another Runner's expiry changed local budget", err)
	}
}

func TestJobsZeroAndNegativeLibraryCapacityAreExplicit(t *testing.T) {
	r := testRegistry(t)
	register(t, r, "node-a", "process-a")
	if _, err := r.StartProcessJob(Access{Username: "alice"}, jobInvocation("disabled")); err == nil {
		t.Fatal("unconfigured Job capacity admitted dispatch")
	}
	options := r.options
	options.MaxJobsPerRunner = -1
	if _, err := NewRegistry(options); err == nil {
		t.Fatal("negative Job capacity accepted")
	}
}
