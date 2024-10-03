package gitlab_client

import (
	"context"
	"errors"

	"github.com/cenkalti/backoff/v4"
	"github.com/xanzy/go-gitlab"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/repo_config"
)

// GetProjectByID gets a project by the given Project Name or ID
func (c *Client) GetProjectByID(project int) (*gitlab.Project, error) {
	var proj *gitlab.Project
	err := backoff.Retry(func() error {
		var err error
		var resp *gitlab.Response
		proj, resp, err = c.c.Projects.GetProject(project, nil)
		return checkReturnForBackoff(resp, err)
	}, getBackOff())
	return proj, err
}

func (c *Client) GetRepoConfigFile(ctx context.Context, projectId int, mergeReqId int) ([]byte, error) {
	_, span := tracer.Start(ctx, "GetRepoConfigFile")
	defer span.End()

	// check MR branch
	for _, file := range repo_config.RepoConfigFilenameVariations() {
		b, _, err := c.c.RepositoryFiles.GetRawFile(
			projectId,
			file,
			&gitlab.GetRawFileOptions{Ref: pkg.Pointer("HEAD")},
		)
		if err != nil {
			continue
		}
		return b, nil
	}

	return nil, errors.New(".kubecheck.yaml file not found")
}
