// SPDX-License-Identifier: Apache-2.0
// Tests for the WebCodex jobs.rs port, including tests/job_log_wait.rs legacy
// cursor behavior, revision 97ad66949a859174911c2f6da2ff1063be98bfa9.
package runner

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func jobLogTestString(s string) *string { return &s }
func jobLogTestUint(n uint64) *uint64   { return &n }

func TestJobLogNullAndEmpty(t *testing.T) {
	log := newJobLogState()
	if log.firstRetainedLine != 1 || log.nextLine != 1 {
		t.Fatalf("new state: %+v", log)
	}
	log.append(nil)
	log.replace(nil)
	if text, next, truncated := log.selectLines(nil, nil); text != "" || next != 1 || truncated {
		t.Fatalf("empty selection = %q, %d, %v", text, next, truncated)
	}
	log.append(jobLogTestString("hello\n"))
	before := log
	log.append(nil)
	log.replace(nil)
	log.append(jobLogTestString(""))
	if log != before {
		t.Fatal("null or empty append changed an existing state")
	}
	log.replace(jobLogTestString(""))
	if log != newJobLogState() {
		t.Fatalf("empty replacement did not clear state: %+v", log)
	}
}

func TestJobLogRustLines(t *testing.T) {
	cases := []struct {
		input, want string
		next        uint64
	}{
		{"", "", 1}, {"\n", "", 2}, {"\n\n", "\n\n", 3},
		{"x", "x\n", 2}, {"x\n", "x\n", 2},
		{"x\r\ny\r\n", "x\ny\n", 3},
		{"x\ry\r", "x\ry\r\n", 2}, {"\r\n", "", 2},
		{"x\r\r\n", "x\r\n", 2},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q", tc.input), func(t *testing.T) {
			log := newJobLogState()
			log.append(&tc.input)
			text, next, truncated := log.selectLines(nil, nil)
			if text != tc.want || next != tc.next || truncated {
				t.Fatalf("got %q, %d, %v; want %q, %d, false", text, next, truncated, tc.want, tc.next)
			}
		})
	}
}

func TestJobLogChunkSplitsAndFollow(t *testing.T) {
	log := newJobLogState()
	for i, chunk := range []string{"hel", "lo\r", "\n", "wor", "ld\n"} {
		log.append(&chunk)
		wantNext := uint64(2)
		if i >= 3 {
			wantNext = 3
		}
		if log.nextLine != wantNext {
			t.Fatalf("chunk %d next = %d, want %d", i, log.nextLine, wantNext)
		}
	}
	text, next, _ := log.selectLines(nil, nil)
	if text != "hello\nworld\n" || next != 3 {
		t.Fatalf("selection %q, %d", text, next)
	}
	if text, got, _ := log.selectLines(&next, nil); text != "" || got != next {
		t.Fatalf("follow duplicated %q", text)
	}
	log.append(jobLogTestString("third"))
	if text, got, _ := log.selectLines(&next, nil); text != "third\n" || got != 4 {
		t.Fatalf("follow %q, %d", text, got)
	}
	// A partial line already counts as a line; completing it does not advance
	// the cursor or replay it to a follower, matching the frozen source.
	cursor := log.nextLine
	log.append(jobLogTestString(" continued\n"))
	if text, got, _ := log.selectLines(&cursor, nil); text != "" || got != cursor {
		t.Fatalf("partial-line continuation %q, %d", text, got)
	}
}

func TestJobLogSelectionPrecedenceAndCursors(t *testing.T) {
	log := newJobLogState()
	log.append(jobLogTestString("a\nb\nc\n"))
	cases := []struct {
		name        string
		since, tail *uint64
		text        string
		truncated   bool
	}{
		{"all", nil, nil, "a\nb\nc\n", false},
		{"zero cursor", jobLogTestUint(0), nil, "a\nb\nc\n", true},
		{"middle", jobLogTestUint(2), nil, "b\nc\n", true},
		{"future", jobLogTestUint(^uint64(0)), nil, "", true},
		{"tail wins", jobLogTestUint(4), jobLogTestUint(2), "b\nc\n", true},
		{"zero tail", jobLogTestUint(2), jobLogTestUint(0), "b\nc\n", true},
		{"huge tail", jobLogTestUint(4), jobLogTestUint(^uint64(0)), "a\nb\nc\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, next, truncated := log.selectLines(tc.since, tc.tail)
			if text != tc.text || next != 4 || truncated != tc.truncated {
				t.Fatalf("got %q, %d, %v", text, next, truncated)
			}
		})
	}
	log.firstRetainedLine, log.nextLine = 20, 23
	if text, next, truncated := log.selectLines(jobLogTestUint(1), nil); text != "a\nb\nc\n" || next != 23 || !truncated {
		t.Fatal("old cursor not flagged")
	}
	if _, _, truncated := log.selectLines(nil, nil); truncated {
		t.Fatal("absent since alone must not flag retained offset")
	}
	if _, _, truncated := log.selectLines(nil, jobLogTestUint(9)); !truncated {
		t.Fatal("tail must flag retained offset")
	}
}

func TestJobLogBoundedWholeLines(t *testing.T) {
	log := newJobLogState()
	log.append(jobLogTestString(strings.Repeat("x\n", maxOutputBytes/2)))
	if len(log.tail) != maxOutputBytes || log.truncated {
		t.Fatal("exact bound truncated")
	}
	log.append(jobLogTestString("last\n"))
	if len(log.tail) != maxOutputBytes-1 || log.firstRetainedLine != 4 || log.nextLine != uint64(maxOutputBytes/2+2) || !log.truncated || !strings.HasSuffix(log.tail, "last\n") {
		t.Fatalf("bounded state: len=%d first=%d next=%d truncated=%v", len(log.tail), log.firstRetainedLine, log.nextLine, log.truncated)
	}
	cursor := log.nextLine
	if text, next, truncated := log.selectLines(&cursor, nil); text != "" || next != cursor || !truncated {
		t.Fatal("bounded follow did not drain")
	}
	log.append(jobLogTestString(strings.Repeat("z", maxOutputBytes+1) + "\n"))
	if log.tail != "" || log.firstRetainedLine != cursor+1 || log.nextLine != cursor+1 {
		t.Fatalf("fully dropped long line: first=%d next=%d", log.firstRetainedLine, log.nextLine)
	}
}

func TestJobLogUTF8LongLineAndSaturation(t *testing.T) {
	for _, ending := range []string{"", "\nend"} {
		log := newJobLogState()
		log.append(jobLogTestString(strings.Repeat("猫", maxOutputBytes/3+20) + ending))
		if !utf8.ValidString(log.tail) || len(log.tail) > maxOutputBytes || !log.truncated {
			t.Fatal("invalid UTF-8 or unbounded tail")
		}
		if ending == "" && (log.firstRetainedLine != 1 || log.nextLine != 2 || strings.HasPrefix(log.tail, "[")) {
			t.Fatal("long line must retain its cursor without transport marker")
		}
		if ending != "" && (log.tail != "end" || log.firstRetainedLine != 2 || log.nextLine != 3) {
			t.Fatal("UTF-8 newline trim failed")
		}
	}
	log := newJobLogState()
	log.firstRetainedLine = ^uint64(0) - 1
	log.append(jobLogTestString("a\nb\nc\n"))
	if log.nextLine != ^uint64(0) {
		t.Fatal("next cursor wrapped")
	}
	log.append(jobLogTestString(strings.Repeat("x\n", maxOutputBytes)))
	if log.firstRetainedLine != ^uint64(0) || log.nextLine != ^uint64(0) {
		t.Fatal("retained cursor wrapped")
	}
}

func TestJobLogLegacyReplaceMarkersAndReset(t *testing.T) {
	markers := []string{"[output truncated]\n", "[...]\n", "[output truncated to last 12000 bytes]\n", "[output truncated to last 000 bytes]\n", "[output truncated to last 999999999999999999999999 bytes]\n"}
	for _, marker := range markers {
		log := jobLogState{tail: "old", firstRetainedLine: 99, nextLine: 100, truncated: true}
		log.replace(jobLogTestString(marker + "retained\n"))
		if !log.truncated || log.firstRetainedLine != 1 || log.nextLine != 3 {
			t.Fatalf("marker %q: %+v", marker, log)
		}
	}
	for _, value := range []string{"ordinary\n", "ordinary\n[output truncated]\n", "[output truncated]", "[output truncated]\r\n", "[output truncated to last  bytes]\n", "[output truncated to last -1 bytes]\n", "[output truncated to last １ bytes]\n", "[output truncated to last 1 bytes]", "[output truncated to last 1 bytes]\r\n"} {
		log := newJobLogState()
		log.replace(&value)
		if log.truncated {
			t.Fatalf("false marker: %q", value)
		}
	}
	log := jobLogState{firstRetainedLine: 99, nextLine: 100, truncated: true}
	log.replace(jobLogTestString("legacy output\n"))
	before := log
	log.replace(jobLogTestString("legacy output\n"))
	if log != before || log.firstRetainedLine != 1 || log.nextLine != 2 || log.truncated {
		t.Fatalf("legacy replacement/no-op: %+v", log)
	}
	long := strings.Repeat("猫", maxOutputBytes/3+20)
	log.replace(&long)
	marker := fmt.Sprintf("[output truncated to last %d bytes]\n", maxOutputBytes)
	if !strings.HasPrefix(log.tail, marker) || !utf8.ValidString(log.tail) || len(log.tail) > maxOutputBytes+len(marker) || log.nextLine != 3 || !log.truncated {
		t.Fatal("legacy byte truncation/marker line count changed")
	}
}
