package config

import (
	"io/fs"

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

	appdir := v2a.appDirByRepo[repoUrl]
	if appdir == nil {
		appdir = NewAppDirectory()
		v2a.appDirByRepo[repoUrl] = appdir
	}

	return appdir
}

func (v2a *VcsToArgoMap) WalkKustomizeApps(repo *repo.Repo, fs fs.FS) *AppDirectory {

	var (
		err error

		result = NewAppDirectory()
		appdir = v2a.GetAppsInRepo(repo.CloneURL)
		apps   = appdir.GetApps(nil)
	)

	for _, app := range apps {
		if err = walkKustomizeFiles(result, fs, app.Name, app.Path); err != nil {
			log.Error().Err(err).Msgf("failed to parse kustomize.yaml in %s", app.Path)
		}
	}

	return result
}
