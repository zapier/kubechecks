package git

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg/config"
)

func wipe(t *testing.T, path string) {
	err := os.RemoveAll(path)
	require.NoError(t, err)
}

func TestRepoRoundTrip(t *testing.T) {
	originRepo, err := os.MkdirTemp("", "kubechecks-test-")
	require.NoError(t, err)
	defer wipe(t, originRepo)

	// initialize the test repo
	cmd := exec.Command("/bin/sh", "-c", `#!/usr/bin/env bash
set -e
set -x

# set up git repo
cd $TEMPDIR
git init
git config user.email "user@test.com"
git config user.name "Zap Zap"

# set up main branch
git branch -m main

echo "one" > abc.txt
git add abc.txt
git commit -m "commit one on main"

# set up testing branch
git checkout -b testing
echo "three" > abc.txt
git add abc.txt
git commit -m "commit two on testing"

# add commit back to main
git checkout main
echo "four" > def.txt
git add def.txt
git commit -m "commit two on main"

# pull main into testing
git checkout testing
git merge main
echo "two" > ghi.txt
git add ghi.txt
git commit -m "commit three"
`)
	cmd.Env = append(cmd.Env, "TEMPDIR="+originRepo)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	err = cmd.Run()
	require.NoError(t, err)

	cmd = exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = originRepo
	output, err := cmd.Output()
	require.NoError(t, err)
	sha := strings.TrimSpace(string(output))

	var cfg config.ServerConfig
	ctx := context.Background()
	repo := New(cfg, originRepo, "main")

	err = repo.Clone(ctx)
	require.NoError(t, err)
	defer wipe(t, repo.Directory)

	err = repo.MergeIntoTarget(ctx, sha)
	require.NoError(t, err)

	files, err := repo.GetListOfChangedFiles(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"abc.txt", "ghi.txt"}, files)
}

func TestRepoGetRemoteHead(t *testing.T) {
	cfg := config.ServerConfig{}
	ctx := context.TODO()

	repo := New(cfg, "https://github.com/zapier/kubechecks.git", "")
	repo.Shallow = true
	repo.BranchName = "gh-pages"
	err := repo.Clone(ctx)
	require.NoError(t, err)

	t.Cleanup(repo.Wipe)

	branch, err := repo.GetRemoteHead()
	require.NoError(t, err)
	assert.Equal(t, "main", branch)
	currentBranch, err := repo.GetCurrentBranch()
	require.NoError(t, err)
	assert.Equal(t, "gh-pages", currentBranch)
}

func TestBuildCloneURL(t *testing.T) {
	tests := map[string]struct {
		VcsBaseUrl, VcsUsername, VcsToken string
		expectedResult                    string
		expectError                       bool
	}{
		"custom VcsBaseUrl": {
			VcsBaseUrl:     "https://git.example.com",
			VcsToken:       "token123",
			VcsUsername:    "testuser",
			expectedResult: "https://testuser:token123@git.example.com",
			expectError:    false,
		},
		"custom VcsBaseUrl with http scheme": {
			VcsBaseUrl:     "http://git.internal.com",
			VcsToken:       "token123",
			VcsUsername:    "testuser",
			expectedResult: "http://testuser:token123@git.internal.com",
			expectError:    false,
		},
		"invalid VcsBaseUrl": {
			VcsBaseUrl:  "://invalid-url",
			VcsToken:    "token123",
			VcsUsername: "testuser",
			expectError: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := BuildCloneURL(tt.VcsBaseUrl, tt.VcsUsername, tt.VcsToken)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResult, result)
			}
		})
	}
}
