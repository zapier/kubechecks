package local

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gotest.tools/v3/assert"
)

func TestParseCloneUrl(t *testing.T) {
	testcases := []struct {
		name, cloneUrl string
		expected       parsedUrl
	}{
		{
			name:     "github git url",
			cloneUrl: "git@github.com:zapier/kubechecks.git",
			expected: parsedUrl{cloneUrl: "https://github.com/zapier/kubechecks.git"},
		},
		{
			name:     "github https url",
			cloneUrl: "https://github.com/zapier/kubechecks.git",
			expected: parsedUrl{cloneUrl: "https://github.com/zapier/kubechecks.git"},
		},
		{
			name:     "gitlab git url",
			cloneUrl: "git@gitlab.com:zapier/team-sre/kubechecks.git",
			expected: parsedUrl{cloneUrl: "https://gitlab.com/zapier/team-sre/kubechecks.git"},
		},
		{
			name:     "gitlab https url",
			cloneUrl: "https://gitlab.com/zapier/team-sre/kubechecks.git",
			expected: parsedUrl{cloneUrl: "https://gitlab.com/zapier/team-sre/kubechecks.git"},
		},
		{
			name:     "gitlab git url with subdir",
			cloneUrl: "git@gitlab.com:zapier/team-sre/kubechecks.git?subdir=/hello/world",
			expected: parsedUrl{
				cloneUrl: "https://gitlab.com/zapier/team-sre/kubechecks.git",
				subdir:   "hello/world",
			},
		},
		{
			name:     "gitlab https url with subdir",
			cloneUrl: "https://gitlab.com/zapier/team-sre/kubechecks.git?subdir=hello/world",
			expected: parsedUrl{
				cloneUrl: "https://gitlab.com/zapier/team-sre/kubechecks.git",
				subdir:   "hello/world",
			},
		},
	}

	for _, tc := range testcases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			actual, err := parseCloneUrl(tc.cloneUrl)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
