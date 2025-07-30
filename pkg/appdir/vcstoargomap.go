package appdir

import (
	"io/fs"
	"path/filepath"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/kustomize"
)

type VcsToArgoMap struct {
	username        string
	appDirByRepo    map[pkg.RepoURL]*AppDirectory
	appSetDirByRepo map[pkg.RepoURL]*AppSetDirectory
}

func NewVcsToArgoMap(vcsUsername string) VcsToArgoMap {
	return VcsToArgoMap{
		username:        vcsUsername,
		appDirByRepo:    make(map[pkg.RepoURL]*AppDirectory),
		appSetDirByRepo: make(map[pkg.RepoURL]*AppSetDirectory),
	}
}

func (v2a VcsToArgoMap) GetMap() map[pkg.RepoURL]*AppDirectory {
	return v2a.appDirByRepo
}

func (v2a VcsToArgoMap) GetAppSetMap() map[pkg.RepoURL]*AppSetDirectory {
	return v2a.appSetDirByRepo
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

// GetAppSetsInRepo returns AppSetDirectory for the specified repository URL.
func (v2a VcsToArgoMap) GetAppSetsInRepo(repoCloneUrl string) *AppSetDirectory {
	repoUrl, _, err := pkg.NormalizeRepoUrl(repoCloneUrl)
	if err != nil {
		log.Warn().Err(err).Msgf("failed to parse %s", repoCloneUrl)
	}
	appSetDir := v2a.appSetDirByRepo[repoUrl]
	if appSetDir == nil {
		appSetDir = NewAppSetDirectory()
		v2a.appSetDirByRepo[repoUrl] = appSetDir
	}

	return appSetDir
}

func (v2a VcsToArgoMap) WalkKustomizeApps(cloneURL string, rootFS fs.FS) *AppDirectory {
	var (
		result = NewAppDirectory()
		appdir = v2a.GetAppsInRepo(cloneURL)
		apps   = appdir.GetApps(nil)
	)

	for _, app := range apps {
		appPath := app.Spec.GetSource().Path

		kustomizePath := filepath.Join(appPath, "kustomization.yaml")
		kustomizeFiles, kustomizeDir, err := kustomize.ProcessKustomizationFile(rootFS, kustomizePath)
		if err != nil {
			log.Error().Err(err).Msgf("failed to parse kustomize.yaml in %s", appPath)
		}
		for _, file := range kustomizeFiles {
			result.addFile(app.Name, file)
		}
		for _, dir := range kustomizeDir {
			result.addDir(app.Name, dir)
		}
	}

	return result
}

func (v2a VcsToArgoMap) processApp(app v1alpha1.Application, fn func(*AppDirectory)) {

	if src := app.Spec.Source; src != nil {
		appDirectory := v2a.GetAppsInRepo(src.RepoURL)
		fn(appDirectory)
	}

	for _, src := range app.Spec.Sources {
		appDirectory := v2a.GetAppsInRepo(src.RepoURL)
		fn(appDirectory)
	}
}

func (v2a VcsToArgoMap) AddApp(app *v1alpha1.Application) {
	v2a.processApp(*app, func(directory *AppDirectory) {
		directory.AddApp(*app)
	})
}

func (v2a VcsToArgoMap) UpdateApp(old *v1alpha1.Application, new *v1alpha1.Application) {
	v2a.processApp(*old, func(directory *AppDirectory) {
		directory.RemoveApp(*old)
	})

	v2a.processApp(*new, func(directory *AppDirectory) {
		directory.AddApp(*new)
	})
}

func (v2a VcsToArgoMap) DeleteApp(app *v1alpha1.Application) {
	v2a.processApp(*app, func(directory *AppDirectory) {
		directory.RemoveApp(*app)
	})
}

func (v2a VcsToArgoMap) GetVcsRepos() []string {
	var repos []string

	for key := range v2a.appDirByRepo {
		repos = append(repos, key.CloneURL(v2a.username))
	}
	for key := range v2a.appSetDirByRepo {
		repos = append(repos, key.CloneURL(v2a.username))
	}
	return repos
}

func (v2a VcsToArgoMap) AddAppSet(app *v1alpha1.ApplicationSet) {
	if app.Spec.Template.Spec.GetSource().RepoURL == "" {
		log.Warn().Msgf("%s/%s: no source, skipping", app.Namespace, app.Name)
		return
	}

	appSetDirectory := v2a.GetAppSetsInRepo(app.Spec.Template.Spec.GetSource().RepoURL)
	appSetDirectory.ProcessAppSet(*app)
}

func (v2a VcsToArgoMap) UpdateAppSet(old *v1alpha1.ApplicationSet, new *v1alpha1.ApplicationSet) {
	if new.Spec.Template.Spec.GetSource().RepoURL == "" {
		log.Warn().Msgf("%s/%s: no source, skipping", new.Namespace, new.Name)
		return
	}

	oldAppDirectory := v2a.GetAppSetsInRepo(old.Spec.Template.Spec.GetSource().RepoURL)
	oldAppDirectory.RemoveAppSet(*old)

	appSetDirectory := v2a.GetAppSetsInRepo(new.Spec.Template.Spec.GetSource().RepoURL)
	appSetDirectory.ProcessAppSet(*new)
}

func (v2a VcsToArgoMap) DeleteAppSet(app *v1alpha1.ApplicationSet) {
	if app.Spec.Template.Spec.GetSource().RepoURL == "" {
		log.Warn().Msgf("%s/%s: no source, skipping", app.Namespace, app.Name)
		return
	}

	appSetDirectory := v2a.GetAppSetsInRepo(app.Spec.Template.Spec.GetSource().RepoURL)
	appSetDirectory.RemoveAppSet(*app)
}
