package service

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCodexTicketSettingsNormalize(t *testing.T) {
	cfg := DefaultCodexTicketSettings()
	normalized, err := NormalizeCodexTicketSettings(cfg)
	require.NoError(t, err)
	require.False(t, normalized.Enabled)
	require.Zero(t, normalized.Revision)
	cfg.Models = []string{" a ", "a", "b"}
	id := int64(8)
	cfg.HarvestProxyID = &id
	normalized, err = NormalizeCodexTicketSettings(cfg)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, normalized.Models)
	*normalized.HarvestProxyID = 9
	normalized.Models[0] = "different"
	require.Equal(t, int64(8), *cfg.HarvestProxyID)
	require.Equal(t, " a ", cfg.Models[0])
	for _, mutate := range []func(*CodexTicketSettings){
		func(c *CodexTicketSettings) { c.Enabled = true },
		func(c *CodexTicketSettings) { c.Revision = CodexTicketMaxRevision + 1 },
		func(c *CodexTicketSettings) { c.SchemaVersion = 2 },
		func(c *CodexTicketSettings) { c.Models = nil },
		func(c *CodexTicketSettings) { c.Models = []string{"a\nb"} },
		func(c *CodexTicketSettings) { c.TTLSeconds = 59 },
		func(c *CodexTicketSettings) { c.RefreshBeforeSeconds = c.TTLSeconds },
		func(c *CodexTicketSettings) { c.MissingPolicy = "unknown" },
		func(c *CodexTicketSettings) { c.MaxConcurrency = 9 },
		func(c *CodexTicketSettings) { c.MaxProbesPerMinute = 61 },
		func(c *CodexTicketSettings) { c.TargetLength = 63 },
	} {
		cfg := DefaultCodexTicketSettings()
		mutate(&cfg)
		_, err := NormalizeCodexTicketSettings(cfg)
		require.Error(t, err)
	}
}
