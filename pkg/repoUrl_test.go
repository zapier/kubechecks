package pkg

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeStrings(t *testing.T) {
	type expected struct {
		RepoURL RepoURL
		Query   url.Values
	}
	testCases := []struct {
		input, name string
		expected    expected
	}{
		{
			name:  "simple github over ssh",
			input: "git@github.com:one/two",
			expected: expected{
				RepoURL: RepoURL{Host: "github.com", Path: "one/two"},
				Query:   make(url.Values),
			},
		},
		{
			name:  "simple github over https",
			input: "https://github.com/one/two",
			expected: expected{
				RepoURL: RepoURL{Host: "github.com", Path: "one/two"},
				Query:   make(url.Values),
			},
		},
		{
			name:  "simple gitlab over ssh",
			input: "git@gitlab.com:djeebus/helm-test.git",
			expected: expected{
				RepoURL: RepoURL{Host: "gitlab.com", Path: "djeebus/helm-test"},
				Query:   make(url.Values),
			},
		},
		{
			name:  "simple gitlab over https",
			input: "https://gitlab.com/djeebus/helm-test.git",
			expected: expected{
				RepoURL: RepoURL{Host: "gitlab.com", Path: "djeebus/helm-test"},
				Query:   make(url.Values),
			},
		},
		{
			name:  "simple gitlab over https with query",
			input: "https://gitlab.com/djeebus/helm-test.git?subdir=/blah",
			expected: expected{
				RepoURL: RepoURL{Host: "gitlab.com", Path: "djeebus/helm-test"},
				Query:   url.Values{"subdir": []string{"/blah"}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("case %s", tc.input), func(t *testing.T) {
			repoURL, query, err := NormalizeRepoUrl(tc.input)
			require.NoError(t, err)
			assert.Equal(t, tc.expected.RepoURL, repoURL)
			assert.Equal(t, tc.expected.Query, query)
		})
	}
}

func TestAreSameRepos(t *testing.T) {
	testcases := map[string]struct {
		input1, input2 string
		expected       bool
	}{
		"empty":                {"", "", true},
		"empty1":               {"", "blah", false},
		"empty2":               {"blah", "", false},
		"git-to-git":           {"git@github.com:zapier/kubechecks.git", "git@github.com:zapier/kubechecks.git", true},
		"no-git-suffix-to-git": {"git@github.com:zapier/kubechecks", "git@github.com:zapier/kubechecks.git", true},
		"https-to-git":         {"https://github.com/zapier/kubechecks", "git@github.com:zapier/kubechecks.git", true},
	}
	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			actual := AreSameRepos(tc.input1, tc.input2)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
