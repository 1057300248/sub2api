package config

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestCustomPatchTicketConfigDefaults(t *testing.T) {
	resetViperWithJWTSecret(t)
	setDefaults()
	const ttlKey = "gateway.openai_codex_ticket.ttl_seconds"
	const leadKey = "gateway.openai_codex_ticket.refresh_before_seconds"
	require.Equal(t, 240, viper.GetInt(ttlKey))
	require.Equal(t, 60, viper.GetInt(leadKey))
	// Reapplying defaults must not overwrite an administrator's existing values.
	viper.Set(ttlKey, 900)
	viper.Set(leadKey, 300)
	setDefaults()
	require.Equal(t, 900, viper.GetInt(ttlKey))
	require.Equal(t, 300, viper.GetInt(leadKey))
}
