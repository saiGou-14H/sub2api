package service

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSolveOpenAIWebTurnstileTokenMatchesReferenceFixture(t *testing.T) {
	// This is a fixed dx payload generated from the same XOR envelope used by
	// chatgpt2api's utils/turnstile.py. It exercises assignment, concatenation,
	// copying a callable, and indirect invocation without containing a secret.
	const p = "fixture-p-token"
	const dx = "PTJKWEZCSQ8YSBgDBEczSjJKWEZDSQ9QDylDMFBCVVlUR0QvSXZCAUddR0cZCRsUEFcvSXZFAUdfR1ZcO0UjTFlGVQFDcFg0XElaVkVLRCgv"

	token, err := SolveOpenAIWebTurnstileToken(dx, p)
	require.NoError(t, err)
	require.Equal(t, "aGVsbG8gd29ybGQ=", token)
}

func TestSolveOpenAIWebTurnstileTokenRejectsInvalidPayload(t *testing.T) {
	_, err := SolveOpenAIWebTurnstileToken("%%%not-base64%%%", "p")
	require.EqualError(t, err, "turnstile challenge payload is invalid")

	encoded := base64.StdEncoding.EncodeToString([]byte("not-json"))
	_, err = SolveOpenAIWebTurnstileToken(encoded, "p")
	require.EqualError(t, err, "turnstile challenge program is invalid")
}

func TestOpenAIWebTurnstileVMSupportsBrowserPrimitives(t *testing.T) {
	program := []any{
		[]any{2, float64(30), "window"},
		[]any{2, float64(31), "document"},
		[]any{6, float64(32), float64(30), float64(31)},
		[]any{8, float64(33), float64(32)},
		[]any{2, float64(34), "location"},
		[]any{24, float64(35), float64(33), float64(34)},
		[]any{2, float64(36), "window.document"},
		[]any{6, float64(37), float64(36), float64(34)},
		[]any{8, float64(40), float64(3)},
		[]any{7, float64(40), float64(37)},
	}
	raw, err := json.Marshal(program)
	require.NoError(t, err)
	const p = "primitive-fixture"
	encoded := openAIWebTurnstileXOR(string(raw), p)
	dx := base64.StdEncoding.EncodeToString([]byte(encoded))
	token, err := SolveOpenAIWebTurnstileToken(dx, p)
	require.NoError(t, err)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("https://chatgpt.com/")), token)
}
