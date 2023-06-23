package repo_config

import (
	"github.com/creasty/defaults"
	"github.com/rs/zerolog/log"
)

// Test helpers

func defaultArgoCdApplicationConfig() *ArgoCdApplicationConfig {
	app := &ArgoCdApplicationConfig{}
	err := defaults.Set(app)
	if err != nil {
		log.Warn().Err(err).Msg("could not set App defaults")
	}

	return app
}

func (a *ArgoCdApplicationConfig) withName(name string) *ArgoCdApplicationConfig {
	a.Name = name
	return a
}

func (a *ArgoCdApplicationConfig) withCluster(cluster string) *ArgoCdApplicationConfig {
	a.Cluster = cluster
	return a
}

func (a *ArgoCdApplicationConfig) withPath(path string) *ArgoCdApplicationConfig {
	a.Path = path
	return a
}

func (a *ArgoCdApplicationConfig) withAdditionalPaths(paths ...string) *ArgoCdApplicationConfig {
	a.AdditionalPaths = paths
	return a
}

func defaultArgoCdApplicationSetConfig() *ArgocdApplicationSetConfig {
	appset := &ArgocdApplicationSetConfig{}
	err := defaults.Set(appset)
	if err != nil {
		log.Warn().Err(err).Msg("could not set App defaults")
	}

	return appset
}

func (a *ArgocdApplicationSetConfig) withName(name string) *ArgocdApplicationSetConfig {
	a.Name = name
	return a
}

func (a *ArgocdApplicationSetConfig) withPaths(paths ...string) *ArgocdApplicationSetConfig {
	a.Paths = paths
	return a
}
