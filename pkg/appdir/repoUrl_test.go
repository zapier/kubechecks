package appdir

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stretchr/testify/assert"
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

// TestBuildNormalizedRepoURL tests the buildNormalizedRepoUrl function.
func TestBuildNormalizedRepoURL(t *testing.T) {
	tests := []struct {
		host     string
		path     string
		expected RepoURL
	}{
		{
			host: "example.com",
			path: "/repository.git",
			expected: RepoURL{
				Host: "example.com",
				Path: "repository",
			},
		},
		// ... additional test cases
	}

	for _, tc := range tests {
		result := buildNormalizedRepoUrl(tc.host, tc.path)
		assert.Equal(t, tc.expected, result)
	}
}
