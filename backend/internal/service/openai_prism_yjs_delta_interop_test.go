package service

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/require"
)

type prismYDeltaFixtures struct {
	Documents map[string]struct {
		Update string `json:"update"`
		Text   string `json:"text"`
	} `json:"documents"`
	Edits map[string]struct {
		Initial            string `json:"initial"`
		Before             string `json:"before"`
		Result             string `json:"result"`
		Update             string `json:"update"`
		Verified           string `json:"verified"`
		ConcurrentVerified string `json:"concurrent_verified"`
		ConcurrentText     string `json:"concurrent_text"`
	} `json:"edits"`
	Nodes map[string]struct {
		Update   string `json:"update"`
		Verified string `json:"verified"`
		Text     string `json:"text"`
	} `json:"nodes"`
}

func TestOpenAIPrismYjsNodeBuilderMatchesOfficialUpdates(t *testing.T) {
	fixtures := prismLoadYDeltaFixtures(t)
	for name, fixture := range fixtures.Nodes {
		t.Run(name, func(t *testing.T) {
			builder := prismYNodeBuilder{client: 600}
			if name == "new_project" {
				builder.node("root-fixture", "root", "folder", "", "", false)
			}
			builder.node("folder-added", "chapters", "folder", "root-fixture", "", false)
			builder.node("text-added", "chapter.tex", "text", "folder-added", fixture.Text, true)
			if name == "new_project" {
				builder.setting("init", true)
				builder.setting("deleted", false)
			}
			require.Equal(t, prismDecodeYDeltaFixture(t, fixture.Update), builder.update())
			doc, err := prismReadYUpdate(prismDecodeYDeltaFixture(t, fixture.Verified))
			require.NoError(t, err)
			text, err := doc.textContent("text-added")
			require.NoError(t, err)
			require.Equal(t, fixture.Text, text.Text)
			require.NoError(t, prismVerifyDelta(doc, prismDeltaExpectation{Path: "chapters/chapter.tex", NodeID: "text-added", Text: fixture.Text, Client: 600, Clock: builder.clock}))
		})
	}
}

func prismLoadYDeltaFixtures(t *testing.T) prismYDeltaFixtures {
	t.Helper()
	data, err := os.ReadFile("testdata/prism_yjs_delta_fixtures.json")
	require.NoError(t, err)
	var fixtures prismYDeltaFixtures
	require.NoError(t, json.Unmarshal(data, &fixtures))
	return fixtures
}

func prismDecodeYDeltaFixture(t *testing.T, encoded string) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	return data
}

func TestOpenAIPrismYjsTextOfficialInteropFixtures(t *testing.T) {
	fixtures := prismLoadYDeltaFixtures(t)
	for name, fixture := range fixtures.Documents {
		t.Run(name, func(t *testing.T) {
			doc, err := prismReadYUpdate(prismDecodeYDeltaFixture(t, fixture.Update))
			require.NoError(t, err)
			text, err := doc.textContent("text-fixture")
			if name == "concurrent_insertion" {
				// The limited reader deliberately rejects ambiguous ordering.
				// It must never guess an order and overwrite concurrent text.
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, fixture.Text, text.Text)
			require.Len(t, text.IDs, len(utf16.Encode([]rune(fixture.Text))))
			_, err = doc.textContent("root-fixture")
			require.Error(t, err)
			if name == "nested" {
				nested, err := doc.textContent("nested-fixture")
				require.NoError(t, err)
				require.Equal(t, "intro\n", nested.Text)
				_, err = doc.textContent("binary-fixture")
				require.Error(t, err)
			}
		})
	}
}

func TestOpenAIPrismYjsTextEditsMatchOfficialUpdates(t *testing.T) {
	fixtures := prismLoadYDeltaFixtures(t)
	for name, fixture := range fixtures.Edits {
		t.Run(name, func(t *testing.T) {
			doc, err := prismReadYUpdate(prismDecodeYDeltaFixture(t, fixture.Initial))
			require.NoError(t, err)
			text, err := doc.textContent("text-fixture")
			require.NoError(t, err)
			require.Equal(t, fixture.Before, text.Text)
			update, err := prismEncodeYTextEdit(600, text, fixture.Result)
			require.NoError(t, err)
			// The official generator applies this exact update to the baseline
			// and separately to a concurrent insertion before saving the fixture.
			// Equality therefore validates both encoding and deletion boundaries.
			require.Equal(t, prismDecodeYDeltaFixture(t, fixture.Update), update)
			verified, err := prismReadYUpdate(prismDecodeYDeltaFixture(t, fixture.Verified))
			require.NoError(t, err)
			actual, err := verified.textContent("text-fixture")
			require.NoError(t, err)
			require.Equal(t, fixture.Result, actual.Text)
			require.Contains(t, fixture.ConcurrentText, "[concurrent]")
			unchanged, err := prismEncodeYTextEdit(600, text, text.Text)
			require.NoError(t, err)
			require.Empty(t, unchanged)
		})
	}
}
