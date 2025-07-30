package affected_apps

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
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
	items, err := matcher.AffectedApps(ctx, changeList, "main", nil)

	// verify results
	require.NoError(t, err)
	require.Len(t, items.Applications, 0)
	require.Len(t, items.ApplicationSets, 0)
}

func TestScenarios(t *testing.T) {
	repoURL := "https://github.com/argoproj/argocd-example-apps.git"

	testCases := map[string]struct {
		files        map[string]string
		changedFiles []string
		app          v1alpha1.Application
		expected     bool
	}{
		"helm - match": {
			files: map[string]string{
				"app/Chart.yaml": `apiVersion: v1`,
			},
			changedFiles: []string{"app/Chart.yaml"},
			app: v1alpha1.Application{
				Spec: v1alpha1.ApplicationSpec{
					Source: &v1alpha1.ApplicationSource{
						RepoURL: repoURL,
						Path:    "app/",
					},
				},
			},
			expected: true,
		},
		"helm - value file outside of app path": {
			files: map[string]string{
				"app/Chart.yaml": `apiVersion: v1`,
				"values.yaml":    "content",
			},
			changedFiles: []string{"values.yaml"},
			app: v1alpha1.Application{
				Spec: v1alpha1.ApplicationSpec{
					Source: &v1alpha1.ApplicationSource{
						RepoURL: repoURL,
						Path:    "app/",
						Helm: &v1alpha1.ApplicationSourceHelm{
							ValueFiles: []string{
								"../values.yaml",
							},
						},
					},
				},
			},
			expected: true,
		},
		"helm - file parameter outside of app path": {
			files: map[string]string{
				"app/Chart.yaml": `apiVersion: v1`,
				"values.yaml":    "content",
			},
			app: v1alpha1.Application{
				Spec: v1alpha1.ApplicationSpec{
					Source: &v1alpha1.ApplicationSource{
						RepoURL: repoURL,
						Path:    "app/",
						Helm: &v1alpha1.ApplicationSourceHelm{
							FileParameters: []v1alpha1.HelmFileParameter{{
								Name: "key", Path: "../values.yaml",
							}},
						},
					},
				},
			},
			changedFiles: []string{"values.yaml"},
			expected:     true,
		},
		"kustomize": {
			files: map[string]string{
				"app/kustomization.yaml": `
resources:
- file.yaml`,
				"app/file.yaml": "content",
			},
			changedFiles: []string{"app/file.yaml"},
			app: v1alpha1.Application{
				Spec: v1alpha1.ApplicationSpec{
					Source: &v1alpha1.ApplicationSource{
						RepoURL: repoURL,
						Path:    "app/",
					},
				},
			},
			expected: true,
		},
		"kustomize - file is outside of app path": {
			files: map[string]string{
				"app/kustomization.yaml": `
resources:
- ../file.yaml`,
				"file.yaml": "content",
			},
			changedFiles: []string{"file.yaml"},
			app: v1alpha1.Application{
				Spec: v1alpha1.ApplicationSpec{
					Source: &v1alpha1.ApplicationSource{
						RepoURL: repoURL,
						Path:    "app/",
					},
				},
			},
			expected: true,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			tc.app.Name = name
			tc.app.Spec.Source.RepoURL = repoURL

			testRepo := dumpFiles(t, repoURL, "HEAD", tc.files)

			testVCSMap := appdir.NewVcsToArgoMap("vcs-username")
			testVCSMap.AddApp(&tc.app)

			m, err := NewArgocdMatcher(testVCSMap, testRepo)
			require.NoError(t, err)

			ctx := context.Background()

			result, err := m.AffectedApps(ctx, tc.changedFiles, "main", testRepo)
			require.NoError(t, err)

			if tc.expected {
				require.Len(t, result.Applications, 1)
				app := result.Applications[0]
				assert.Equal(t, tc.app.Name, app.Name)
			} else {
				require.Len(t, result.Applications, 0)
			}
		})
	}
}

func dumpFiles(t *testing.T, cloneURL, target string, files map[string]string) *git.Repo {
	tempDir := filepath.Join(t.TempDir(), strconv.Itoa(rand.Int()))
	repo := &git.Repo{
		BranchName: target,
		CloneURL:   cloneURL,
		Directory:  tempDir,
	}

	for file, fileContent := range files {
		fullfilepath := filepath.Join(tempDir, file)

		// ensure the directories exist
		filedir := filepath.Dir(fullfilepath)
		err := os.MkdirAll(filedir, 0o755)
		require.NoError(t, err)

		// write the file to disk
		err = os.WriteFile(fullfilepath, []byte(fileContent), 0o600)
		require.NoError(t, err)
	}

	return repo
}
