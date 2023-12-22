package gitlab_client

import (
	"context"
	"errors"

	"github.com/cenkalti/backoff/v4"
	"github.com/xanzy/go-gitlab"
	"go.opentelemetry.io/otel"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/repo_config"
)

// GetProjectByIDorName gets a project by the given Project Name or ID
func (c *Client) GetProjectByID(project int) (*gitlab.Project, error) {
	var proj *gitlab.Project
	err := backoff.Retry(func() error {
		var err error
		var resp *gitlab.Response
		proj, resp, err = c.Projects.GetProject(project, nil)
		return checkReturnForBackoff(resp, err)
	}, getBackOff())
	return proj, err
}

func (c *Client) GetRepoConfigFile(ctx context.Context, projectId int, mergeReqId int) ([]byte, error) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "GetRepoConfigFile")
	defer span.End()

	// check MR branch
	for _, file := range repo_config.RepoConfigFilenameVariations() {
		b, _, err := c.RepositoryFiles.GetRawFile(
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
