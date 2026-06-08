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

func TestIsHexString(t *testing.T) {
	testcases := map[string]struct {
		input    string
		expected bool
	}{
		"full-sha":        {"a3f1c2d4e5b6a7f8c9d0e1f2a3b4c5d6e7f8a9b0", true},
		"short-sha":       {"a3f1c2d", true},
		"too-short":       {"a3f1c", false},
		"branch-name":     {"main", false},
		"tag-name":        {"v1.2.3", false},
		"mixed-case-hex":  {"A3F1C2D4E5B6A7F8", true},
		"non-hex-chars":   {"a3f1g2d4", false},
		"empty":           {"", false},
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
		"nil":                        {nil, false},
		"plumbing-err-ref-not-found": {plumbing.ErrReferenceNotFound, true},
		"generic-error":              {errors.New("some other error"), false},
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
