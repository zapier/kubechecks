package affected_apps

import (
	"context"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/repo"
)

type ArgocdMatcher struct {
	//       map[AppPath]AppName
	repoApps map[string]string
}

func NewArgocdMatcher(vcsToArgoMap pkg.VcsToArgoMap, repo *repo.Repo) *ArgocdMatcher {
	log.Debug().Msgf("looking for %s repos", repo.CloneURL)
	repoApps := vcsToArgoMap.GetAppsInRepo(repo.CloneURL)
	log.Debug().Msgf("found %d apps", len(repoApps))
	return &ArgocdMatcher{
		repoApps: repoApps,
	}
}

func (a *ArgocdMatcher) AffectedApps(ctx context.Context, changeList []string) (AffectedItems, error) {
	log.Debug().Msgf("checking %d changes", len(changeList))

	appsMap := make(map[string]string)
	appsSet := make(map[string]struct{})
	for _, changePath := range changeList {
		log.Debug().Msgf("change: %s", changePath)
		for path, name := range a.repoApps {
			log.Debug().Msgf("- app path: %s", path)
			if strings.HasPrefix(changePath, path) {
				log.Debug().Msg("match!")
				appsMap[name] = path
				appsSet[name] = struct{}{}
				break
			}
		}
	}

	log.Debug().Msgf("matched %d files into %d apps", len(appsMap), len(appsSet))
	return AffectedItems{AppNameToPathMap: appsMap}, nil
}

var _ Matcher = new(ArgocdMatcher)
