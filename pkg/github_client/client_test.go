package github_client

import (
	"fmt"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestCreateClient(t *testing.T) {
	viper.Set("vcs-token", "pass")
	githubClient := createGithubClient()
	assert.Equal(t, "https://api.github.com/", githubClient.BaseURL.String(), fmt.Sprintf("api URL in githubClient (%s) does not match github public API", githubClient.BaseURL.String()))
}
