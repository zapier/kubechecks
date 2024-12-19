package affected_apps

import (
	"context"
	"os"

	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg/appdir"
	"github.com/zapier/kubechecks/pkg/git"
)

type ArgocdMatcher struct {
	appsDirectory    *appdir.AppDirectory
	appSetsDirectory *appdir.AppSetDirectory
}

func NewArgocdMatcher(vcsToArgoMap appdir.VcsToArgoMap, repo *git.Repo) (*ArgocdMatcher, error) {
	repoApps := getArgocdApps(vcsToArgoMap, repo)
	kustomizeAppFiles := getKustomizeApps(vcsToArgoMap, repo, repo.Directory)

	appDirectory := appdir.NewAppDirectory().
		Union(repoApps).
		Union(kustomizeAppFiles)

	repoAppSets := getArgocdAppSets(vcsToArgoMap, repo)
	appSetDirectory := appdir.NewAppSetDirectory().
		Union(repoAppSets)

	return &ArgocdMatcher{
		appsDirectory:    appDirectory,
		appSetsDirectory: appSetDirectory,
	}, nil
}

func logCounts(repoApps *appdir.AppDirectory) {
	if repoApps == nil {
		log.Debug().Msg("found no apps")
	} else {
		log.Debug().Int("apps", repoApps.AppsCount()).
			Int("app_files", repoApps.AppFilesCount()).
			Int("app_dirs", repoApps.AppDirsCount()).
			Msg("mapped apps")
	}
}

func getKustomizeApps(vcsToArgoMap appdir.VcsToArgoMap, repo *git.Repo, repoPath string) *appdir.AppDirectory {
	log.Debug().Msgf("creating fs for %s", repoPath)
	fs := os.DirFS(repoPath)

	log.Debug().Msg("following kustomize apps")
	kustomizeAppFiles := vcsToArgoMap.WalkKustomizeApps(repo.CloneURL, fs)

	logCounts(kustomizeAppFiles)
	return kustomizeAppFiles
}

func getArgocdApps(vcsToArgoMap appdir.VcsToArgoMap, repo *git.Repo) *appdir.AppDirectory {
	log.Debug().Msgf("looking for %s repos", repo.CloneURL)
	repoApps := vcsToArgoMap.GetAppsInRepo(repo.CloneURL)

	logCounts(repoApps)
	return repoApps
}

func getArgocdAppSets(vcsToArgoMap appdir.VcsToArgoMap, repo *git.Repo) *appdir.AppSetDirectory {
	log.Debug().Msgf("looking for %s repos", repo.CloneURL)
	repoApps := vcsToArgoMap.GetAppSetsInRepo(repo.CloneURL)

	if repoApps == nil {
		log.Debug().Msg("found no appSets")
	} else {
		log.Debug().Msgf("found %d appSets", repoApps.Count())
	}
	return repoApps
}

func (a *ArgocdMatcher) AffectedApps(_ context.Context, changeList []string, targetBranch string, repo *git.Repo) (AffectedItems, error) {
	if a.appsDirectory == nil {
		return AffectedItems{}, nil
	}

	appsSlice := a.appsDirectory.FindAppsBasedOnChangeList(changeList, targetBranch)
	appSetsSlice := a.appSetsDirectory.FindAppSetsBasedOnChangeList(changeList, repo)

	// and return both apps and appSets
	return AffectedItems{
		Applications:    appsSlice,
		ApplicationSets: appSetsSlice,
	}, nil
}

var _ Matcher = new(ArgocdMatcher)
