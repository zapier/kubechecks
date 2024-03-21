package events

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/git"
)

// TestCleanupGetManifestsError tests the cleanupGetManifestsError function.
func TestCleanupGetManifestsError(t *testing.T) {
	repoDirectory := "/some-dir"

	tests := []struct {
		name          string
		inputErr      error
		expectedError string
	}{
		{
			name:          "helm error",
			inputErr:      errors.New("`helm template . --name-template kubechecks --namespace kubechecks --kube-version 1.22 --values /tmp/kubechecks-mr-clone2267947074/manifests/tooling-eks-01/values.yaml --values /tmp/kubechecks-mr-clone2267947074/manifests/tooling-eks-01/current-tag.yaml --api-versions storage.k8s.io/v1 --api-versions storage.k8s.io/v1beta1 --api-versions v1 --api-versions vault.banzaicloud.com/v1alpha1 --api-versions velero.io/v1 --api-versions vpcresources.k8s.aws/v1beta1 --include-crds` failed exit status 1: Error: execution error at (kubechecks/charts/web/charts/ingress/templates/ingress.yaml:7:20): ingressClass value is required\\n\\nUse --debug flag to render out invalid YAML"),
			expectedError: "Helm Error: execution error at (kubechecks/charts/web/charts/ingress/templates/ingress.yaml:7:20): ingressClass value is required\\n\\nUse --debug flag to render out invalid YAML",
		},
		{
			name:          "strip temp directory",
			inputErr:      fmt.Errorf("error: %s/tmpfile.yaml not found", repoDirectory),
			expectedError: "error: tmpfile.yaml not found",
		},
		{
			name:          "strip temp directory and helm error",
			inputErr:      fmt.Errorf("`helm template . --name-template in-cluster-echo-server --namespace echo-server --kube-version 1.25 --values %s/apps/echo-server/in-cluster/values.yaml --values %s/apps/echo-server/in-cluster/notexist.yaml --api-versions admissionregistration.k8s.io/v1 --api-versions admissionregistration.k8s.io/v1/MutatingWebhookConfiguration --api-versions v1/Secret --api-versions v1/Service --api-versions v1/ServiceAccount --include-crds` failed exit status 1: Error: open %s/apps/echo-server/in-cluster/notexist.yaml: no such file or directory", repoDirectory, repoDirectory, repoDirectory),
			expectedError: "Helm Error: open apps/echo-server/in-cluster/notexist.yaml: no such file or directory",
		},
		{
			name:          "other error",
			inputErr:      errors.New("error: unknown error"),
			expectedError: "error: unknown error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanedError := cleanupGetManifestsError(tt.inputErr, repoDirectory)
			if cleanedError != tt.expectedError {
				t.Errorf("Expected error: %s, \n                    Received: %s", tt.expectedError, cleanedError)
			}
		})
	}
}

type mockVcsClient struct{}

func (m mockVcsClient) Username() string {
	return "username"
}

func TestCheckEventGetRepo(t *testing.T) {
	cloneURL := "https://github.com/zapier/kubechecks.git"
	canonical, err := canonicalize(cloneURL)
	cfg := config.ServerConfig{}
	require.NoError(t, err)

	ctx := context.TODO()

	t.Run("empty branch name", func(t *testing.T) {
		ce := CheckEvent{
			clonedRepos: make(map[string]*git.Repo),
			repoManager: git.NewRepoManager(cfg),
		}

		repo, err := ce.getRepo(ctx, mockVcsClient{}, cloneURL, "")
		require.NoError(t, err)
		assert.Equal(t, "main", repo.BranchName)
		assert.Len(t, ce.clonedRepos, 2)
		assert.Contains(t, ce.clonedRepos, generateRepoKey(canonical, "HEAD"))
		assert.Contains(t, ce.clonedRepos, generateRepoKey(canonical, "main"))
	})

	t.Run("branch is HEAD", func(t *testing.T) {
		ce := CheckEvent{
			clonedRepos: make(map[string]*git.Repo),
			repoManager: git.NewRepoManager(cfg),
		}

		repo, err := ce.getRepo(ctx, mockVcsClient{}, cloneURL, "HEAD")
		require.NoError(t, err)
		assert.Equal(t, "main", repo.BranchName)
		assert.Len(t, ce.clonedRepos, 2)
		assert.Contains(t, ce.clonedRepos, generateRepoKey(canonical, "HEAD"))
		assert.Contains(t, ce.clonedRepos, generateRepoKey(canonical, "main"))
	})

	t.Run("branch is the same as HEAD", func(t *testing.T) {
		ce := CheckEvent{
			clonedRepos: make(map[string]*git.Repo),
			repoManager: git.NewRepoManager(cfg),
		}

		repo, err := ce.getRepo(ctx, mockVcsClient{}, cloneURL, "main")
		require.NoError(t, err)
		assert.Equal(t, "main", repo.BranchName)
		assert.Len(t, ce.clonedRepos, 2)
		assert.Contains(t, ce.clonedRepos, generateRepoKey(canonical, "HEAD"))
		assert.Contains(t, ce.clonedRepos, generateRepoKey(canonical, "main"))
	})

	t.Run("branch is not the same as HEAD", func(t *testing.T) {
		ce := CheckEvent{
			clonedRepos: make(map[string]*git.Repo),
			repoManager: git.NewRepoManager(cfg),
		}

		repo, err := ce.getRepo(ctx, mockVcsClient{}, cloneURL, "gh-pages")
		require.NoError(t, err)
		assert.Equal(t, "gh-pages", repo.BranchName)
		assert.Len(t, ce.clonedRepos, 1)
		assert.Contains(t, ce.clonedRepos, generateRepoKey(canonical, "gh-pages"))
	})
}
