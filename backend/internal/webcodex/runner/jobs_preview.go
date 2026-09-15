// SPDX-License-Identifier: Apache-2.0
// Ported from WebCodex crates/webcodex-core/src/audit_preview.rs (process_preview)
// and sensitive_text.rs (secret_like_value), revision
// 97ad66949a859174911c2f6da2ff1063be98bfa9.
package runner

import (
	"strings"
	"unicode"
)

// jobProcessPreview is a bounded presentation summary, never executable input
// or a retry source. Like the Rust source, the limit is 120 Unicode scalars
// before an optional ellipsis, and secret detection examines only that prefix.
func jobProcessPreview(executable string, args []string) string {
	const limit = 120
	summary := make([]rune, 0, limit+1)
	push := func(character rune) bool {
		if len(summary) >= limit {
			return false
		}
		summary = append(summary, character)
		return true
	}
	truncated := false
	for i := 0; i <= len(args); i++ {
		value := executable
		if i > 0 {
			value = args[i-1]
		}
		if len(summary) != 0 && !push(' ') {
			truncated = true
			break
		}
		simple := value != ""
		for _, character := range value {
			if !jobPreviewAlphanumeric(character) && !strings.ContainsRune("_-./\\", character) {
				simple = false
				break
			}
		}
		if !simple && !push('"') {
			truncated = true
			break
		}
		for _, character := range value {
			if character == '"' || character == '\\' && !simple {
				if !push('\\') || !push(character) {
					truncated = true
					break
				}
			} else {
				if unicode.IsControl(character) {
					character = '\uFFFD'
				}
				if !push(character) {
					truncated = true
					break
				}
			}
		}
		if truncated {
			break
		}
		if !simple && !push('"') {
			truncated = true
			break
		}
	}
	if jobPreviewSecretLike(string(summary)) {
		return "[redacted]"
	}
	if truncated {
		summary = append(summary, '…')
	}
	return string(summary)
}

// Rust char::is_alphanumeric combines Alphabetic and Number. Alphabetic also
// includes Other_Alphabetic marks; unicode.IsLetter alone would miss them.
// Character properties follow the Unicode tables shipped with the Go toolchain.
func jobPreviewAlphanumeric(character rune) bool {
	return unicode.IsLetter(character) || unicode.IsNumber(character) ||
		unicode.Is(unicode.Other_Alphabetic, character)
}

func jobPreviewSecretLike(value string) bool {
	// Rust to_ascii_lowercase does not fold non-ASCII Unicode characters.
	lower := []byte(value)
	for i, character := range lower {
		if character >= 'A' && character <= 'Z' {
			lower[i] = character + ('a' - 'A')
		}
	}
	text := string(lower)
	for _, pattern := range [...]string{
		"-----begin", "bearer ", "api_key", "token=", "id_rsa", "id_ed25519",
		"wc_pat_", "wc_agent_", "wc_acct_", "wc_oat_", "wc_ort_", "wc_csec_", "wc_pair_", "wc_boot_",
	} {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return false
}
