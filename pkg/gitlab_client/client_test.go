package gitlab_client

import (
	"fmt"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestCreateClient(t *testing.T) {
	viper.Set("vcs-token", "pass")
	gitlabClient := createGitlabClient()
	assert.Equal(t, "https://gitlab.com/api/v4/", gitlabClient.BaseURL().String(), fmt.Sprintf("api URL in githubClient (%s) does not match github public API", gitlabClient.BaseURL().String()))
}
