// SPDX-License-Identifier: Apache-2.0
// Semantic boundary tests for WebCodex audit_preview.rs process_preview and
// sensitive_text.rs secret_like_value, revision
// 97ad66949a859174911c2f6da2ff1063be98bfa9. All credential-like data is fabricated.
package runner

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestJobProcessPreviewArguments(t *testing.T) {
	for _, test := range []struct {
		name, executable string
		args             []string
		want             string
	}{
		{"empty executable", "", nil, `""`},
		{"simple", "git", []string{"status", "--short", "a_b-c.d/e\\f"}, `git status --short a_b-c.d/e\f`},
		{"boundaries", "tool", []string{"", "two words", "$(literal)", "a", "b"}, `tool "" "two words" "$(literal)" a b`},
		{"executable quoting", "two words", []string{"x"}, `"two words" x`},
		{"quote escaping", "tool", []string{`a"b\c`, `a\b`}, `tool "a\"b\\c" a\b`},
		{"unicode alphanumeric", "工具", []string{"é９Ⅳ²", "\u0345", "\u05B0"}, "工具 é９Ⅳ² \u0345 \u05B0"},
		{"combining non alphabetic", "tool", []string{"e\u0301"}, "tool \"e\u0301\""},
		{"controls", "tool", []string{"a\x00\t\n\r\x7f\u0085b"}, "tool \"a������b\""},
		{"format and separator not controls", "tool", []string{"a\u200Db\u2028c"}, "tool \"a\u200Db\u2028c\""},
		{"emoji scalar", "tool", []string{"😀"}, "tool \"😀\""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := jobProcessPreview(test.executable, test.args); got != test.want {
				t.Fatalf("preview = %q, want %q", got, test.want)
			}
		})
	}
}

func TestJobProcessPreviewScalarLimit(t *testing.T) {
	for _, test := range []struct {
		name, executable string
		args             []string
		want             string
	}{
		{"exact ascii", strings.Repeat("x", 120), nil, strings.Repeat("x", 120)},
		{"exact unicode", strings.Repeat("界", 120), nil, strings.Repeat("界", 120)},
		{"unicode overflow", strings.Repeat("界", 121), nil, strings.Repeat("界", 120) + "…"},
		{"separator overflow", strings.Repeat("x", 120), []string{"a"}, strings.Repeat("x", 120) + "…"},
		{"opening quote overflow", strings.Repeat("x", 119), []string{""}, strings.Repeat("x", 119) + " …"},
		{"closing quote exact", "", []string{strings.Repeat("😀", 115)}, `"" "` + strings.Repeat("😀", 115) + `"`},
		{"closing quote overflow", "", []string{strings.Repeat("😀", 116)}, `"" "` + strings.Repeat("😀", 116) + "…"},
		{"partial escape", strings.Repeat("x", 117) + `"`, nil, `"` + strings.Repeat("x", 117) + `\"` + "…"},
		{"escape split", strings.Repeat("x", 118) + `"`, nil, `"` + strings.Repeat("x", 118) + `\` + "…"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := jobProcessPreview(test.executable, test.args)
			if got != test.want {
				t.Fatalf("preview = %q, want %q", got, test.want)
			}
			if !utf8.ValidString(got) || utf8.RuneCountInString(got) > 121 {
				t.Fatal("preview violates Unicode scalar bound")
			}
		})
	}
}

func TestJobProcessPreviewSecretVocabulary(t *testing.T) {
	patterns := []string{
		"-----BEGIN FABRICATED KEY-----", "Bearer fabricated", "api_key=fabricated",
		"token=fabricated", "id_rsa", "id_ed25519", "wc_pat_fabricated",
		"wc_agent_fabricated", "wc_acct_fabricated", "wc_oat_fabricated",
		"wc_ort_fabricated", "wc_csec_fabricated", "wc_pair_fabricated", "wc_boot_fabricated",
	}
	for i, pattern := range patterns {
		for _, value := range []string{pattern, strings.ToUpper(pattern)} {
			if jobProcessPreview("tool", []string{value}) != "[redacted]" {
				t.Errorf("fabricated pattern %d was not redacted in argv", i)
			}
			if jobProcessPreview(value, nil) != "[redacted]" {
				t.Errorf("fabricated pattern %d was not redacted in executable", i)
			}
		}
	}
	for _, test := range []struct {
		name, value, want string
	}{
		{"URL credentials unchanged by source", "https://demo:invented@example.invalid/path", `tool "https://demo:invented@example.invalid/path"`},
		{"URL matching vocabulary", "https://demo:wc_pat_fabricated@example.invalid", "[redacted]"},
		{"query token", "https://example.invalid/?token=fabricated", "[redacted]"},
		{"source false positives", "notapi_keyname", "[redacted]"},
		{"ASCII folding only", "API_KEY=fabricated", `tool "API_KEY=fabricated"`},
		{"not in source vocabulary", "password=fabricated", `tool "password=fabricated"`},
		{"control replaces bearer delimiter", "Bearer\tfabricated", "tool \"Bearer�fabricated\""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := jobProcessPreview("tool", []string{test.value}); got != test.want {
				t.Fatalf("fabricated fixture preview = %q, want %q", got, test.want)
			}
		})
	}
}

func TestJobProcessPreviewDetectsOnlyRenderedPrefix(t *testing.T) {
	// Source checks the rendered prefix, not the full argv or the ellipsis.
	prefix := strings.Repeat("x", 120)
	if got := jobProcessPreview(prefix, []string{"wc_pat_fabricated"}); got != prefix+"…" {
		t.Fatal("secret beyond truncation changed the source preview behavior")
	}
	if got := jobProcessPreview(strings.Repeat("x", 114)+"wc_pat_fabricated", nil); got != strings.Repeat("x", 114)+"wc_pat…" {
		t.Fatal("incomplete sensitive prefix was treated as a full pattern")
	}
	if got := jobProcessPreview(strings.Repeat("x", 113)+"wc_pat_fabricated", nil); got != "[redacted]" {
		t.Fatal("complete sensitive prefix at boundary was not redacted")
	}
	if got := jobProcessPreview("bearer", []string{"fabricated"}); got != "[redacted]" {
		t.Fatal("secret detection did not include the rendered argv separator")
	}
}
