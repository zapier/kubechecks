package affected_apps

import (
	"context"
	"path"

	"github.com/zapier/kubechecks/pkg/config"
)

type AffectedItems struct {
	Applications    []config.ApplicationStub
	ApplicationSets []ApplicationSet
}

type ApplicationSet struct {
	Name string
}

type Matcher interface {
	AffectedApps(ctx context.Context, changeList []string) (AffectedItems, error)
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
