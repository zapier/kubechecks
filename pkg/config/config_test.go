package config

import (
	"testing"

	"github.com/rs/zerolog"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg"
)

func TestNew(t *testing.T) {
	v := viper.New()
	v.Set("log-level", "info")
	v.Set("argocd-api-insecure", "true")
	v.Set("worst-conftest-state", "warning")

	cfg, err := NewWithViper(v)
	require.NoError(t, err)
	assert.Equal(t, zerolog.InfoLevel, cfg.LogLevel)
	assert.Equal(t, true, cfg.ArgoCDInsecure)
	assert.Equal(t, pkg.StateWarning, cfg.WorstConfTestState, "worst states can be overridden")

}
