package affected_apps

import (
	"context"
	"strings"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/repo"
)

type ArgocdMatcher struct {
	//       map[AppPath]AppName
	repoApps map[string]string
}

func NewArgocdMatcher(vcsToArgoMap pkg.VcsToArgoMap, repo *repo.Repo) *ArgocdMatcher {
	repoApps := vcsToArgoMap.GetAppsInRepo(repo.CloneURL)
	return &ArgocdMatcher{
		repoApps: repoApps,
	}
}

func (a *ArgocdMatcher) AffectedApps(ctx context.Context, changeList []string) (AffectedItems, error) {
	appsMap := make(map[string]string)
	for _, changePath := range changeList {
		for path, name := range a.repoApps {
			if strings.HasPrefix(changePath, path) {
				appsMap[changePath] = name
				break
			}
		}
	}

	return AffectedItems{AppNameToPathMap: appsMap}, nil
}

var _ Matcher = new(ArgocdMatcher)
