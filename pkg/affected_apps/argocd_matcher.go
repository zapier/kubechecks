package affected_apps

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/app_directory"
	"github.com/zapier/kubechecks/pkg/repo"
)

type ArgocdMatcher struct {
	appsDirectory *app_directory.AppDirectory
}

func NewArgocdMatcher(vcsToArgoMap pkg.VcsToArgoMap, repo *repo.Repo) *ArgocdMatcher {
	log.Debug().Msgf("looking for %s repos", repo.CloneURL)
	repoApps := vcsToArgoMap.GetAppsInRepo(repo.CloneURL)
	if repoApps == nil {
		log.Debug().Msg("found no apps")
	} else {
		log.Debug().Msgf("found %d apps", repoApps.Count())
	}

	return &ArgocdMatcher{
		appsDirectory: repoApps,
	}
}

func (a *ArgocdMatcher) AffectedApps(ctx context.Context, changeList []string) (AffectedItems, error) {
	if a.appsDirectory == nil {
		return AffectedItems{}, nil
	}

	appsSlice := a.appsDirectory.FindAppsBasedOnChangeList(changeList)
	return AffectedItems{Applications: appsSlice}, nil
}

var _ Matcher = new(ArgocdMatcher)
