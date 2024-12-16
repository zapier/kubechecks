package argo_client

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/git"
	"github.com/zapier/kubechecks/pkg/vcs"
)

func TestAreSameTargetRef(t *testing.T) {
	testcases := map[string]struct {
		ref1, ref2 string
		expected   bool
	}{
		"same":      {"one", "one", true},
		"different": {"one", "two", false},
	}
	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			actual := areSameTargetRef(tc.ref1, tc.ref2)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestSplitRefFromPath(t *testing.T) {
	testcases := map[string]struct {
		input         string
		refName, path string
		err           error
	}{
		"simple": {
			"$values/charts/prometheus/values.yaml", "values", "charts/prometheus/values.yaml", nil,
		},
		"too-short": {
			"$values", "", "", ErrInvalidSourceRef,
		},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			ref, path, err := splitRefFromPath(tc.input)
			if tc.err != nil {
				assert.EqualError(t, err, tc.err.Error())
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, tc.refName, ref)
			assert.Equal(t, tc.path, path)
		})
	}
}

func TestPreprocessSources(t *testing.T) {
	t.Run("one source", func(t *testing.T) {
		app := &v1alpha1.Application{
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{},
			},
		}
		pr := vcs.PullRequest{}

		sources, refs := preprocessSources(app, pr)
		assert.Len(t, sources, 1)
		assert.Len(t, refs, 0)
	})

	t.Run("one multisource", func(t *testing.T) {
		app := &v1alpha1.Application{
			Spec: v1alpha1.ApplicationSpec{
				Sources: []v1alpha1.ApplicationSource{{}},
			},
		}
		pr := vcs.PullRequest{}

		sources, refs := preprocessSources(app, pr)
		assert.Len(t, sources, 1)
		assert.Len(t, refs, 0)
	})

	t.Run("one source, one ref, needs targetrev transform", func(t *testing.T) {
		app := &v1alpha1.Application{
			Spec: v1alpha1.ApplicationSpec{
				Sources: []v1alpha1.ApplicationSource{
					{
						Ref:            "",
						RepoURL:        "git@github.com:argoproj/argo-cd.git",
						TargetRevision: "main",
					},
					{
						Ref:            "test-ref",
						RepoURL:        "https://github.com/argoproj/argo-cd.git",
						TargetRevision: "main",
					},
				},
			},
		}

		pr := vcs.PullRequest{
			CloneURL: "git@github.com:argoproj/argo-cd.git",
			BaseRef:  "main",
			HeadRef:  "test-ref",
		}

		sources, refs := preprocessSources(app, pr)
		require.Len(t, sources, 1)
		assert.Equal(t, "main", sources[0].TargetRevision)
		require.Len(t, refs, 1)
		assert.Equal(t, "test-ref", refs[0].TargetRevision)
	})

	t.Run("one source, one ref, no targetrev transform", func(t *testing.T) {
		app := &v1alpha1.Application{
			Spec: v1alpha1.ApplicationSpec{
				Sources: []v1alpha1.ApplicationSource{
					{
						Ref:            "",
						RepoURL:        "git@github.com:argoproj/argo-cd.git",
						TargetRevision: "main",
					},
					{
						Ref:            "test-ref",
						RepoURL:        "https://github.com/argoproj/argo-cd.git",
						TargetRevision: "staging",
					},
				},
			},
		}

		pr := vcs.PullRequest{
			CloneURL: "git@github.com:argoproj/argo-cd.git",
			BaseRef:  "main",
			HeadRef:  "test-ref",
		}

		sources, refs := preprocessSources(app, pr)
		require.Len(t, sources, 1)
		assert.Equal(t, "main", sources[0].TargetRevision)
		require.Len(t, refs, 1)
		assert.Equal(t, "staging", refs[0].TargetRevision)
	})
}

func TestCopyFile(t *testing.T) {
	t.Run("simple", func(t *testing.T) {
		tempSourcePath := filepath.Join(t.TempDir(), "tempsrc1.txt")
		err := os.WriteFile(tempSourcePath, []byte("hello world"), 0o600)
		require.NoError(t, err)

		tempDestinationPath := filepath.Join(t.TempDir(), "subdir", "tempdest1.txt")
		err = copyFile(tempSourcePath, tempDestinationPath)
		require.NoError(t, err)

		data, err := os.ReadFile(tempDestinationPath)
		require.NoError(t, err)

		assert.Equal(t, []byte("hello world"), data)
	})
}

type repoTarget struct {
	repo, target string
}

type repoTargetPath struct {
	repo, target, path string
}

func TestPackageApp(t *testing.T) {
	testCases := map[string]struct {
		app           v1alpha1.Application
		pullRequest   vcs.PullRequest
		filesByRepo   map[repoTarget]set[string]
		expectedFiles map[string]repoTargetPath
	}{
		"unused-paths-are-ignored": {
			app: v1alpha1.Application{
				Spec: v1alpha1.ApplicationSpec{
					Source: &v1alpha1.ApplicationSource{
						RepoURL:        "git@github.com:testuser/testrepo.git",
						Path:           "app1/",
						TargetRevision: "main",
					},
				},
			},
			filesByRepo: map[repoTarget]set[string]{
				repoTarget{"git@github.com:testuser/testrepo.git", "main"}: newSet[string](
					"app1/Chart.yaml",
					"app1/values.yaml",
					"app2/Chart.yaml",
					"app2/values.yaml",
				),
			},
			expectedFiles: map[string]repoTargetPath{
				"app1/Chart.yaml":  {"git@github.com:testuser/testrepo.git", "main", "app1/Chart.yaml"},
				"app1/values.yaml": {"git@github.com:testuser/testrepo.git", "main", "app1/values.yaml"},
			},
		},

		"missing-values-can-be-accpetable": {
			pullRequest: vcs.PullRequest{
				CloneURL: "git@github.com:testuser/testrepo.git",
				BaseRef:  "main",
				HeadRef:  "update-code",
			},

			app: v1alpha1.Application{
				Spec: v1alpha1.ApplicationSpec{
					Sources: []v1alpha1.ApplicationSource{
						{
							RepoURL:        "git@github.com:testuser/testrepo.git",
							Path:           "app1/",
							TargetRevision: "main",
							Helm: &v1alpha1.ApplicationSourceHelm{
								IgnoreMissingValueFiles: true,
								ValueFiles: []string{
									"./values.yaml",
									"missing.yaml",
									"$staging/base.yaml",
									"$staging/missing.yaml",
								},
							},
						},
						{
							Ref:            "staging",
							RepoURL:        "git@github.com:testuser/otherrepo.git",
							TargetRevision: "main",
						},
					},
				},
			},

			filesByRepo: map[repoTarget]set[string]{
				repoTarget{"git@github.com:testuser/testrepo.git", "main"}: newSet[string](
					"app1/Chart.yaml",
					"app1/values.yaml",
					"app2/Chart.yaml",
					"app2/values.yaml",
				),

				repoTarget{"git@github.com:testuser/otherrepo.git", "main"}: newSet[string](
					"base.yaml",
				),
			},

			expectedFiles: map[string]repoTargetPath{
				"app1/Chart.yaml":         {"git@github.com:testuser/testrepo.git", "main", "app1/Chart.yaml"},
				"app1/values.yaml":        {"git@github.com:testuser/testrepo.git", "main", "app1/values.yaml"},
				".refs/staging/base.yaml": {"git@github.com:testuser/otherrepo.git", "main", "base.yaml"},
			},
		},

		"refs-are-copied": {
			pullRequest: vcs.PullRequest{
				CloneURL: "git@github.com:testuser/testrepo.git",
				BaseRef:  "main",
				HeadRef:  "update-code",
			},
			app: v1alpha1.Application{
				Spec: v1alpha1.ApplicationSpec{
					Sources: []v1alpha1.ApplicationSource{
						{
							RepoURL:        "git@github.com:testuser/testrepo.git",
							Path:           "app1/",
							TargetRevision: "main",
							Helm: &v1alpha1.ApplicationSourceHelm{
								ValueFiles: []string{
									"./values.yaml",
									"./staging.yaml",
									"$staging/base.yaml",
								},
							},
						},
						{
							Ref:            "staging",
							RepoURL:        "git@github.com:testuser/otherrepo.git",
							TargetRevision: "main",
						},
					},
				},
			},
			filesByRepo: map[repoTarget]set[string]{
				repoTarget{"git@github.com:testuser/testrepo.git", "main"}: newSet[string](
					"app1/Chart.yaml",
					"app1/values.yaml",
					"app1/staging.yaml",
				),
				repoTarget{"git@github.com:testuser/otherrepo.git", "main"}: newSet[string](
					"base.yaml",
				),
			},
			expectedFiles: map[string]repoTargetPath{
				"app1/Chart.yaml":         {"git@github.com:testuser/testrepo.git", "main", "app1/Chart.yaml"},
				"app1/values.yaml":        {"git@github.com:testuser/testrepo.git", "main", "app1/values.yaml"},
				"app1/staging.yaml":       {"git@github.com:testuser/testrepo.git", "main", "app1/staging.yaml"},
				".refs/staging/base.yaml": {"git@github.com:testuser/otherrepo.git", "main", "base.yaml"},
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			var err error
			ctx := context.Background()

			// write garbage content for files in fake repos, and
			// store the tempdirs as repos
			repoDirs, fileContentByRepo := createTestRepos(t, tc.filesByRepo)

			// split sources from refs
			sources, refs := preprocessSources(&tc.app, tc.pullRequest)
			require.Len(t, sources, 1)
			source := sources[0]

			// get repos from the map, but nowhere else
			getRepo := func(ctx context.Context, cloneURL, branchName string) (*git.Repo, error) {
				repoHash := hash(t, repoTarget{cloneURL, branchName})
				repo, ok := repoDirs[repoHash]
				if !ok {
					return nil, errors.New("repo not found")
				}
				return repo, nil
			}

			// get the source repo, which was created above
			repo, err := getRepo(ctx, source.RepoURL, source.TargetRevision)
			require.NoError(t, err)

			// FUNCTION UNDER TEST: package the app
			path, err := packageApp(ctx, source, refs, repo, getRepo)
			require.NoError(t, err)

			// ensure that only the expected files were copied
			actualFiles := makeRelPathFilesSet(t, path)
			expectedFilesSet := makeExpectedFilesSet(t, tc.expectedFiles)
			extraCopiedFiles := actualFiles.Minus(expectedFilesSet)
			assert.Empty(t, extraCopiedFiles, "extra files have been copied")
			missingCopiedFiles := expectedFilesSet.Minus(actualFiles)
			assert.Empty(t, missingCopiedFiles, "files that should have been packaged are missing")

			// verify that the correct files were written
			for file, config := range tc.expectedFiles {
				fullfile := filepath.Join(path, file)
				actual, err := os.ReadFile(fullfile)
				expected := fileContentByRepo[config]
				if assert.NoError(t, err) {
					assert.Equal(t, expected, string(actual))
				}
			}
		})
	}
}

func makeExpectedFilesSet(t *testing.T, files map[string]repoTargetPath) set[string] {
	t.Helper()

	result := newSet[string]()

	for path := range files {
		result.Add(path)
	}

	return result
}

func createTestRepos(t *testing.T, filesByRepo map[repoTarget]set[string]) (map[string]*git.Repo, map[repoTargetPath]string) {
	repoDirs := make(map[string]*git.Repo)
	fileContents := make(map[repoTargetPath]string)

	var err error

	for cloneURL, files := range filesByRepo {
		repoHash := hash(t, cloneURL)
		tempDir := filepath.Join(t.TempDir(), repoHash)
		repoDirs[repoHash] = &git.Repo{
			BranchName: cloneURL.target,
			CloneURL:   cloneURL.repo,
			Directory:  tempDir,
		}

		for file := range files {
			fullfilepath := filepath.Join(tempDir, file)

			// ensure the directories exist
			filedir := filepath.Dir(fullfilepath)
			err = os.MkdirAll(filedir, 0o755)
			require.NoError(t, err)

			// generate and store content
			fileContent := uuid.NewString()
			fileContents[repoTargetPath{cloneURL.repo, cloneURL.target, file}] = fileContent

			// write the file to disk
			err = os.WriteFile(fullfilepath, []byte(fileContent), 0o600)
			require.NoError(t, err)
		}
	}

	return repoDirs, fileContents
}

func makeRelPathFilesSet(t *testing.T, path string) set[string] {
	files := newSet[string]()
	err := filepath.Walk(path, func(fullPath string, info fs.FileInfo, err error) error {
		require.NoError(t, err)

		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(path, fullPath)
		require.NoError(t, err)

		files.Add(relPath)
		return nil
	})
	require.NoError(t, err)
	return files
}

func hash(t *testing.T, repo repoTarget) string {
	t.Helper()

	url, err := pkg.Canonicalize(repo.repo)
	require.NoError(t, err)

	data := md5.Sum([]byte(url.Host + url.Path + repo.target))
	return hex.EncodeToString(data[:])
}

type set[T comparable] map[T]struct{}

func newSet[T comparable](items ...T) set[T] {
	result := make(set[T])
	for _, item := range items {
		result.Add(item)
	}
	return result
}

func (s set[T]) Add(value T) {
	s[value] = struct{}{}
}

func (s set[T]) Remove(value T) {
	delete(s, value)
}

func (s set[T]) Minus(other set[T]) set[T] {
	result := newSet[T]()
	for k := range s {
		result.Add(k)
	}
	for k := range other {
		result.Remove(k)
	}
	return result
}
