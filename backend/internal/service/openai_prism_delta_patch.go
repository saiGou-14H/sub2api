package service

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

var prismDiffHunk = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@(?:.*)$`)

// Apply only a complete unified diff against its exact current document. No
// fuzzy matching is permitted: a concurrent edit must not be silently replaced.
func prismApplyUnifiedDiff(base, patch, filePath string) (string, error) {
	// Captured Prism diffs use render/codex labels and omit newline markers.
	// Browser application preserves an existing document's EOL convention;
	// newly added text has LF separators and no implicit final newline.
	if strings.HasPrefix(patch, "--- render/") {
		prefix := "--- render/" + filePath + "\n+++ codex/" + filePath + "\n"
		if !strings.HasPrefix(patch, prefix) || strings.Contains(patch, `\ No newline at end of file`) {
			return "", errors.New("Prism file diff headers do not match the document")
		}
		crlf := strings.Contains(base, "\r\n")
		finalNewline := strings.HasSuffix(base, "\n")
		normalized := strings.ReplaceAll(base, "\r\n", "\n")
		if crlf && strings.Contains(strings.ReplaceAll(base, "\r\n", ""), "\n") {
			return "", errors.New("Prism file has mixed line endings")
		}
		if normalized != "" && !finalNewline {
			normalized += "\n"
		}
		canonical := "--- a/" + filePath + "\n+++ b/" + filePath + "\n" + strings.TrimPrefix(patch, prefix)
		if !strings.HasSuffix(canonical, "\n") {
			canonical += "\n"
		}
		result, err := prismApplyStrictUnifiedDiff(normalized, canonical, filePath)
		if err != nil {
			return "", err
		}
		if !finalNewline {
			result = strings.TrimSuffix(result, "\n")
		}
		if crlf {
			result = strings.ReplaceAll(result, "\n", "\r\n")
		}
		return result, nil
	}
	return prismApplyStrictUnifiedDiff(base, patch, filePath)
}

func prismApplyStrictUnifiedDiff(base, patch, filePath string) (string, error) {
	invalid := errors.New("Prism file diff is incomplete or does not match the current document")
	if len(base) > 8<<20 || len(patch) > 16<<20 || filePath == "" || strings.ContainsAny(filePath, "\x00\r\n") {
		return "", invalid
	}
	split := func(text string) []string {
		if text == "" {
			return nil
		}
		lines := strings.SplitAfter(text, "\n")
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		return lines
	}
	old := split(base)
	lines := split(patch)
	var result strings.Builder
	terminated := true
	writeLine := func(line string) bool {
		if !terminated || result.Len()+len(line) > 8<<20 {
			return false
		}
		result.WriteString(line)
		terminated = strings.HasSuffix(line, "\n")
		return true
	}
	position, outputLines, hunks := 0, 0, 0
	oldHeader, newHeader := false, false
	deletesFile := false
	for index := 0; index < len(lines); {
		line := strings.TrimSuffix(lines[index], "\n")
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			if hunks != 0 {
				return "", invalid // Multiple files must never be applied to one node.
			}
			name := line[4:]
			if strings.HasPrefix(line, "--- ") {
				if oldHeader || (name != "a/"+filePath && name != filePath && name != "/dev/null") || (name == "/dev/null" && base != "") {
					return "", invalid
				}
				oldHeader = true
			} else {
				if newHeader || (name != "b/"+filePath && name != filePath && name != "/dev/null") {
					return "", invalid
				}
				newHeader = true
				deletesFile = name == "/dev/null"
			}
			index++
			continue
		}
		match := prismDiffHunk.FindStringSubmatch(line)
		if match == nil {
			if hunks == 0 && (strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "index ") || strings.HasPrefix(line, "new file mode ") || strings.HasPrefix(line, "deleted file mode ")) {
				index++
				continue
			}
			return "", invalid
		}
		if !oldHeader || !newHeader {
			return "", invalid
		}
		parse := func(startText, countText string) (int, int, bool) {
			start, err := strconv.Atoi(startText)
			count := 1
			if err != nil {
				return 0, 0, false
			}
			if countText != "" {
				count, err = strconv.Atoi(countText)
			}
			if err != nil || start < 0 || count < 0 || (start == 0 && count != 0) {
				return 0, 0, false
			}
			// Zero-length ranges refer to the insertion point after start.
			if count != 0 {
				start--
			}
			return start, count, true
		}
		oldStart, oldCount, okOld := parse(match[1], match[2])
		newStart, newCount, okNew := parse(match[3], match[4])
		if !okOld || !okNew || oldStart < position || oldStart > len(old) || oldCount > len(old)-oldStart {
			return "", invalid
		}
		for position < oldStart {
			if !writeLine(old[position]) {
				return "", invalid
			}
			position++
			outputLines++
		}
		if outputLines != newStart {
			return "", invalid
		}
		index++
		consumed, produced := 0, 0
		for index < len(lines) && !strings.HasPrefix(lines[index], "@@ ") {
			part := lines[index]
			if len(part) == 0 || (part[0] != ' ' && part[0] != '+' && part[0] != '-') {
				return "", invalid
			}
			value := part[1:]
			index++
			if index < len(lines) && strings.TrimSuffix(lines[index], "\n") == `\ No newline at end of file` {
				value = strings.TrimSuffix(value, "\n")
				index++
			} else if !strings.HasSuffix(value, "\n") {
				return "", invalid
			}
			if part[0] != '+' {
				if consumed >= oldCount || position >= len(old) || old[position] != value {
					return "", invalid
				}
				position++
				consumed++
			}
			if part[0] != '-' {
				if produced >= newCount || !writeLine(value) {
					return "", invalid
				}
				produced++
				outputLines++
			}
		}
		if consumed != oldCount || produced != newCount {
			return "", invalid
		}
		hunks++
	}
	if hunks == 0 {
		return "", invalid
	}
	for _, tail := range old[position:] {
		if !writeLine(tail) {
			return "", invalid
		}
	}
	if deletesFile && result.Len() != 0 {
		return "", invalid
	}
	return result.String(), nil
}
