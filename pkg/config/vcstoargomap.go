package config

import (
	"io/fs"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg/repo"
)

type VcsToArgoMap struct {
	appDirByRepo map[repoURL]*AppDirectory
}

func NewVcsToArgoMap() VcsToArgoMap {
	return VcsToArgoMap{
		appDirByRepo: make(map[repoURL]*AppDirectory),
	}
}

func (v2a *VcsToArgoMap) GetAppsInRepo(repoCloneUrl string) *AppDirectory {
	repoUrl, err := normalizeRepoUrl(repoCloneUrl)
	if err != nil {
		log.Warn().Err(err).Msgf("failed to parse %s", repoCloneUrl)
	}

	return v2a.appDirByRepo[repoUrl]
}

func (v2a *VcsToArgoMap) WalkKustomizeApps(repo *repo.Repo, fs fs.FS) (*AppDirectory, error) {
	repoUrl, err := normalizeRepoUrl(repo.CloneURL)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse %s", repo.CloneURL)
	}

	appdir := v2a.appDirByRepo[repoUrl]
	apps := appdir.GetApps(nil)

	var result AppDirectory
	for _, app := range apps {
		if err = walkKustomizeFiles(&result, fs, app.Name, app.Path); err != nil {
			log.Error().Err(err).Msgf("failed to parse kustomize.yaml in %s", app.Path)
		}
	}

	return &result, nil
}
