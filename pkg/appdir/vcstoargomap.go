package appdir

import (
	"io/fs"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg"
)

type VcsToArgoMap struct {
	username     string
	appDirByRepo map[pkg.RepoURL]*AppDirectory
}

func NewVcsToArgoMap(vcsUsername string) VcsToArgoMap {
	return VcsToArgoMap{
		username:     vcsUsername,
		appDirByRepo: make(map[pkg.RepoURL]*AppDirectory),
	}
}

func (v2a VcsToArgoMap) GetMap() map[pkg.RepoURL]*AppDirectory {
	return v2a.appDirByRepo
}

func (v2a VcsToArgoMap) GetAppsInRepo(repoCloneUrl string) *AppDirectory {
	repoUrl, _, err := pkg.NormalizeRepoUrl(repoCloneUrl)
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

func (v2a VcsToArgoMap) WalkKustomizeApps(cloneURL string, fs fs.FS) *AppDirectory {
	var (
		err error

		result = NewAppDirectory()
		appdir = v2a.GetAppsInRepo(cloneURL)
		apps   = appdir.GetApps(nil)
	)

	for _, app := range apps {
		appPath := app.Spec.GetSource().Path
		if err = walkKustomizeFiles(result, fs, app.Name, appPath); err != nil {
			log.Error().Err(err).Msgf("failed to parse kustomize.yaml in %s", appPath)
		}
	}

	return result
}

func (v2a VcsToArgoMap) AddApp(app *v1alpha1.Application) {
	if app.Spec.Source == nil {
		log.Warn().Msgf("%s/%s: no source, skipping", app.Namespace, app.Name)
		return
	}

	appDirectory := v2a.GetAppsInRepo(app.Spec.Source.RepoURL)
	appDirectory.ProcessApp(*app)
}

func (v2a VcsToArgoMap) UpdateApp(old *v1alpha1.Application, new *v1alpha1.Application) {
	if new.Spec.Source == nil {
		log.Warn().Msgf("%s/%s: no source, skipping", new.Namespace, new.Name)
		return
	}

	oldAppDirectory := v2a.GetAppsInRepo(old.Spec.Source.RepoURL)
	oldAppDirectory.RemoveApp(*old)

	newAppDirectory := v2a.GetAppsInRepo(new.Spec.Source.RepoURL)
	newAppDirectory.ProcessApp(*new)
}

func (v2a VcsToArgoMap) DeleteApp(app *v1alpha1.Application) {
	if app.Spec.Source == nil {
		log.Warn().Msgf("%s/%s: no source, skipping", app.Namespace, app.Name)
		return
	}

	oldAppDirectory := v2a.GetAppsInRepo(app.Spec.Source.RepoURL)
	oldAppDirectory.RemoveApp(*app)
}

func (v2a VcsToArgoMap) GetVcsRepos() []string {
	var repos []string

	for key := range v2a.appDirByRepo {
		repos = append(repos, key.CloneURL(v2a.username))
	}

	return repos
}
