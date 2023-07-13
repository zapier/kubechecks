package affected_apps

import (
	"context"
	"strings"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/repo"
)

type ArgocdMatcher struct {
	repoApps []v1alpha1.Application
}

func NewArgocdMatcher(vcsToArgoMap pkg.VcsToArgoMap, repo *repo.Repo) *ArgocdMatcher {
	repoApps := vcsToArgoMap.GetRepo(repo.CloneURL)
	return &ArgocdMatcher{
		repoApps: repoApps,
	}
}

func (a ArgocdMatcher) AffectedApps(ctx context.Context, changeList []string) (AffectedItems, error) {
	appsMap := make(map[string]string)
	for _, changePath := range changeList {
		for _, app := range a.repoApps {
			if strings.HasPrefix(changePath, app.Spec.Source.Path) {
				appsMap[changePath] = app.Name
			}
		}
	}

	return AffectedItems{AppNameToPathMap: appsMap}, nil
}

var _ Matcher = new(ArgocdMatcher)
