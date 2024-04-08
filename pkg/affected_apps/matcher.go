package affected_apps

import (
	"context"
	"path"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
)

type AffectedItems struct {
	Applications    []v1alpha1.Application
	ApplicationSets []ApplicationSet
}

func (ai AffectedItems) Union(other AffectedItems) AffectedItems {
	// merge apps
	appNameSet := make(map[string]struct{})
	for _, app := range ai.Applications {
		appNameSet[app.Name] = struct{}{}
	}
	for _, app := range other.Applications {
		if _, ok := appNameSet[app.Name]; ok {
			continue
		}

		ai.Applications = append(ai.Applications, app)
	}

	// merge appsets
	appSetNameSet := make(map[string]struct{})
	for _, appSet := range ai.ApplicationSets {
		appSetNameSet[appSet.Name] = struct{}{}
	}
	for _, appSet := range other.ApplicationSets {
		if _, ok := appSetNameSet[appSet.Name]; ok {
			continue
		}

		ai.ApplicationSets = append(ai.ApplicationSets, appSet)
	}

	// return the merge
	return ai
}

type ApplicationSet struct {
	Name string
}

type Matcher interface {
	AffectedApps(ctx context.Context, changeList []string, targetBranch string) (AffectedItems, error)
}

// modifiedDirs filters a list of changed files down to a list
// the unique dirs containing the changed files
func modifiedDirs(changeList []string) []string {
	dirMap := map[string]bool{}
	for _, file := range changeList {
		dir := path.Dir(file)
		dirMap[dir] = true
	}

	dirs := []string{}
	for k := range dirMap {
		dirs = append(dirs, k)
	}

	return dirs
}
