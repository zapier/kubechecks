package affected_apps

import (
	"context"
	"os"

	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/repo"
)

type ArgocdMatcher struct {
	appsDirectory *config.AppDirectory
}

func NewArgocdMatcher(vcsToArgoMap config.VcsToArgoMap, repo *repo.Repo, repoPath string) (*ArgocdMatcher, error) {
	repoApps := getArgocdApps(vcsToArgoMap, repo)
	kustomizeAppFiles := getKustomizeApps(vcsToArgoMap, repo, repoPath)

	appDirectory := new(config.AppDirectory).
		Union(repoApps).
		Union(kustomizeAppFiles)

	return &ArgocdMatcher{
		appsDirectory: appDirectory,
	}, nil
}

func logCounts(repoApps *config.AppDirectory) {
	if repoApps == nil {
		log.Debug().Msg("found no apps")
	} else {
		log.Debug().Msgf("found %d apps", repoApps.Count())
	}
}

func getKustomizeApps(vcsToArgoMap config.VcsToArgoMap, repo *repo.Repo, repoPath string) *config.AppDirectory {
	log.Debug().Msgf("creating fs for %s", repoPath)
	fs := os.DirFS(repoPath)
	log.Debug().Msg("following kustomize apps")
	kustomizeAppFiles, err := vcsToArgoMap.WalkKustomizeApps(repo, fs)
	if err != nil {
		log.Warn().Err(err).Msgf("failed to follow kustomize files")
	}

	logCounts(kustomizeAppFiles)
	return kustomizeAppFiles
}

func getArgocdApps(vcsToArgoMap config.VcsToArgoMap, repo *repo.Repo) *config.AppDirectory {
	log.Debug().Msgf("looking for %s repos", repo.CloneURL)
	repoApps := vcsToArgoMap.GetAppsInRepo(repo.CloneURL)

	logCounts(repoApps)
	return repoApps
}

func (a *ArgocdMatcher) AffectedApps(ctx context.Context, changeList []string) (AffectedItems, error) {
	if a.appsDirectory == nil {
		return AffectedItems{}, nil
	}

	appsSlice := a.appsDirectory.FindAppsBasedOnChangeList(changeList)
	return AffectedItems{Applications: appsSlice}, nil
}

var _ Matcher = new(ArgocdMatcher)
