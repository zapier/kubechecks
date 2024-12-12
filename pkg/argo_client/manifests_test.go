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

type repoAndTarget struct {
	repo, target string
}

func TestPackageApp(t *testing.T) {
	testCases := map[string]struct {
		app           v1alpha1.Application
		pullRequest   vcs.PullRequest
		filesByRepo   map[repoAndTarget][]string
		expectedFiles []string
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
			filesByRepo: map[repoAndTarget][]string{
				repoAndTarget{"git@github.com:testuser/testrepo.git", "main"}: {
					"app1/Chart.yaml",
					"app1/values.yaml",
					"app2/Chart.yaml",
					"app2/values.yaml",
				},
			},
			expectedFiles: []string{
				"app1/Chart.yaml",
				"app1/values.yaml",
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
			filesByRepo: map[repoAndTarget][]string{
				repoAndTarget{"git@github.com:testuser/testrepo.git", "main"}: {
					"app1/Chart.yaml",
					"app1/values.yaml",
					"app1/staging.yaml",
				},
				repoAndTarget{"git@github.com:testuser/otherrepo.git", "main"}: {
					"base.yaml",
				},
			},
			expectedFiles: []string{
				"app1/Chart.yaml",
				"app1/values.yaml",
				"app1/staging.yaml",
				"base.yaml",
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			var err error
			ctx := context.Background()

			// write garbage content for files in fake repos, and
			// track the tempdirs
			repoDirs := make(map[string]*git.Repo)
			for cloneURL, files := range tc.filesByRepo {
				repoHash := hash(t, cloneURL)
				tempDir := filepath.Join(t.TempDir(), repoHash)
				repoDirs[repoHash] = &git.Repo{
					BranchName: cloneURL.target,
					CloneURL:   cloneURL.repo,
					Directory:  tempDir,
				}

				for _, file := range files {
					fullfilepath := filepath.Join(tempDir, file)
					filedir := filepath.Dir(fullfilepath)
					err = os.MkdirAll(filedir, 0o755)
					require.NoError(t, err)
					err = os.WriteFile(fullfilepath, []byte(file), 0o600)
					require.NoError(t, err)
				}
			}

			// split sources from refs
			sources, refs := preprocessSources(&tc.app, tc.pullRequest)
			require.Len(t, sources, 1)
			source := sources[0]

			// get repos from the map, but nowhere else
			getRepo := func(ctx context.Context, cloneURL, branchName string) (*git.Repo, error) {
				repoHash := hash(t, repoAndTarget{cloneURL, branchName})
				repo, ok := repoDirs[repoHash]
				if !ok {
					return nil, errors.New("repo not found")
				}
				return repo, nil
			}

			// create a fake repo
			repo, err := getRepo(ctx, source.RepoURL, source.TargetRevision)
			require.NoError(t, err)

			// package the app
			path, err := packageApp(ctx, source, refs, repo, getRepo)
			require.NoError(t, err)

			// verify that the correct files were written
			for _, file := range tc.expectedFiles {
				fullfile := filepath.Join(path, file)
				data, err := os.ReadFile(fullfile)
				require.NoError(t, err)
				assert.Equal(t, []byte(file), data)
			}

			// verify that only the correct files were copied
			files := make(map[string]struct{})
			err = filepath.Walk(path, func(fullPath string, info fs.FileInfo, err error) error {
				require.NoError(t, err)

				if info.IsDir() {
					return nil
				}

				relPath, err := filepath.Rel(path, fullPath)
				require.NoError(t, err)

				files[relPath] = struct{}{}
				return nil
			})
			require.NoError(t, err)

			for _, file := range tc.expectedFiles {
				_, ok := files[file]
				require.Truef(t, ok, "expected file %s to exist", file)
				delete(files, file)
			}

			assert.Len(t, files, 0, "extra files were found in the output")
		})
	}
}

func hash(t *testing.T, repo repoAndTarget) string {
	t.Helper()

	url, err := pkg.Canonicalize(repo.repo)
	require.NoError(t, err)

	data := md5.Sum([]byte(url.Host + url.Path + repo.target))
	return hex.EncodeToString(data[:])
}
