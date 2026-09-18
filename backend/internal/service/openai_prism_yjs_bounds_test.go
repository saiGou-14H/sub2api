package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIPrismYjsEnforcesAggregateLimits(t *testing.T) {
	t.Run("structs across clients", func(t *testing.T) {
		var update prismYWriter
		update.uint(2)
		for client := uint64(1); client <= 2; client++ {
			update.uint(6000)
			update.uint(client)
			update.uint(0)
			for i := 0; i < 6000; i++ {
				update = append(update, 0) // GC struct, length one.
				update.uint(1)
			}
		}
		update.uint(0)
		_, err := prismReadYUpdate(update)
		require.ErrorContains(t, err, "limit")
	})
	t.Run("deleted ranges across clients", func(t *testing.T) {
		var update prismYWriter
		update.uint(0)
		update.uint(6)
		for client := uint64(1); client <= 6; client++ {
			update.uint(client)
			update.uint(9000)
			for clock := uint64(0); clock < 9000; clock++ {
				update.uint(clock * 2)
				update.uint(1)
			}
		}
		_, err := prismReadYUpdate(update)
		require.ErrorContains(t, err, "limit")
	})
	t.Run("nested arrays share budget", func(t *testing.T) {
		var update prismYWriter
		update.uint(1)
		update.uint(1)
		update.uint(1)
		update.uint(0)
		update = append(update, 40) // ContentAny with a map key.
		update.uint(1)
		update.text("metadata")
		update.text("values")
		update.uint(1)
		update = append(update, 117)
		update.uint(12)
		for i := 0; i < 12; i++ {
			update = append(update, 117)
			update.uint(4500)
			for j := 0; j < 4500; j++ {
				update = append(update, 126) // null
			}
		}
		update.uint(0)
		_, err := prismReadYUpdate(update)
		require.ErrorContains(t, err, "limit")
	})
}

func TestOpenAIPrismYjsRejectsAmbiguousClockOwnership(t *testing.T) {
	for _, secondClock := range []uint64{0, 1, 2} {
		var update prismYWriter
		update.uint(2)
		for _, clock := range []uint64{0, secondClock} {
			update.uint(1)
			update.uint(7)
			update.uint(clock)
			update = append(update, 0)
			update.uint(2)
		}
		update.uint(0)
		_, err := prismReadYUpdate(update)
		require.Error(t, err, "duplicate client blocks must not replace clock ownership")
	}
	var overflow prismYWriter
	overflow.uint(1)
	overflow.uint(1)
	overflow.uint(1)
	overflow.uint(1<<53 - 1)
	overflow = append(overflow, 0)
	overflow.uint(2)
	overflow.uint(0)
	_, err := prismReadYUpdate(overflow)
	require.Error(t, err)
}

func TestOpenAIPrismYjsInitializationRequiresEmptyLiveContent(t *testing.T) {
	empty, err := prismReadYUpdate([]byte{0, 0})
	require.NoError(t, err)
	require.True(t, empty.canInitialize())
	for _, tc := range []struct {
		name, root, key string
		value           byte
		initialize      bool
	}{
		{"scalar content", "content", "unexpected", 126, false},
		{"empty content key", "content", "", 126, false},
		{"deleted project", "settings", "deleted", 120, false},
		{"live project settings", "settings", "deleted", 121, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var update prismYWriter
			update.uint(1)
			update.uint(1)
			update.uint(1)
			update.uint(0)
			update = append(update, 40)
			update.uint(1)
			update.text(tc.root)
			update.text(tc.key)
			update.uint(1)
			update = append(update, tc.value)
			update.uint(0)
			doc, err := prismReadYUpdate(update)
			require.NoError(t, err)
			files, err := doc.files()
			require.NoError(t, err)
			require.Empty(t, files)
			require.Equal(t, tc.initialize, doc.canInitialize())
		})
	}
}

func TestOpenAIPrismYjsRejectsCyclicOrigin(t *testing.T) {
	var update prismYWriter
	update.uint(1)
	update.uint(1)
	update.uint(1)
	update.uint(0)
	update = append(update, 135) // ContentType with an origin, pointing to itself.
	update.uint(1)
	update.uint(0)
	update.uint(1)
	update.uint(0)
	doc, err := prismReadYUpdate(update)
	require.NoError(t, err)
	_, err = doc.files()
	require.Error(t, err)
	require.False(t, doc.canInitialize())
}

func TestOpenAIPrismYjsIndexedRangesRetainInteriorOrigins(t *testing.T) {
	var update prismYWriter
	update.uint(1)
	update.uint(3)
	update.uint(1)
	update.uint(0)
	for i := 0; i < 3; i++ {
		update = append(update, 8) // Four ContentAny values in an unrelated root.
		update.uint(1)
		update.text("metadata")
		update.uint(4)
		update = append(update, 126, 126, 126, 126)
	}
	update.uint(1)
	update.uint(1)
	update.uint(3)
	for _, span := range [][2]uint64{{4, 2}, {0, 2}, {1, 4}} {
		update.uint(span[0])
		update.uint(span[1])
	}
	doc, err := prismReadYUpdate(update)
	require.NoError(t, err)
	require.Same(t, doc.items[prismYID{1, 0}], doc.find(&prismYID{1, 3}))
	require.Same(t, doc.items[prismYID{1, 4}], doc.find(&prismYID{1, 7}))
	require.Same(t, doc.items[prismYID{1, 8}], doc.find(&prismYID{1, 11}))
	require.True(t, doc.items[prismYID{1, 0}].deleted)
	require.True(t, doc.items[prismYID{1, 4}].deleted)
	require.False(t, doc.items[prismYID{1, 8}].deleted)
	require.Nil(t, doc.find(&prismYID{1, 12}))
	require.Nil(t, doc.find(&prismYID{2, 3}))
}
