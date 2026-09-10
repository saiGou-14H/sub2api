// SPDX-License-Identifier: Apache-2.0
// Unicode string fidelity for the WebCodex serde_json wire adaptation.
package protocol

import (
	"fmt"
	"strconv"
	"unicode/utf8"
)

// encoding/json substitutes U+FFFD for malformed UTF-8 and lone UTF-16
// surrogates; serde_json rejects them. Do not silently change paths or argv.
// Syntax beyond Unicode escape pairing is checked by encoding/json.
func validateJSONUnicode(b []byte) error {
	if !utf8.Valid(b) {
		return fmt.Errorf("invalid UTF-8 JSON")
	}
	quoted := false
	for i := 0; i < len(b); i++ {
		if b[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || b[i] != '\\' {
			continue
		}
		i++
		if i >= len(b) {
			return fmt.Errorf("incomplete JSON escape")
		}
		if b[i] != 'u' {
			continue
		}
		if i+4 >= len(b) {
			return fmt.Errorf("incomplete Unicode escape")
		}
		n, err := strconv.ParseUint(string(b[i+1:i+5]), 16, 16)
		if err != nil {
			return fmt.Errorf("invalid Unicode escape")
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return fmt.Errorf("unpaired low Unicode surrogate")
		}
		if n < 0xd800 || n > 0xdbff {
			continue
		}
		if i+6 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
			return fmt.Errorf("unpaired high Unicode surrogate")
		}
		low, err := strconv.ParseUint(string(b[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return fmt.Errorf("invalid Unicode surrogate pair")
		}
		i += 6
	}
	return nil
}
