package affected_apps

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg/appdir"
	"github.com/zapier/kubechecks/pkg/git"
)

func TestCreateNewMatcherWithNilVcsMap(t *testing.T) {
	// setup
	var (
		repo git.Repo

		vcsMap = appdir.NewVcsToArgoMap("vcs-username")
	)

	// run test
	matcher, err := NewArgocdMatcher(vcsMap, &repo)
	require.NoError(t, err)

	// verify results
	require.NotNil(t, matcher.appsDirectory)
}

func TestFindAffectedAppsWithNilAppsDirectory(t *testing.T) {
	// setup
	var (
		ctx        = context.TODO()
		changeList = []string{"/go.mod"}
	)

	matcher := ArgocdMatcher{}
	items, err := matcher.AffectedApps(ctx, changeList, "main")

	// verify results
	require.NoError(t, err)
	require.Len(t, items.Applications, 0)
	require.Len(t, items.ApplicationSets, 0)
}
