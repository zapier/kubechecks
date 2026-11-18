package config

import (
	"testing"
	"time"

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
	v.Set("argocd-api-plaintext", "true")
	v.Set("worst-conftest-state", "warning")
	v.Set("repo-refresh-interval", "10m")
	v.Set("additional-apps-namespaces", "default,kube-system")

	cfg, err := NewWithViper(v)
	require.NoError(t, err)
	assert.Equal(t, zerolog.InfoLevel, cfg.LogLevel)
	assert.Equal(t, true, cfg.ArgoCDInsecure)
	assert.Equal(t, true, cfg.ArgoCDPlainText)
	assert.Equal(t, pkg.StateWarning, cfg.WorstConfTestState, "worst states can be overridden")
	assert.Equal(t, time.Minute*10, cfg.RepoRefreshInterval)
	assert.Equal(t, []string{"default", "kube-system"}, cfg.AdditionalAppsNamespaces)
}

func TestNew_MaxCommentsPerCheck(t *testing.T) {
	for value, want := range map[int]int{0: 999, 1: 1, 25: 25, 999: 999} {
		v := viper.New()
		v.Set("max-comments-per-check", value)

		cfg, err := NewWithViper(v)
		require.NoError(t, err, "%d", value)
		assert.Equal(t, want, cfg.MaxCommentsPerCheck, "%d", value)
	}

	for _, value := range []int{-1, 1000} {
		v := viper.New()
		v.Set("max-comments-per-check", value)

		_, err := NewWithViper(v)
		require.ErrorContains(t, err, "max-comments-per-check", "%d", value)
	}
}

func TestNew_VcsBaseUrlDefault(t *testing.T) {
	for vcsType, want := range map[string]string{
		"github": "https://api.github.com",
		"gitlab": "https://gitlab.com",
	} {
		v := viper.New()
		v.Set("vcs-type", vcsType)

		cfg, err := NewWithViper(v)
		require.NoError(t, err, vcsType)
		assert.Equal(t, want, cfg.VcsBaseUrl, vcsType)
	}

	v := viper.New()
	v.Set("vcs-type", "github")
	v.Set("vcs-base-url", "https://github.example.com/api/v3")

	cfg, err := NewWithViper(v)
	require.NoError(t, err)
	assert.Equal(t, "https://github.example.com/api/v3", cfg.VcsBaseUrl, "an explicit base url is kept")
}

func TestNew_InvalidVcsType(t *testing.T) {
	// vcs-type is validated here because a value from KUBECHECKS_VCS_TYPE reaches
	// viper without flag-level validation.
	v := viper.New()
	v.Set("vcs-type", "bitbucket")

	_, err := NewWithViper(v)
	require.ErrorContains(t, err, "vcs-type must be one of github or gitlab")
	require.ErrorContains(t, err, `"bitbucket"`)
}
