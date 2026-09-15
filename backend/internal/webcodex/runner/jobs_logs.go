// SPDX-License-Identifier: Apache-2.0
// Ported from WebCodex crates/webcodex-runner-registry/src/jobs.rs,
// revision 97ad66949a859174911c2f6da2ff1063be98bfa9 (append/replace/select log helpers).
package runner

import (
	"strings"
	"unicode/utf8"
)

// jobLogState retains an absolute line cursor for append-only updates. Legacy
// replacement resets that cursor; callers must initialize with newJobLogState.
// Synchronization belongs to the owning registry.
type jobLogState struct {
	tail              string
	firstRetainedLine uint64
	nextLine          uint64
	truncated         bool
}

func newJobLogState() jobLogState {
	return jobLogState{firstRetainedLine: 1, nextLine: 1}
}

func (l *jobLogState) append(chunk *string) {
	if chunk == nil {
		return
	}
	l.tail += *chunk
	if len(l.tail) > maxOutputBytes {
		observedNext := jobLogSaturatingAdd(l.firstRetainedLine, jobLogLineCount(l.tail))
		start := len(l.tail) - maxOutputBytes
		// Byte indexing also works when the minimum offset falls inside UTF-8.
		if newline := strings.IndexByte(l.tail[start:], '\n'); newline >= 0 {
			start += newline + 1
		} else {
			for start < len(l.tail) && !utf8.RuneStart(l.tail[start]) {
				start++
			}
		}
		dropped := uint64(strings.Count(l.tail[:start], "\n"))
		l.firstRetainedLine = jobLogSaturatingAdd(l.firstRetainedLine, dropped)
		// Clone so a short retained tail does not pin a large discarded chunk.
		l.tail = strings.Clone(l.tail[start:])
		if l.tail == "" {
			l.firstRetainedLine = observedNext
		}
		l.truncated = true
	}
	l.nextLine = jobLogSaturatingAdd(l.firstRetainedLine, jobLogLineCount(l.tail))
}

func (l *jobLogState) replace(tail *string) {
	if tail == nil {
		return
	}
	l.tail = *truncateOutput(tail)
	l.firstRetainedLine = 1
	l.nextLine = jobLogSaturatingAdd(1, jobLogLineCount(l.tail))
	l.truncated = jobLogHasTransportMarker(l.tail)
}

func (l *jobLogState) selectLines(since, tailLines *uint64) (text string, next uint64, truncated bool) {
	lines := jobLogLines(l.tail)
	start := uint64(0)
	if tailLines != nil && *tailLines > 0 {
		if *tailLines < uint64(len(lines)) {
			start = uint64(len(lines)) - *tailLines
		}
		truncated = l.firstRetainedLine > 1 || start > 0 || l.truncated
	} else {
		if since != nil && *since > l.firstRetainedLine {
			start = *since - l.firstRetainedLine
			if start > uint64(len(lines)) {
				start = uint64(len(lines))
			}
		}
		truncated = (since != nil && *since < l.firstRetainedLine) || start > 0 || l.truncated
	}
	text = strings.Join(lines[start:], "\n")
	// Rust joins first: a single empty line therefore returns empty text.
	if text != "" {
		text += "\n"
	}
	return text, l.nextLine, truncated
}

func jobLogSaturatingAdd(a, b uint64) uint64 {
	if ^uint64(0)-a < b {
		return ^uint64(0)
	}
	return a + b
}

func jobLogLineCount(value string) uint64 {
	count := uint64(strings.Count(value, "\n"))
	if value != "" && !strings.HasSuffix(value, "\n") {
		count++
	}
	return count
}

// Match Rust str::lines: no extra line after a final LF, strip CR only as
// part of CRLF, and retain a standalone final CR.
func jobLogLines(value string) []string {
	var lines []string
	for value != "" {
		newline := strings.IndexByte(value, '\n')
		if newline < 0 {
			lines = append(lines, value)
			break
		}
		lines = append(lines, strings.TrimSuffix(value[:newline], "\r"))
		value = value[newline+1:]
	}
	return lines
}

func jobLogHasTransportMarker(value string) bool {
	if strings.HasPrefix(value, "[output truncated]\n") || strings.HasPrefix(value, "[...]\n") {
		return true
	}
	rest, ok := strings.CutPrefix(value, "[output truncated to last ")
	if !ok {
		return false
	}
	newline := strings.IndexByte(rest, '\n')
	if newline < 0 {
		return false
	}
	count, ok := strings.CutSuffix(rest[:newline], " bytes]")
	if !ok || count == "" {
		return false
	}
	for i := range count {
		if count[i] < '0' || count[i] > '9' {
			return false
		}
	}
	return true
}
