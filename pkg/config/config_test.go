package config

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeStrings(t *testing.T) {
	testCases := []struct {
		input    string
		expected RepoURL
	}{
		{
			input:    "git@github.com:one/two",
			expected: RepoURL{"github.com", "one/two"},
		},
		{
			input:    "https://github.com/one/two",
			expected: RepoURL{"github.com", "one/two"},
		},
		{
			input:    "git@gitlab.com:djeebus/helm-test.git",
			expected: RepoURL{"gitlab.com", "djeebus/helm-test"},
		},
		{
			input:    "https://gitlab.com/djeebus/helm-test.git",
			expected: RepoURL{"gitlab.com", "djeebus/helm-test"},
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("case %s", tc.input), func(t *testing.T) {
			actual, err := NormalizeRepoUrl(tc.input)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
