package config

import (
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg/app_directory"
)

type VcsToArgoMap struct {
	vcsAppStubsByRepo map[RepoURL]*app_directory.AppDirectory
}

func NewVcsToArgoMap() VcsToArgoMap {
	return VcsToArgoMap{
		vcsAppStubsByRepo: make(map[RepoURL]*app_directory.AppDirectory),
	}
}

func (v2a *VcsToArgoMap) GetAppsInRepo(repoCloneUrl string) *app_directory.AppDirectory {
	repoUrl, err := NormalizeRepoUrl(repoCloneUrl)
	if err != nil {
		log.Warn().Err(err).Msgf("failed to parse %s", repoCloneUrl)
	}

	return v2a.vcsAppStubsByRepo[repoUrl]
}

func (v2a *VcsToArgoMap) AddApp(app *v1alpha1.Application) {
	if app.Spec.Source == nil {
		log.Warn().Msgf("%s/%s: no source, skipping", app.Namespace, app.Name)
		return
	}

	rawRepoUrl := app.Spec.Source.RepoURL
	cleanRepoUrl, err := NormalizeRepoUrl(rawRepoUrl)
	if err != nil {
		log.Warn().Err(err).Msgf("%s/%s: failed to parse %s", app.Namespace, app.Name, rawRepoUrl)
		return
	}

	log.Debug().Msgf("%s/%s: %s => %s", app.Namespace, app.Name, rawRepoUrl, cleanRepoUrl)

	appDirectory := v2a.vcsAppStubsByRepo[cleanRepoUrl]
	if appDirectory == nil {
		appDirectory = app_directory.NewAppDirectory()
	}
	appDirectory.AddApp(app)
	v2a.vcsAppStubsByRepo[cleanRepoUrl] = appDirectory
}
