package gitlab_client

import (
	"fmt"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateClient(t *testing.T) {
	viper.Set("vcs-token", "pass")
	gitlabClient := createGitlabClient()
	assert.Equal(t, "https://gitlab.com/api/v4/", gitlabClient.BaseURL().String(), fmt.Sprintf("api URL in githubClient (%s) does not match github public API", gitlabClient.BaseURL().String()))
}

func TestCustomGitURLParsing(t *testing.T) {
	testcases := []struct {
		giturl, expected string
	}{
		{
			// subproject
			giturl:   "git@gitlab.com:zapier/project.git",
			expected: "zapier/project",
		},
		{
			// subproject
			giturl:   "git@gitlab.com:zapier/subteam/project.git",
			expected: "zapier/subteam/project",
		},
		{
			giturl:   "https://gitlab.com/zapier/argo-cd-configs.git",
			expected: "zapier/argo-cd-configs",
		},
		{
			// custom domain
			giturl:   "git@git.mycompany.com:k8s/namespaces/security",
			expected: "k8s/namespaces/security",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.giturl, func(t *testing.T) {
			actual, err := parseRepoName(tc.giturl)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
