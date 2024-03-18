package cmd

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg/git"
)

type fakeCloner struct {
	result *git.Repo
	err    error
}

func (f fakeCloner) Clone(ctx context.Context, cloneUrl, branchName string) (*git.Repo, error) {
	return f.result, f.err
}

const testRoot = "/tmp/path"

func TestMaybeCloneGitUrl_HappyPath(t *testing.T) {
	var (
		ctx = context.TODO()
	)

	testcases := []struct {
		name, input, expected string
	}{
		{
			name:     "ssh clone url",
			input:    "git@gitlab.com:org/team/project.git",
			expected: testRoot,
		},
		{
			name:     "http clone url",
			input:    "https://gitlab.com/org/team/project.git",
			expected: testRoot,
		},
		{
			name:     "ssh clone url with subdir",
			input:    "git@gitlab.com:org/team/project.git?subdir=/charts",
			expected: filepath.Join(testRoot, "charts"),
		},
		{
			name:     "http clone url with subdir",
			input:    "https://gitlab.com/org/team/project.git?subdir=/charts",
			expected: filepath.Join(testRoot, "charts"),
		},
		{
			name:     "ssh clone url with subdir without slash prefix",
			input:    "git@gitlab.com:org/team/project.git?subdir=charts",
			expected: filepath.Join(testRoot, "charts"),
		},
		{
			name:     "http clone url with subdir without slash prefix",
			input:    "https://gitlab.com/org/team/project.git?subdir=charts",
			expected: filepath.Join(testRoot, "charts"),
		},
		{
			name:     "local path with slash prefix",
			input:    "/tmp/output",
			expected: "/tmp/output",
		},
		{
			name:     "local path without slash prefix",
			input:    "tmp/output",
			expected: "tmp/output",
		},
	}

	for _, tc := range testcases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			actual, err := maybeCloneGitUrl(ctx, fakeCloner{&git.Repo{Directory: testRoot}, nil}, tc.input)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestMaybeCloneGitUrl_URLError(t *testing.T) {
	var (
		ctx = context.TODO()
	)

	testcases := []struct {
		name, input, expected string
	}{
		{
			name:     "cannot use query with file path",
			input:    "/blahblah?subdir=blah",
			expected: "relative and absolute file paths cannot have query parameters",
		},
	}

	for _, tc := range testcases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result, err := maybeCloneGitUrl(ctx, fakeCloner{&git.Repo{Directory: testRoot}, nil}, tc.input)
			require.ErrorContains(t, err, tc.expected)
			require.Equal(t, "", result)
		})
	}
}

func TestMaybeCloneGitUrl_CloneError(t *testing.T) {
	var (
		ctx = context.TODO()
	)

	testcases := []struct {
		name, input, expected string
		cloneError            error
	}{
		{
			name:       "failed to clone",
			input:      "github.com/blah/blah",
			cloneError: errors.New("blahblah"),
			expected:   "failed to clone: blahblah",
		},
	}

	for _, tc := range testcases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result, err := maybeCloneGitUrl(ctx, fakeCloner{&git.Repo{Directory: testRoot}, tc.cloneError}, tc.input)
			require.ErrorContains(t, err, tc.expected)
			require.Equal(t, "", result)
		})
	}
}
