package affected_apps

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zapier/kubechecks/pkg"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	t.Run("simple setup", func(t *testing.T) {
		ctx := context.Background()
		repoURL := "https://github.com/argoproj/argocd-example-apps.git"
		repoFiles := map[string]string{
			// app of apps of apps
			"app-of-apps-of-apps/kustomization.yaml": `
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - clusters.yaml
`,
			"app-of-apps-of-apps/clusters.yaml": `
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: cluster-apps
spec:
  destination:
    namespace: argocd
    server: https://kubernetes.default.svc
  project: default
  source:
    path: charts/app-of-apps
    repoURL: https://gitlab.com/zapier/argo-cd-configs.git
    targetRevision: HEAD
    helm:
      valueFiles:
        - values.yaml
        - ../../app-of-apps/cluster.yaml
  syncPolicy:
    automated:
      prune: true
`,
			// helm chart that renders app of apps
			"charts/app-of-apps/Chart.yaml": `
apiVersion: v1
description: Chart for setting up argocd applications
name: app-of-apps
version: 1.0.0
`,
			"charts/app-of-apps/values.yaml": ``,
			"charts/app-of-apps/templates/app.yaml": `
{{- range $name, $app := .Values.apps }}
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: {{ $name }}
spec:
  destination:
    namespace: argocd
  source:
    path: apps/{{ $name }}
    
`,

			// values files for app of apps
			"app-of-apps/cluster.yaml": `
apps:
  app1: `,

			// one app
			"apps/app1/Chart.yaml":  ``,
			"apps/app1/values.yaml": ``,
		}

		testRepo := dumpFiles(t, repoURL, "HEAD", repoFiles)

		testVCSMap := appdir.NewVcsToArgoMap("vcs-username")
		testVCSMap.AddApp(&v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name: "app-of-apps-of-apps",
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        repoURL,
					Path:           "app-of-apps-of-apps",
					TargetRevision: "HEAD",
				},
			},
		})

		testVCSMap.AddApp(&v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name: "app-of-apps",
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        repoURL,
					Path:           "charts/app-of-apps",
					TargetRevision: "HEAD",
					Helm: &v1alpha1.ApplicationSourceHelm{
						ValueFiles: []string{
							"values.yaml",
							"../../app-of-apps/cluster.yaml",
						},
					},
				},
			},
		})
		testVCSMap.AddApp(&v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name: "app",
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        repoURL,
					Path:           "app/app1",
					TargetRevision: "HEAD",
					Helm: &v1alpha1.ApplicationSourceHelm{
						ValueFiles: []string{
							"values.yaml",
						},
					},
				},
			},
		})

		m, err := NewArgocdMatcher(testVCSMap, testRepo)
		require.NoError(t, err)

		t.Run("modify cluster.yaml", func(t *testing.T) {
			result, err := m.AffectedApps(ctx, []string{"app-of-apps/cluster.yaml"}, "main", testRepo)
			require.NoError(t, err)

			require.Len(t, result.Applications, 1)
			app := result.Applications[0]
			assert.Equal(t, "app-of-apps", app.Name)
		})

		t.Run("modify clusters.yaml", func(t *testing.T) {
			result, err := m.AffectedApps(ctx, []string{"app-of-apps-of-apps/clusters.yaml"}, "main", testRepo)
			require.NoError(t, err)

			require.Len(t, result.Applications, 1)
			app := result.Applications[0]
			assert.Equal(t, "app-of-apps-of-apps", app.Name)
		})

		t.Run("unindexed file in kustomize directory", func(t *testing.T) {
			result, err := m.AffectedApps(ctx, []string{"app-of-apps-of-apps/invalid.yaml"}, "main", testRepo)
			require.NoError(t, err)

			require.Len(t, result.Applications, 1)
			app := result.Applications[0]
			assert.Equal(t, "app-of-apps-of-apps", app.Name)
		})

		t.Run("unindexed file in helm directory", func(t *testing.T) {
			result, err := m.AffectedApps(ctx, []string{"charts/app-of-apps/templates/new.yaml"}, "main", testRepo)
			require.NoError(t, err)

			require.Len(t, result.Applications, 1)
			app := result.Applications[0]
			assert.Equal(t, "app-of-apps", app.Name)
		})
	})
}

func dumpFiles(t *testing.T, cloneURL, target string, files map[string]string) *git.Repo {
	repoHash := hash(t, cloneURL, target)
	tempDir := filepath.Join(t.TempDir(), repoHash)
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

func hash(t *testing.T, repoURL, target string) string {
	t.Helper()

	url, err := pkg.Canonicalize(repoURL)
	require.NoError(t, err)

	data := md5.Sum([]byte(url.Host + url.Path + target))
	return hex.EncodeToString(data[:])
}
