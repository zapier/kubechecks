package cmd

import (
	"fmt"

	"github.com/zapier/kubechecks/pkg/vcs"
	"github.com/zapier/kubechecks/pkg/vcs/github_client"
	"github.com/zapier/kubechecks/pkg/vcs/gitlab_client"
)

func createVCSClient(clientType string) (vcs.Client, error) {
	switch clientType {
	case "gitlab":
		return gitlab_client.CreateGitlabClient()
	case "github":
		return github_client.CreateGithubClient()
	default:
		return nil, fmt.Errorf("unknown vcs type: %s", clientType)
	}
}
