package affected_apps

import (
	"context"
	"os"

	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg/appdir"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/vcs"
)

type ArgocdMatcher struct {
	appsDirectory *appdir.AppDirectory
}

func NewArgocdMatcher(vcsToArgoMap container.VcsToArgoMap, repo *vcs.Repo, repoPath string) (*ArgocdMatcher, error) {
	repoApps := getArgocdApps(vcsToArgoMap, repo)
	kustomizeAppFiles := getKustomizeApps(vcsToArgoMap, repo, repoPath)

	appDirectory := appdir.NewAppDirectory().
		Union(repoApps).
		Union(kustomizeAppFiles)

	return &ArgocdMatcher{
		appsDirectory: appDirectory,
	}, nil
}

func logCounts(repoApps *appdir.AppDirectory) {
	if repoApps == nil {
		log.Debug().Msg("found no apps")
	} else {
		log.Debug().Msgf("found %d apps", repoApps.Count())
	}
}

func getKustomizeApps(vcsToArgoMap container.VcsToArgoMap, repo *vcs.Repo, repoPath string) *appdir.AppDirectory {
	log.Debug().Msgf("creating fs for %s", repoPath)
	fs := os.DirFS(repoPath)
	log.Debug().Msg("following kustomize apps")
	kustomizeAppFiles := vcsToArgoMap.WalkKustomizeApps(repo.CloneURL, fs)

	logCounts(kustomizeAppFiles)
	return kustomizeAppFiles
}

func getArgocdApps(vcsToArgoMap container.VcsToArgoMap, repo *vcs.Repo) *appdir.AppDirectory {
	log.Debug().Msgf("looking for %s repos", repo.CloneURL)
	repoApps := vcsToArgoMap.GetAppsInRepo(repo.CloneURL)

	logCounts(repoApps)
	return repoApps
}

func (a *ArgocdMatcher) AffectedApps(ctx context.Context, changeList []string, targetBranch string) (AffectedItems, error) {
	if a.appsDirectory == nil {
		return AffectedItems{}, nil
	}

	appsSlice := a.appsDirectory.FindAppsBasedOnChangeList(changeList, targetBranch)
	return AffectedItems{Applications: appsSlice}, nil
}

var _ Matcher = new(ArgocdMatcher)
