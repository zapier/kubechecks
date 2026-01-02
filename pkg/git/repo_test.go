package git

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg/config"
)

func TestRepoGetRemoteHead(t *testing.T) {
	cfg := config.ServerConfig{}
	ctx := context.TODO()

	repo := New(cfg, "https://github.com/zapier/kubechecks.git", "")
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
