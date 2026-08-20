package git

import (
	"context"
	"errors"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg/config"
)

func TestRepoGetAuth(t *testing.T) {
	ctx := context.Background()

	t.Run("credential provider success", func(t *testing.T) {
		cfg := config.ServerConfig{
			GitCreds: func(context.Context) (string, string, error) {
				return "x-access-token", "ghs_installation_token", nil
			},
		}
		repo := New(cfg, "https://github.com/zapier/kubechecks.git", "")

		auth, err := repo.getAuth(ctx)
		require.NoError(t, err)
		require.NotNil(t, auth)
		assert.Equal(t, "x-access-token", auth.Username)
		assert.Equal(t, "ghs_installation_token", auth.Password)
	})

	t.Run("credential provider empty password means anonymous", func(t *testing.T) {
		cfg := config.ServerConfig{
			GitCreds: func(context.Context) (string, string, error) {
				return "x-access-token", "", nil
			},
		}
		repo := New(cfg, "https://github.com/zapier/kubechecks.git", "")

		auth, err := repo.getAuth(ctx)
		require.NoError(t, err)
		assert.Nil(t, auth)
	})

	t.Run("credential provider error propagates without duplicate wrapping", func(t *testing.T) {
		cfg := config.ServerConfig{
			GitCreds: func(context.Context) (string, string, error) {
				return "", "", errors.New("token fetch failed")
			},
		}
		repo := New(cfg, "https://github.com/zapier/kubechecks.git", "")

		auth, err := repo.getAuth(ctx)
		require.Error(t, err)
		assert.Nil(t, auth)
		assert.Equal(t, "token fetch failed", err.Error())
	})

	t.Run("falls back to VcsToken when no provider configured", func(t *testing.T) {
		cfg := config.ServerConfig{VcsToken: "ghp_static_pat"}
		repo := New(cfg, "https://github.com/zapier/kubechecks.git", "")

		auth, err := repo.getAuth(ctx)
		require.NoError(t, err)
		require.NotNil(t, auth)
		assert.Equal(t, "git", auth.Username)
		assert.Equal(t, "ghp_static_pat", auth.Password)
	})

	t.Run("anonymous when neither provider nor VcsToken configured", func(t *testing.T) {
		repo := New(config.ServerConfig{}, "https://github.com/zapier/kubechecks.git", "")

		auth, err := repo.getAuth(ctx)
		require.NoError(t, err)
		assert.Nil(t, auth)
	})
}

func TestRepoGetRemoteHead(t *testing.T) {
	cfg := config.ServerConfig{}
	ctx := context.TODO()

	repo := New(cfg, "https://github.com/zapier/kubechecks.git", "")
	repo.BranchName = "gh-pages"
	err := repo.Clone(ctx)
	require.NoError(t, err)

	t.Cleanup(repo.Wipe)

	branch, err := repo.GetRemoteHead(ctx)
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

func TestIsHexString(t *testing.T) {
	testcases := map[string]struct {
		input    string
		expected bool
	}{
		"full-sha":       {"a3f1c2d4e5b6a7f8c9d0e1f2a3b4c5d6e7f8a9b0", true},
		"short-sha":      {"a3f1c2d", true},
		"too-short":      {"a3f1c", false},
		"branch-name":    {"main", false},
		"tag-name":       {"v1.2.3", false},
		"mixed-case-hex": {"A3F1C2D4E5B6A7F8", true},
		"non-hex-chars":  {"a3f1g2d4", false},
		"empty":          {"", false},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			actual := isHexString(tc.input)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestIsRefNotFound(t *testing.T) {
	testcases := map[string]struct {
		err      error
		expected bool
	}{
		"nil":                              {nil, false},
		"plumbing-err-ref-not-found":       {plumbing.ErrReferenceNotFound, true},
		"generic-error":                    {errors.New("some other error"), false},
		"string-match-reference-not-found": {errors.New("reference not found"), true},
		"string-match-remote-ref":          {errors.New("couldn't find remote ref refs/heads/nonexistent"), true},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			actual := isRefNotFound(tc.err)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
