package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIPrismStrictDeltaPatch(t *testing.T) {
	for _, tc := range []struct{ name, base, patch, result string }{
		{"Prism add", "", "--- render/main.tex\n+++ codex/main.tex\n@@ -0,0 +1,2 @@\n+one\n+two", "one\ntwo"},
		{"Prism existing CRLF", "one\r\ntwo\r\n", "--- render/main.tex\n+++ codex/main.tex\n@@ -1,2 +1,2 @@\n-one\n-two\n+first\n+last", "first\r\nlast\r\n"},
		{"Prism existing no final LF", "one\ntwo", "--- render/main.tex\n+++ codex/main.tex\n@@ -1,2 +1,2 @@\n-one\n-two\n+first\n+last", "first\nlast"},
		{"add", "", "--- /dev/null\n+++ b/main.tex\n@@ -0,0 +1,2 @@\n+one\n+two\n", "one\ntwo\n"},
		{"replace", "one\ntwo\nthree\n", "--- a/main.tex\n+++ b/main.tex\n@@ -1,3 +1,3 @@\n one\n-two\n+changed\n three\n", "one\nchanged\nthree\n"},
		{"two hunks", "one\ntwo\nthree\nfour\n", "--- a/main.tex\n+++ b/main.tex\n@@ -1 +1 @@\n-one\n+first\n@@ -4 +4 @@\n-four\n+last\n", "first\ntwo\nthree\nlast\n"},
		{"append", "one\n", "--- a/main.tex\n+++ b/main.tex\n@@ -1,0 +2 @@\n+two\n", "one\ntwo\n"},
		{"delete", "one\n", "--- a/main.tex\n+++ /dev/null\n@@ -1 +0,0 @@\n-one\n", ""},
		{"no newline", "old", "--- a/main.tex\n+++ b/main.tex\n@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n\\ No newline at end of file\n", "new"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := prismApplyUnifiedDiff(tc.base, tc.patch, "main.tex")
			require.NoError(t, err)
			require.Equal(t, tc.result, result)
		})
	}
	for _, patch := range []string{
		"--- a/main.tex\n+++ b/main.tex\n@@ -1 +1 @@\n-old\n+new\n", // baseline mismatch
		"--- a/other.tex\n+++ b/other.tex\n@@ -1 +1 @@\n-one\n+new\n",
		"--- a/main.tex\n+++ b/main.tex\n@@ -1,2 +1 @@\n-one\n+new\n",                          // incomplete count
		"--- a/main.tex\n+++ b/main.tex\n@@ -1 +2 @@\n-one\n+new\n",                            // displaced output
		"--- a/main.tex\n+++ b/main.tex\n@@ -1 +1 @@\n-one\n+new",                              // missing newline marker
		"--- a/main.tex\n+++ b/main.tex\n@@ -1 +1 @@\n-one\n+new\n@@ -1 +1 @@\n-one\n+again\n", // overlap
	} {
		_, err := prismApplyUnifiedDiff("one\n", patch, "main.tex")
		require.Error(t, err)
	}
}

func TestOpenAIPrismStrictDeltaPatchRejectsIncompleteFileDeletion(t *testing.T) {
	for _, patch := range []string{
		"--- a/main.tex\n+++ /dev/null\n@@ -1 +0,0 @@\n-one\n",
		"--- a/main.tex\n+++ /dev/null\n@@ -1,2 +1 @@\n-one\n-two\n+replacement\n",
	} {
		_, err := prismApplyUnifiedDiff("one\ntwo\n", patch, "main.tex")
		require.Error(t, err)
	}
}
