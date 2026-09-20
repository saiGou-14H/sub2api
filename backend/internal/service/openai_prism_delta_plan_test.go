package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func prismPlanFixtureDelta(filePath, status string) prismDeltaFile {
	return prismDeltaFile{FilePath: filePath, Status: status, Diff: "--- render/" + filePath + "\n+++ codex/" + filePath + "\n@@ -0,0 +1 @@\n+synthetic"}
}

func TestOpenAIPrismDeltaPlanCreatesNestedTextInEmptyDocument(t *testing.T) {
	doc, err := prismReadYUpdate([]byte{0, 0})
	require.NoError(t, err)
	update, expected, err := prismBuildDeltaUpdate(doc, 600, prismPlanFixtureDelta("chapters/intro.tex", "added"))
	require.NoError(t, err)
	created, err := prismReadYUpdate(update)
	require.NoError(t, err)
	require.NoError(t, prismVerifyDelta(created, expected))
	require.Equal(t, "synthetic", expected.Text)
	files, err := created.files()
	require.NoError(t, err)
	require.Len(t, files, 3)
	file := files[expected.NodeID]
	require.Equal(t, "intro.tex", file["filename"])
	require.Equal(t, "text", file["type"])
	parent, ok := file["inFolder"].(string)
	require.True(t, ok)
	require.Equal(t, "chapters", files[parent]["filename"])
	require.Equal(t, "folder", files[parent]["type"])
}

func TestOpenAIPrismDeltaPlanModifiesOnlyMatchingBaseline(t *testing.T) {
	fixtures := prismLoadYDeltaFixtures(t)
	doc, err := prismReadYUpdate(prismDecodeYDeltaFixture(t, fixtures.Documents["simple"].Update))
	require.NoError(t, err)
	delta := prismDeltaFile{
		FilePath: "main.tex", Status: "modified",
		Diff: "--- render/main.tex\n+++ codex/main.tex\n@@ -1,3 +1,3 @@\n alpha\n-beta\n+changed\n gamma",
	}
	update, expected, err := prismBuildDeltaUpdate(doc, 600, delta)
	require.NoError(t, err)
	require.Equal(t, "text-fixture", expected.NodeID)
	require.Equal(t, fixtures.Edits["replace_middle"].Result, expected.Text)
	require.Equal(t, prismDecodeYDeltaFixture(t, fixtures.Edits["replace_middle"].Update), update)
	delta.Diff = "--- render/main.tex\n+++ codex/main.tex\n@@ -1,3 +1,3 @@\n alpha\n-wrong\n+changed\n gamma"
	_, _, err = prismBuildDeltaUpdate(doc, 600, delta)
	require.Equal(t, "baseline_mismatch", prismDeltaReason(err))
	// A failed plan must not mutate the captured document or its identities.
	text, err := doc.textContent("text-fixture")
	require.NoError(t, err)
	require.Equal(t, fixtures.Documents["simple"].Text, text.Text)
}

func TestOpenAIPrismDeltaPlanRejectsAmbiguousOrConflictingPaths(t *testing.T) {
	fixtures := prismLoadYDeltaFixtures(t)
	for _, tc := range []struct {
		name, fixture, filePath, status, reason string
	}{
		{"existing added file", "simple", "main.tex", "added", "path_conflict"},
		{"file used as folder", "simple", "main.tex/child.tex", "added", "path_conflict"},
		{"missing modified file", "simple", "missing.tex", "modified", "missing_baseline"},
		{"duplicate visible paths", "ambiguous_path", "main.tex", "modified", "ambiguous_document"},
		{"binary existing file", "nested", "diagram.png", "modified", "unsupported_file_type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := prismReadYUpdate(prismDecodeYDeltaFixture(t, fixtures.Documents[tc.fixture].Update))
			require.NoError(t, err)
			update, _, err := prismBuildDeltaUpdate(doc, 600, prismPlanFixtureDelta(tc.filePath, tc.status))
			require.Error(t, err)
			require.Empty(t, update)
			require.Equal(t, tc.reason, prismDeltaReason(err))
		})
	}
}

func TestOpenAIPrismDeltaPreflightLeavesUnsupportedFilesUnsynced(t *testing.T) {
	for _, tc := range []struct {
		name, filePath, status, reason string
	}{
		{"root instructions", "AGENTS.md", "added", "ignored_agent_instructions"},
		{"deleted file", "main.tex", "deleted", "unsupported_status"},
		{"rename", "main.tex", "renamed", "unsupported_status"},
		{"pdf without inline body", "render.pdf", "added", "unsupported_binary"},
		{"parent traversal", "../main.tex", "added", "invalid_path"},
		{"absolute path", "/main.tex", "added", "invalid_path"},
		{"backslash", "chapters\\main.tex", "added", "invalid_path"},
		{"windows stream", "main.tex:stream", "added", "invalid_path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.reason, prismDeltaPreflight(prismPlanFixtureDelta(tc.filePath, tc.status)))
		})
	}
	for _, filePath := range []string{"chapters/AGENTS.md", "agents.md"} {
		require.Empty(t, prismDeltaPreflight(prismPlanFixtureDelta(filePath, "added")))
	}
	for _, field := range []string{"body", "mime", "fetch_error"} {
		delta := prismPlanFixtureDelta("diagram.png", "added")
		value := "synthetic"
		switch field {
		case "body":
			delta.BinaryBodyB64 = &value
		case "mime":
			delta.BinaryMimeType = &value
		case "fetch_error":
			delta.BinaryFetchError = &value
		}
		require.Equal(t, "unsupported_binary", prismDeltaPreflight(delta))
	}
	for _, field := range []string{"truncated", "hash_mismatch", "diff_error"} {
		delta := prismPlanFixtureDelta("main.tex", "modified")
		switch field {
		case "truncated":
			delta.DiffTruncated = true
		case "hash_mismatch":
			delta.BaseRenderHashMismatch = true
		case "diff_error":
			value := "synthetic failure"
			delta.DiffError = &value
		}
		require.Equal(t, "incomplete_diff", prismDeltaPreflight(delta))
	}
}
