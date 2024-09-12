package cmd

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg/git"
)

type fakeCloner struct {
	cloneUrl, branchName string
	result               *git.Repo
	err                  error
}

func (f *fakeCloner) Clone(_ context.Context, cloneUrl, branchName string) (*git.Repo, error) {
	f.cloneUrl = cloneUrl
	f.branchName = branchName
	return f.result, f.err
}

func TestMaybeCloneGitUrl_NonGitUrl(t *testing.T) {
	ctx := context.TODO()

	type testcase struct {
		name, input string
	}

	testcases := []testcase{
		{
			name:  "https url",
			input: "https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json",
		},
	}

	for _, tc := range testcases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeCloner{result: nil, err: nil}
			actual, err := maybeCloneGitUrl(ctx, fc, time.Duration(0), tc.input, testUsername)
			require.NoError(t, err)
			assert.Equal(t, "", fc.branchName)
			assert.Equal(t, "", fc.cloneUrl)
			assert.Equal(t, tc.input, actual)
		})
	}
}

const testRoot = "/tmp/path"
const testUsername = "username"

func TestMaybeCloneGitUrl_HappyPath(t *testing.T) {
	var (
		ctx = context.TODO()
	)

	type expected struct {
		path, cloneUrl, branch string
	}
	type testcase struct {
		name, input string
		expected    expected
	}

	testcases := []testcase{
		{
			name:  "ssh clone url",
			input: "git@gitlab.com:org/team/project.git",
			expected: expected{
				path:     testRoot,
				cloneUrl: fmt.Sprintf("https://%s@gitlab.com/org/team/project", testUsername),
			},
		},
		{
			name:  "http clone url",
			input: "https://gitlab.com/org/team/project.git",
			expected: expected{
				path:     testRoot,
				cloneUrl: fmt.Sprintf("https://%s@gitlab.com/org/team/project", testUsername),
			},
		},
		{
			name:  "ssh clone url with subdir",
			input: "git@gitlab.com:org/team/project.git?subdir=/charts",
			expected: expected{
				cloneUrl: fmt.Sprintf("https://%s@gitlab.com/org/team/project", testUsername),
				path:     fmt.Sprintf("%s/charts", testRoot),
			},
		},
		{
			name:  "http clone url with subdir",
			input: "https://gitlab.com/org/team/project.git?subdir=/charts",
			expected: expected{
				cloneUrl: fmt.Sprintf("https://%s@gitlab.com/org/team/project", testUsername),
				path:     fmt.Sprintf("%s/charts", testRoot),
			},
		},
		{
			name:  "ssh clone url with subdir without slash prefix",
			input: "git@gitlab.com:org/team/project.git?subdir=charts",
			expected: expected{
				cloneUrl: fmt.Sprintf("https://%s@gitlab.com/org/team/project", testUsername),
				path:     fmt.Sprintf("%s/charts", testRoot),
			},
		},
		{
			name:  "http clone url with subdir without slash prefix",
			input: "https://gitlab.com/org/team/project.git?subdir=charts",
			expected: expected{
				cloneUrl: fmt.Sprintf("https://%s@gitlab.com/org/team/project", testUsername),
				path:     fmt.Sprintf("%s/charts", testRoot),
			},
		},
		{
			name:  "local path with slash prefix",
			input: "/tmp/output",
			expected: expected{
				path: "/tmp/output",
			},
		},
		{
			name:  "local path without slash prefix",
			input: "tmp/output",
			expected: expected{
				path: "tmp/output",
			},
		},
	}

	for _, tc := range testcases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeCloner{result: &git.Repo{Directory: testRoot}, err: nil}
			actual, err := maybeCloneGitUrl(ctx, fc, time.Duration(0), tc.input, testUsername)
			require.NoError(t, err)
			assert.Equal(t, tc.expected.branch, fc.branchName)
			assert.Equal(t, tc.expected.cloneUrl, fc.cloneUrl)
			assert.Equal(t, tc.expected.path, actual)
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
			fc := &fakeCloner{result: &git.Repo{Directory: testRoot}, err: nil}
			result, err := maybeCloneGitUrl(ctx, fc, time.Duration(0), tc.input, testUsername)
			require.ErrorContains(t, err, tc.expected)
			require.Equal(t, "", result)
		})
	}
}

func TestMaybeCloneGitUrl_CloneError(t *testing.T) {
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
			ctx := context.Background()
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()

			fc := &fakeCloner{result: &git.Repo{Directory: testRoot}, err: tc.cloneError}
			result, err := maybeCloneGitUrl(ctx, fc, time.Duration(0), tc.input, testUsername)
			require.ErrorContains(t, err, tc.expected)
			require.Equal(t, "", result)
		})
	}
}

func Test_isGitURL(t *testing.T) {
	type args struct {
		str string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "git url 1",
			args: args{
				str: "https://gitlab.com/org/team/project.git",
			},
			want: true,
		},
		{
			name: "git url 2",
			args: args{
				str: "git://github.com/org/team/project.git",
			},
			want: true,
		},
		{
			name: "git url 3",
			args: args{
				str: "http://github.com/org/team/project.git",
			},
			want: true,
		},
		{
			name: "git url 4",
			args: args{
				str: "git://test.local/org/team/project.git",
			},
			want: true,
		},
		{
			name: "git url invalid 1",
			args: args{
				str: "scp://whatever.com/org/team/project.git",
			},
			want: false,
		},
		{
			name: "git url invalid 2",
			args: args{
				str: "ftp://github.com/org/team/project.git",
			},
			want: false,
		},
		{
			name: "git url invalid 3",
			args: args{
				str: "thisisnoturl",
			},
			want: false,
		},
		{
			name: "git url invalid 4",
			args: args{
				str: "http://zapier.com",
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, isGitURL(tt.args.str), "isGitURL(%v)", tt.args.str)
		})
	}
}
