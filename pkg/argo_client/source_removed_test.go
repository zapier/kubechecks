package argo_client

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg/git"
	"github.com/zapier/kubechecks/pkg/vcs"
)

const removedTestRepo = "https://github.com/testuser/testrepo.git"

var removedTestPR = vcs.PullRequest{
	CloneURL: removedTestRepo,
	BaseRef:  "main",
	HeadRef:  "remove-app",
}

// headWith returns a checkout of the pull request's head holding only the given directories.
func headWith(t *testing.T, dirs ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range dirs {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, dir, "Chart.yaml"), []byte("name: x\n"), 0o644))
	}
	return root
}

func TestSourcePathRemoved(t *testing.T) {
	head := headWith(t, "apps/kept")

	tests := map[string]struct {
		source     v1alpha1.ApplicationSource
		repoTarget string
		want       bool
	}{
		"path deleted at the head": {
			source:     v1alpha1.ApplicationSource{RepoURL: removedTestRepo, Path: "apps/gone"},
			repoTarget: "remove-app",
			want:       true,
		},
		"path still at the head": {
			source:     v1alpha1.ApplicationSource{RepoURL: removedTestRepo, Path: "apps/kept"},
			repoTarget: "remove-app",
		},
		"same repo, written as ssh": {
			source:     v1alpha1.ApplicationSource{RepoURL: "git@github.com:testuser/testrepo.git", Path: "apps/gone"},
			repoTarget: "remove-app",
			want:       true,
		},
		"another repository": {
			source:     v1alpha1.ApplicationSource{RepoURL: "https://github.com/testuser/other.git", Path: "apps/gone"},
			repoTarget: "remove-app",
		},
		"another revision of this repository": {
			source:     v1alpha1.ApplicationSource{RepoURL: removedTestRepo, Path: "apps/gone"},
			repoTarget: "release",
		},
		"no path": {
			source:     v1alpha1.ApplicationSource{RepoURL: removedTestRepo},
			repoTarget: "remove-app",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, sourcePathRemoved(tc.source, removedTestPR, tc.repoTarget, head))
		})
	}
}

func TestGetManifestsSourceRemoved(t *testing.T) {
	head := headWith(t, "apps/kept")
	getRepo := func(_ context.Context, cloneURL, branch string) (*git.Repo, error) {
		require.Equal(t, removedTestPR.HeadRef, branch, "the pull request's base must be read at its head")
		return &git.Repo{CloneURL: cloneURL, BranchName: branch, Directory: head}, nil
	}
	source := func(path string) v1alpha1.ApplicationSource {
		return v1alpha1.ApplicationSource{RepoURL: removedTestRepo, Path: path, TargetRevision: "main"}
	}

	t.Run("every content source removed is a removal", func(t *testing.T) {
		app := v1alpha1.Application{Spec: v1alpha1.ApplicationSpec{Source: ptr(source("apps/gone"))}}

		manifests, err := (&ArgoClient{}).GetManifests(context.Background(), "gone", app, removedTestPR, getRepo)

		var removed *AppSourceRemovedError
		require.True(t, errors.As(err, &removed), "got %v", err)
		assert.Equal(t, []string{"apps/gone"}, removed.Paths)
		assert.Nil(t, manifests)
	})

	t.Run("a ref source is not content", func(t *testing.T) {
		app := v1alpha1.Application{Spec: v1alpha1.ApplicationSpec{Sources: v1alpha1.ApplicationSources{
			source("apps/gone"),
			{RepoURL: removedTestRepo, Ref: "values", TargetRevision: "main"},
		}}}

		_, err := (&ArgoClient{}).GetManifests(context.Background(), "gone", app, removedTestPR, getRepo)

		var removed *AppSourceRemovedError
		require.True(t, errors.As(err, &removed), "got %v", err)
		assert.Equal(t, []string{"apps/gone"}, removed.Paths)
	})
}

func TestCollectManifests(t *testing.T) {
	gone := v1alpha1.ApplicationSource{Path: "apps/gone"}
	kept := v1alpha1.ApplicationSource{Path: "apps/kept"}
	broken := v1alpha1.ApplicationSource{Path: "apps/broken"}
	generate := func(source v1alpha1.ApplicationSource) ([]string, error) {
		switch source.Path {
		case gone.Path:
			return nil, errSourcePathRemoved
		case broken.Path:
			return nil, errors.New("helm template failed")
		}
		return []string{source.Path + "-manifest"}, nil
	}

	t.Run("every source removed", func(t *testing.T) {
		_, err := collectManifests([]v1alpha1.ApplicationSource{gone}, removedTestPR, generate)
		var removed *AppSourceRemovedError
		require.True(t, errors.As(err, &removed), "got %v", err)
		assert.Equal(t, []string{"apps/gone"}, removed.Paths)
	})

	t.Run("one of several removed is an error, not a removal", func(t *testing.T) {
		_, err := collectManifests([]v1alpha1.ApplicationSource{kept, gone}, removedTestPR, generate)
		require.Error(t, err)
		var removed *AppSourceRemovedError
		assert.False(t, errors.As(err, &removed))
		assert.Contains(t, err.Error(), "apps/gone does not exist at remove-app")
	})

	t.Run("a render failure is still a failure", func(t *testing.T) {
		_, err := collectManifests([]v1alpha1.ApplicationSource{broken}, removedTestPR, generate)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "helm template failed")
	})

	t.Run("nothing removed renders as before", func(t *testing.T) {
		manifests, err := collectManifests([]v1alpha1.ApplicationSource{kept}, removedTestPR, generate)
		require.NoError(t, err)
		assert.Equal(t, []string{"apps/kept-manifest"}, manifests)
	})
}

func ptr[T any](v T) *T { return &v }
