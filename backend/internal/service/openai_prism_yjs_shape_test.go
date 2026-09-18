package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIPrismYjsRejectsMultiValueMapProperty(t *testing.T) {
	// A Y.Map attribute has exactly one clock. A synthetic array-like content
	// block must not be interpreted as a normal field by keeping its last value.
	var update prismYWriter
	update.uint(1)
	update.uint(2)
	update.uint(700)
	update.uint(0)
	update = append(update, 39)
	update.uint(1)
	update.text("content")
	update.text("root-shape")
	update.uint(1)
	update = append(update, 40)
	update.uint(0)
	update.uint(700)
	update.uint(0)
	update.text("id")
	update.uint(2)
	for _, value := range []string{"unrelated", "root-shape"} {
		update = append(update, 119)
		update.text(value)
	}
	update.uint(0)
	doc, err := prismReadYUpdate(update)
	require.NoError(t, err)
	_, err = doc.files()
	require.ErrorContains(t, err, "map property length")
}

func TestOpenAIPrismYjsAttachmentRejectsDuplicatePathForExistingNode(t *testing.T) {
	builder := prismYNodeBuilder{client: 701}
	builder.node("root-shape", "root", "folder", "", "", false)
	builder.node("file-original", "diagram.png", "url", "root-shape", "", false)
	builder.node("file-duplicate", "diagram.png", "url", "root-shape", "", false)
	doc, err := prismReadYUpdate(builder.update())
	require.NoError(t, err)
	_, exists, err := doc.rootFolder("file-original", "diagram.png")
	require.ErrorContains(t, err, "already contains")
	require.False(t, exists)
}
